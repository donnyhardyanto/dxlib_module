package credentials

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/donnyhardyanto/dxlib/core"
	dxRedis "github.com/donnyhardyanto/dxlib/redis"
	goredis "github.com/go-redis/redis/v8"
)

// RedisConnection is a Valkey (or Redis) connection made through dxlib's redis package with credentials from a
// Provider. It reconnects with a fresh credential when the current one is about to expire, when the server
// refuses it (WRONGPASS, NOAUTH), or when Refresh is called after a rotation. What is stored in Valkey is not
// touched by a reconnect, so open sessions survive it.
//
// Its methods take and return plain Go values (RESP replies as string, int64, []interface{} or nil), so no client
// library type is part of this package's API.
//
// Each new credential is first tried with a short-lived single connection that is closed afterwards, and only a
// credential the server accepts is handed to dxlib's Connect: dxlib keeps a client it failed to connect running
// (with a health check that pings for the life of the process), so a failed Connect must not happen in a loop.
// After a failed attempt the next one waits RetryMin, doubling per consecutive failure up to RetryMax; in between
// the last error is returned at once.
//
// dxlib's redis package connects to one node (or a Sentinel-managed primary); Valkey Cluster is not supported.
// It has no TLS option today, so the connection is plain TCP: keep Valkey on a private network until dxlib
// offers TLS.
type RedisConnection struct {
	NameId         string
	Address        string
	DatabaseIndex  int
	CredentialName string
	Provider       Provider
	// RenewBefore is how long before a credential's ExpiresAt a new one is fetched. Default one minute.
	RenewBefore time.Duration
	// CloseGrace is how long a replaced connection stays open for commands already running on it. Default 30 s.
	CloseGrace time.Duration
	// RetryMin and RetryMax bound the wait after a failed connection attempt. Defaults 1 s and 30 s.
	RetryMin, RetryMax time.Duration
	// ProbeTimeout bounds the trial connection of a new credential. Default 5 s.
	ProbeTimeout time.Duration

	mu       sync.Mutex
	current  *dxRedis.DXRedis
	cred     Credential
	gen      uint64 // counts connections made; a forced reconnect asked for an older one is not repeated
	failures int
	lastFail time.Time
	lastErr  error
	attempts int // trial connections made, for tests
}

