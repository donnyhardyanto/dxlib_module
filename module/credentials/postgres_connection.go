package credentials

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/log"
)

// PostgresConnection is a PostgreSQL connection pool made through dxlib's databases package with credentials from a
// Provider: the counterpart of RedisConnection. Its Tx is the one the DBStores of module/password, module/passkey
// and module/user_photo take, so it drops in where a *databases.DXDatabase went.
//
// It asks the provider again when the credential is within RenewBefore of its ExpiresAt (half the credential's
// lifetime when that is shorter), when Refresh is called after a rotation notice, and when the server refuses the
// credential (SQLSTATE 28P01 or 28000) as a transaction begins. The same credential with a later ExpiresAt (a
// renewed lease) only moves the expiry; a different one opens a new pool, and new transactions go to it. The old
// pool is closed once the last transaction already running on it has finished, so a rotation loses no work, and
// what is stored is not touched by a reconnect.
//
// A transaction whose callback has started is never run again. Before the callback runs, a refused credential is
// retried on a new pool with the provider's newer credential, and a connection the server had ended (a revoked lease
// ends its user's sessions) on the same pool; at most two retries.
//
// After a failed connection attempt the next one waits RetryMin, doubling per consecutive failure up to RetryMax;
// in between the last error is returned at once. A failed attempt leaves no pool running.
type PostgresConnection struct {
	NameId string
	// Address is host:port.
	Address      string
	DatabaseName string
	// ConnectionOptions are further libpq keywords, space-separated key=value pairs without quotes, for example
	// "sslmode=verify-full sslrootcert=/etc/ssl/db-ca.pem". The credential and the address are not accepted here:
	// user, password, passfile, host, hostaddr, port, dbname, service and servicefile are refused.
	ConnectionOptions string
	CredentialName    string
	Provider          Provider
	// RenewBefore is how long before a credential's ExpiresAt the provider is asked again. Default one minute, or
	// half the credential's lifetime when that is shorter.
	RenewBefore time.Duration
	// RetryMin and RetryMax bound the wait after a failed connection attempt. Defaults 1 s and 30 s.
	RetryMin, RetryMax time.Duration
	// ConnectTimeout bounds opening one connection. Default 5 s.
	ConnectTimeout time.Duration
	// Pool settings, passed to dxlib (0: dxlib's default).
	PoolMaxOpenConns, PoolMaxIdleConns                     int
	PoolConnMaxLifetimeMinutes, PoolConnMaxIdleTimeMinutes int

	mu       sync.Mutex
	current  *pgPool
	cred     Credential
	credAt   time.Time // when cred (or its last renewal) was received
	gen      uint64
	failures int
	lastFail time.Time
	lastErr  error
	attempts int // connection attempts made, for tests
	closed   bool
	retired  []*pgPool // replaced pools still running transactions, for tests
}

type pgPool struct {
	db       *databases.DXDatabase
	gen      uint64
	inflight int
	retired  bool
}

// NewPostgresConnection returns a connection that is made on first use.
func NewPostgresConnection(nameId, address, databaseName, credentialName string, provider Provider) *PostgresConnection {
	return &PostgresConnection{
		NameId:         nameId,
		Address:        address,
		DatabaseName:   databaseName,
		CredentialName: credentialName,
		Provider:       provider,
	}
}

// Describe names the connection for error messages: name, address and database, never the credential.
func (c *PostgresConnection) Describe() string {
	return fmt.Sprintf("PostgreSQL %s at %s/%s", c.NameId, c.Address, c.DatabaseName)
}

var pgOptionToken = regexp.MustCompile(`^([a-z_]+)=[^\s'"\\]+$`)

var pgRefusedOptions = map[string]bool{"user": true, "password": true, "passfile": true, "host": true, "hostaddr": true,
	"port": true, "dbname": true, "service": true, "servicefile": true}

// pgQuote quotes a libpq keyword value, so a password holding a space or a quote cannot add keywords.
func pgQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
}

