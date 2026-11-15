package credentials

import (
	"context"
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

	mu      sync.Mutex
	current *dxRedis.DXRedis
	cred    Credential
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

func (c *RedisConnection) renewBefore() time.Duration {
	if c.RenewBefore > 0 {
		return c.RenewBefore
	}
	return time.Minute
}

func (c *RedisConnection) closeGrace() time.Duration {
	if c.CloseGrace > 0 {
		return c.CloseGrace
	}
	return 30 * time.Second
}

// Describe names the connection for error messages: name, address and database, never the credential.
func (c *RedisConnection) Describe() string {
	return fmt.Sprintf("Valkey %s at %s/%d", c.NameId, c.Address, c.DatabaseIndex)
}

// client returns the live client, connecting or reconnecting when needed.
func (c *RedisConnection) client(ctx context.Context, force bool) (goredis.UniversalClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiring := !c.cred.ExpiresAt.IsZero() && time.Until(c.cred.ExpiresAt) < c.renewBefore()
	if c.current != nil && !force && !expiring {
		return c.current.Connection, nil
	}
	cred, err := c.Provider.Credential(ctx, c.CredentialName)
	if err != nil {
		return nil, fmt.Errorf("%s: reading credential %q from the credential provider: %w", c.Describe(), c.CredentialName, err)
	}
	if c.current != nil && !force && cred.Version == c.cred.Version && cred.ExpiresAt.Equal(c.cred.ExpiresAt) {
		// The provider has nothing newer yet; keep the working connection.
		return c.current.Connection, nil
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
		return nil, fmt.Errorf("%s: connecting as %q (credential version %q): %w", c.Describe(), cred.Username, cred.Version, ExplainUnreachable(err, c.Address))
	}
	old := c.current
	c.current, c.cred = r, cred
	if old != nil {
		time.AfterFunc(c.closeGrace(), func() { _ = old.Disconnect() })
	}
	return r.Connection, nil
}

// Do runs fn on the live client. When the server refuses the credential, Do fetches a new one, reconnects and runs
// fn once more. Errors name the connection and keep the server's own text.
func (c *RedisConnection) Do(ctx context.Context, op string, fn func(goredis.UniversalClient) error) error {
	cl, err := c.client(ctx, false)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	err = fn(cl)
	if err != nil && IsAuthError(err) {
		cl, rerr := c.client(ctx, true)
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

// Refresh reconnects with the provider's current credential at once, as after a rotation notice.
func (c *RedisConnection) Refresh(ctx context.Context) error {
	_, err := c.client(ctx, true)
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