// NewRedisConnection returns a connection that is made on first use.
func NewRedisConnection(nameId, address string, databaseIndex int, credentialName string, provider Provider) *RedisConnection {
	return &RedisConnection{
		NameId:         nameId,
		Address:        address,
		DatabaseIndex:  databaseIndex,
		CredentialName: credentialName,
		Provider:       provider,
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Describe names the connection for error messages: name, address and database, never the credential.
func (c *RedisConnection) Describe() string {
	return fmt.Sprintf("Valkey %s at %s/%d", c.NameId, c.Address, c.DatabaseIndex)
}

type connectMode int

const (
	modeUse     connectMode = iota // connect when not connected or the credential is about to expire
	modeRefresh                    // reconnect when the provider has a different credential
	modeRefused                    // the server refused the credential of connection generation `used`
)

// retryWait returns how long the next connection attempt must still wait. Called with c.mu held.
func (c *RedisConnection) retryWait() time.Duration {
	if c.failures == 0 {
		return 0
	}
	d, max := orDefault(c.RetryMin, time.Second), orDefault(c.RetryMax, 30*time.Second)
	for i := 1; i < c.failures && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d - time.Since(c.lastFail)
}

// fail records a failed attempt for the back-off. An attempt that failed because the caller's own context ended
// says nothing about the server and is not counted.
func (c *RedisConnection) fail(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	c.failures++
	c.lastFail = time.Now()
	c.lastErr = err
	return err
}

func sameCredential(a, b Credential) bool {
	return a.Version == b.Version && a.Username == b.Username && a.Password == b.Password && a.ExpiresAt.Equal(b.ExpiresAt)
}

// client returns the live client and its generation, connecting or reconnecting when needed.
func (c *RedisConnection) client(ctx context.Context, mode connectMode, used uint64) (goredis.UniversalClient, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == modeRefused && c.current != nil && c.gen != used {
		// Another call reconnected after this one took its client: use the new connection.
		return c.current.Connection, c.gen, nil
	}
	expiring := !c.cred.ExpiresAt.IsZero() && time.Until(c.cred.ExpiresAt) < orDefault(c.RenewBefore, time.Minute)
	if c.current != nil && mode == modeUse && !expiring {
		return c.current.Connection, c.gen, nil
	}
	if wait := c.retryWait(); wait > 0 {
		if c.current != nil && mode == modeUse {
			// The credential is close to expiry but still valid: keep using it until the next attempt is due.
			// Refresh and a refused credential get the last error instead, so a rotation notice is not lost silently.
			return c.current.Connection, c.gen, nil
		}
		return nil, 0, fmt.Errorf("%s: no new connection attempt for another %s after %d failed in a row; the last one: %w",
			c.Describe(), wait.Round(time.Millisecond), c.failures, c.lastErr)
	}
	cred, err := c.Provider.Credential(ctx, c.CredentialName)
	if err != nil {
		return nil, 0, c.fail(ctx, fmt.Errorf("%s: reading credential %q from the credential provider: %w", c.Describe(), c.CredentialName, err))
	}
	if c.current != nil && sameCredential(cred, c.cred) {
		if mode == modeRefused {
			// A new connection with the credential the server just refused would be refused too.
			return nil, 0, c.fail(ctx, fmt.Errorf("%s refused credential version %q of user %q, and the credential provider still returns that credential: the rotation has not reached the provider yet, or the server's user was changed outside it",
				c.Describe(), cred.Version, cred.Username))
		}
		// The provider has nothing newer yet; keep the working connection.
		return c.current.Connection, c.gen, nil
	}
	c.attempts++
	if err := c.probe(ctx, cred); err != nil {
		return nil, 0, c.fail(ctx, fmt.Errorf("%s: connecting as %q (credential version %q): %w", c.Describe(), cred.Username, cred.Version, err))
	}
	r := &dxRedis.DXRedis{
		NameId:        c.NameId,
		IsConfigured:  true, // never read from configuration: the credential comes from the provider
		Address:       c.Address,
		UserName:      cred.Username,
		HasUserName:   cred.Username != "",
		Password:      cred.Password,
		HasPassword:   cred.Password != "",
		DatabaseIndex: c.DatabaseIndex,
		MustConnected: false, // true would end the process on a failed connect
		Context:       core.RootContext,
	}
	if r.Context == nil {
		r.Context = context.Background()
	}
	if err := r.Connect(); err != nil {
		return nil, 0, c.fail(ctx, fmt.Errorf("%s: connecting as %q (credential version %q) after a trial connection succeeded: %w", c.Describe(), cred.Username, cred.Version, ExplainUnreachable(err, c.Address)))
	}
	old := c.current
	c.current, c.cred = r, cred
	c.gen++
	c.failures, c.lastErr = 0, nil
	if old != nil {
		time.AfterFunc(orDefault(c.CloseGrace, 30*time.Second), func() { _ = old.Disconnect() })
	}
	return r.Connection, c.gen, nil
}

// probe tries a credential on one plain connection, closed afterwards, so a refused or unreachable server leaves
// nothing running.
func (c *RedisConnection) probe(ctx context.Context, cred Credential) error {
	ctx, cancel := context.WithTimeout(ctx, orDefault(c.ProbeTimeout, 5*time.Second))
	defer cancel()
	cl := goredis.NewClient(&goredis.Options{Addr: c.Address, Username: cred.Username, Password: cred.Password, DB: c.DatabaseIndex,
		PoolSize: 1, MaxRetries: -1, IdleCheckFrequency: -1})
	defer func() { _ = cl.Close() }()
	return cl.Ping(ctx).Err()
}

// do runs fn on the live client. When the server refuses the credential, do fetches a new one, reconnects and runs
// fn once more. Errors name the connection and keep the server's own text.
func (c *RedisConnection) do(ctx context.Context, op string, fn func(goredis.UniversalClient) error) error {
	cl, gen, err := c.client(ctx, modeUse, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	err = fn(cl)
	if err != nil && IsAuthError(err) {
		cl, _, rerr := c.client(ctx, modeRefused, gen)
		if rerr != nil {
			return fmt.Errorf("%s: %s refused the credential (%v), and reconnecting failed: %w", op, c.Describe(), err, rerr)
		}
		err = fn(cl)
	}
	if err != nil {
		return fmt.Errorf("%s on %s: %w", op, c.Describe(), ExplainUnreachable(err, c.Address))
	}
	return nil
}

func nilReply(v interface{}, err error) (interface{}, error) {
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	return v, err
}

// Eval runs a Lua script, by its SHA-1 when the server has it cached and with its full text otherwise (as EVALSHA,
// then EVAL on NOSCRIPT). A nil reply is (nil, nil). op names the operation in errors.
func (c *RedisConnection) Eval(ctx context.Context, op, script string, keys []string, args ...interface{}) (interface{}, error) {
	sum := sha1.Sum([]byte(script))
	sha := hex.EncodeToString(sum[:])
	var res interface{}
	err := c.do(ctx, op, func(cl goredis.UniversalClient) error {
		r, err := nilReply(cl.EvalSha(ctx, sha, keys, args...).Result())
		if err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
			r, err = nilReply(cl.Eval(ctx, script, keys, args...).Result())
		}
		res = r
		return err
	})
	return res, err
}

// HGet returns a hash field and whether it exists.
func (c *RedisConnection) HGet(ctx context.Context, op, key, field string) (string, bool, error) {
	var v string
	found := false
	err := c.do(ctx, op, func(cl goredis.UniversalClient) error {
		r, err := cl.HGet(ctx, key, field).Result()
		if errors.Is(err, goredis.Nil) {
			return nil
		}
		v, found = r, err == nil
		return err
	})
	return v, found, err
}

// Command runs one command given as its words ("SET", key, value) and returns the reply; a nil reply is (nil, nil).
func (c *RedisConnection) Command(ctx context.Context, op string, args ...interface{}) (interface{}, error) {
	var res interface{}
	err := c.do(ctx, op, func(cl goredis.UniversalClient) error {
		r, err := nilReply(cl.Do(ctx, args...).Result())
		res = r
		return err
	})
	return res, err
}

// Refresh reconnects at once when the provider has a different credential, as after a rotation notice; with the
// same credential it keeps the connection. Within the back-off after a failed attempt it returns that failure.
func (c *RedisConnection) Refresh(ctx context.Context) error {
	_, _, err := c.client(ctx, modeRefresh, 0)
	return err
}

// CredentialVersion returns the version of the credential the live connection was made with ("" before the first
// connection).
func (c *RedisConnection) CredentialVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cred.Version
}

// Close disconnects.
func (c *RedisConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil
	}
	err := c.current.Disconnect()
	c.current = nil
	return err
}

// IsAuthError reports whether the server refused the connection's credential.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "WRONGPASS") || strings.Contains(s, "NOAUTH")
}

// ExplainUnreachable adds the network's own reason to go-redis's "all ring shards are down", which hides it:
// dxlib connects through a one-shard ring, and once its health check marks the node down every command fails with
// that text alone.
func ExplainUnreachable(err error, address string) error {
	if err == nil || !strings.Contains(err.Error(), "ring shards are down") {
		return err
	}
	conn, dialErr := net.DialTimeout("tcp", address, time.Second)
	if dialErr != nil {
		return fmt.Errorf("%w (dialing %s: %v)", err, address, dialErr)
	}
	_ = conn.Close()
	return fmt.Errorf("%w (%s accepts TCP again; the client retries on its next health check)", err, address)
}