// connectionString builds the libpq keyword/value string for cred.
func (c *PostgresConnection) connectionString(cred Credential) (string, error) {
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("address %q must be host:port", c.Address)
	}
	if c.DatabaseName == "" {
		return "", errors.New("DatabaseName is empty")
	}
	var opts []string
	for _, tok := range strings.Fields(c.ConnectionOptions) {
		m := pgOptionToken.FindStringSubmatch(tok)
		if m == nil {
			return "", fmt.Errorf("ConnectionOptions entry %q must be key=value with no quote, backslash or space", truncateText(tok, 80))
		}
		if pgRefusedOptions[m[1]] {
			return "", fmt.Errorf("ConnectionOptions may not set %q: the address comes from Address and DatabaseName, the credential from the provider", m[1])
		}
		opts = append(opts, tok)
	}
	timeout := orDefault(c.ConnectTimeout, 5*time.Second)
	secs := int((timeout + time.Second - 1) / time.Second)
	s := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s connect_timeout=%d",
		pgQuote(host), pgQuote(port), pgQuote(c.DatabaseName), pgQuote(cred.Username), pgQuote(cred.Password), secs)
	if len(opts) > 0 {
		s += " " + strings.Join(opts, " ")
	}
	return s, nil
}

// renewThreshold is how long before ExpiresAt the provider is asked again. Called with c.mu held.
func (c *PostgresConnection) renewThreshold() time.Duration {
	t := orDefault(c.RenewBefore, time.Minute)
	if life := c.cred.ExpiresAt.Sub(c.credAt); life > 0 && t > life/2 {
		t = life / 2
	}
	return t
}

// retryWait returns how long the next connection attempt must still wait. Called with c.mu held.
func (c *PostgresConnection) retryWait() time.Duration {
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

// fail records a failed attempt for the back-off; one that failed because the caller's context ended is not
// counted. Called with c.mu held.
func (c *PostgresConnection) fail(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	c.failures++
	c.lastFail = time.Now()
	c.lastErr = err
	return err
}

func sameIdentity(a, b Credential) bool {
	return a.Version == b.Version && a.Username == b.Username && a.Password == b.Password
}

// take hands out p for one transaction. Called with c.mu held.
func (p *pgPool) take() *pgPool {
	p.inflight++
	return p
}

// acquire returns the live pool for one transaction, connecting or reconnecting when needed. The caller must
// release it.
func (c *PostgresConnection) acquire(ctx context.Context, mode connectMode, used uint64) (*pgPool, error) {
	c.mu.Lock()
	var toClose []*pgPool
	defer func() {
		c.mu.Unlock()
		for _, p := range toClose {
			disconnect(p)
		}
	}()
	if c.closed {
		return nil, fmt.Errorf("%s is closed", c.Describe())
	}
	if mode == modeRefused && c.current != nil && c.gen != used {
		// Another call reconnected after this one took its pool: use the new one.
		return c.current.take(), nil
	}
	expired := !c.cred.ExpiresAt.IsZero() && !time.Now().Before(c.cred.ExpiresAt)
	expiring := !c.cred.ExpiresAt.IsZero() && time.Until(c.cred.ExpiresAt) < c.renewThreshold()
	if c.current != nil && mode == modeUse && !expiring {
		return c.current.take(), nil
	}
	if wait := c.retryWait(); wait > 0 {
		if c.current != nil && mode == modeUse && !expired {
			// Near its end but still valid: keep using it until the next attempt is due.
			return c.current.take(), nil
		}
		return nil, fmt.Errorf("%s: no new connection attempt for another %s after %d failed in a row; the last one: %w",
			c.Describe(), wait.Round(time.Millisecond), c.failures, c.lastErr)
	}
	if inv, ok := c.Provider.(Invalidator); ok && mode == modeRefused && c.cred.Version != "" {
		inv.Invalidate(c.CredentialName, c.cred.Version)
	}
	cred, err := c.Provider.Credential(ctx, c.CredentialName)
	if err != nil {
		err = c.fail(ctx, fmt.Errorf("%s: reading credential %q from the credential provider: %w", c.Describe(), c.CredentialName, err))
		if c.current != nil && mode == modeUse && !expired {
			return c.current.take(), nil // the provider is down, but the credential in use still works
		}
		return nil, err
	}
	if c.current != nil && sameIdentity(cred, c.cred) {
		if mode == modeRefused {
			return nil, c.fail(ctx, fmt.Errorf("%s refused credential version %q of user %q, and the credential provider still returns that credential: the rotation has not reached the provider yet, or the server's user was changed outside it",
				c.Describe(), cred.Version, cred.Username))
		}
		// The same credential, renewed (or not yet): only its expiry moves.
		if !cred.ExpiresAt.Equal(c.cred.ExpiresAt) {
			c.cred.ExpiresAt, c.credAt = cred.ExpiresAt, time.Now()
		}
		return c.current.take(), nil
	}
	if !cred.ExpiresAt.IsZero() && !time.Now().Before(cred.ExpiresAt) {
		return nil, c.fail(ctx, fmt.Errorf("%s: the credential provider returned credential version %q of user %q that ended at %s",
			c.Describe(), cred.Version, cred.Username, cred.ExpiresAt.Format(time.RFC3339)))
	}
	c.attempts++
	db, err := c.connect(cred)
	if err != nil {
		return nil, c.fail(ctx, fmt.Errorf("%s: connecting as %q (credential version %q): %w", c.Describe(), cred.Username, cred.Version, err))
	}
	old := c.current
	c.gen++
	c.current = &pgPool{db: db, gen: c.gen}
	c.cred, c.credAt = cred, time.Now()
	c.failures, c.lastErr = 0, nil
	if old != nil {
		old.retired = true
		if old.inflight == 0 {
			toClose = append(toClose, old)
		} else {
			c.retired = append(c.retired, old)
		}
	}
	return c.current.take(), nil
}

// connect opens a dxlib pool with cred. A failed attempt closes what dxlib opened: its Connect keeps the pool when
// the first ping fails, and its Disconnect does nothing for a database it never marked connected.
func (c *PostgresConnection) connect(cred Credential) (*databases.DXDatabase, error) {
	dsn, err := c.connectionString(cred)
	if err != nil {
		return nil, err
	}
	db := &databases.DXDatabase{
		NameId:                       c.NameId,
		IsConfigured:                 true, // never read from configuration: the credential comes from the provider
		DatabaseType:                 base.DXDatabaseTypePostgreSQL,
		Address:                      c.Address,
		DatabaseName:                 c.DatabaseName,
		UserName:                     cred.Username,
		ConnectionString:             dsn,
		NonSensitiveConnectionString: fmt.Sprintf("postgres://%s/%s", c.Address, c.DatabaseName),
		MustConnected:                false, // true would end the process on a failed connect
		PoolMaxOpenConns:             c.PoolMaxOpenConns,
		PoolMaxIdleConns:             c.PoolMaxIdleConns,
		PoolConnMaxLifetimeMinutes:   c.PoolConnMaxLifetimeMinutes,
		PoolConnMaxIdleTimeMinutes:   c.PoolConnMaxIdleTimeMinutes,
	}
	if err := db.Connect(); err != nil {
		if db.Connection != nil {
			_ = db.Connection.Close()
			db.Connection = nil
		}
		if db.PgxPool != nil {
			db.PgxPool.Close()
			db.PgxPool = nil
		}
		return nil, err
	}
	return db, nil
}

func disconnect(p *pgPool) {
	_ = p.db.Disconnect()
}

// release ends one transaction's use of p, closing p when it was replaced and this was its last transaction.
func (c *PostgresConnection) release(p *pgPool) {
	c.mu.Lock()
	p.inflight--
	last := p.retired && p.inflight == 0
	if last {
		for i, r := range c.retired {
			if r == p {
				c.retired = append(c.retired[:i], c.retired[i+1:]...)
				break
			}
		}
	}
	c.mu.Unlock()
	if last {
		disconnect(p)
	}
}

// run runs one transaction on p and releases p, even when the callback panics.
func (c *PostgresConnection) run(ctx context.Context, p *pgPool, l *log.DXLog, iso sql.IsolationLevel, cb databases.DXDatabaseTxCallback) error {
	defer c.release(p)
	return p.db.Tx(ctx, l, iso, cb)
}

// Tx runs callback in a transaction, committing when it returns nil, as databases.DXDatabase.Tx does. A failure
// before the callback ran is retried as the type's doc says; the callback itself runs at most once.
func (c *PostgresConnection) Tx(ctx context.Context, l *log.DXLog, isolationLevel sql.IsolationLevel, callback databases.DXDatabaseTxCallback) error {
	if l == nil {
		l = &log.Log
	}
	called := false
	cb := func(dtx *databases.DXDatabaseTx) error {
		called = true
		return callback(dtx)
	}
	p, err := c.acquire(ctx, modeUse, 0)
	if err != nil {
		return fmt.Errorf("beginning a transaction: %w", err)
	}
	err = c.run(ctx, p, l, isolationLevel, cb)
	// Before the callback ran nothing was done, so a refused credential or a connection the server had ended
	// (a revoked lease ends its user's sessions) is tried again: at most twice, on a new pool for a refusal and on
	// the same pool, which drops the broken connection, otherwise.
	for retry := 0; retry < 2 && err != nil && !called; retry++ {
		var next *pgPool
		var rerr error
		switch {
		case IsPostgresAuthError(err):
			next, rerr = c.acquire(ctx, modeRefused, p.gen)
		case isConnectionLost(err):
			next, rerr = c.acquire(ctx, modeUse, 0)
		default:
			retry = 2
			continue
		}
		if rerr != nil {
			return fmt.Errorf("beginning a transaction: %s: %v; trying again failed: %w", c.Describe(), err, rerr)
		}
		p = next
		err = c.run(ctx, p, l, isolationLevel, cb)
	}
	if err != nil && !called {
		return fmt.Errorf("beginning a transaction on %s: %w", c.Describe(), err)
	}
	return err
}

// Refresh asks the provider at once, as after a rotation notice: a different credential opens a new pool, the same
// one keeps the pool (moving its expiry). Within the back-off after a failed attempt it returns that failure.
func (c *PostgresConnection) Refresh(ctx context.Context) error {
	p, err := c.acquire(ctx, modeRefresh, 0)
	if err != nil {
		return err
	}
	c.release(p)
	return nil
}

// CredentialVersion returns the version of the credential the live pool was opened with ("" before the first
// connection).
func (c *PostgresConnection) CredentialVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cred.Version
}

// Close closes the live pool once its running transactions have finished; later transactions are refused.
func (c *PostgresConnection) Close() error {
	c.mu.Lock()
	c.closed = true
	p := c.current
	c.current = nil
	closeNow := false
	if p != nil {
		p.retired = true
		if p.inflight == 0 {
			closeNow = true
		} else {
			c.retired = append(c.retired, p)
		}
	}
	c.mu.Unlock()
	if closeNow {
		return p.db.Disconnect()
	}
	return nil
}

// isConnectionLost reports whether the connection a transaction began on had been ended by the server or the
// network: SQLSTATE 57P01 to 57P03 (terminated by an administrator, a crash or shutdown), or a closed or reset
// connection.
func isConnectionLost(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	s := err.Error()
	for _, m := range []string{"SQLSTATE 57P01", "SQLSTATE 57P02", "SQLSTATE 57P03", "conn closed", "unexpected EOF", "broken pipe", "connection reset by peer"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// IsPostgresAuthError reports whether PostgreSQL refused the login: SQLSTATE 28P01 (a wrong or expired password)
// or 28000 (no such role, or no pg_hba entry for it).
func IsPostgresAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 28P01") || strings.Contains(s, "SQLSTATE 28000")
}
