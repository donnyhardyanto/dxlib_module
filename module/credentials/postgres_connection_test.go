package credentials

import (
	"context"
	"database/sql"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/databases"
)

// A password holding a space, a quote or a backslash stays one value and cannot add libpq keywords.
func TestPostgresConnectionStringQuotesTheCredential(t *testing.T) {
	c := NewPostgresConnection("x", "db.test:5432", "app", "pg", nil)
	c.ConnectionOptions = "sslmode=verify-full sslrootcert=/etc/ssl/ca.pem"
	s, err := c.connectionString(Credential{Username: "v-app", Password: `a b' sslmode=disable \x`})
	if err != nil {
		t.Fatal(err)
	}
	want := `host='db.test' port='5432' dbname='app' user='v-app' password='a b\' sslmode=disable \\x' connect_timeout=5 sslmode=verify-full sslrootcert=/etc/ssl/ca.pem`
	if s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
}

func TestPostgresConnectionRefusesOptionsThatSetTheCredentialOrAddress(t *testing.T) {
	for opts, want := range map[string]string{
		"password=x":                `may not set "password"`,
		"sslmode=require user=root": `may not set "user"`,
		"host=evil.test":            `may not set "host"`,
		"dbname=other":              `may not set "dbname"`,
		"passfile=/tmp/p":           `may not set "passfile"`,
		"application_name='a b'":    "no quote",
	} {
		c := NewPostgresConnection("x", "db.test:5432", "app", "pg", nil)
		c.ConnectionOptions = opts
		if _, err := c.connectionString(Credential{Username: "u", Password: "p"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("options %q: %v, want an error naming %q", opts, err, want)
		}
	}
	c := NewPostgresConnection("x", "db.test", "app", "pg", nil)
	if _, err := c.connectionString(Credential{}); err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Errorf("address without a port: %v", err)
	}
}

func TestIsPostgresAuthError(t *testing.T) {
	for s, want := range map[string]bool{
		`FATAL: password authentication failed for user "v-x" (SQLSTATE 28P01)`: true,
		`FATAL: role "v-x" does not exist (SQLSTATE 28000)`:                     true,
		`ERROR: permission denied for table notes (SQLSTATE 42501)`:             false,
		`dial tcp 127.0.0.1:1: connect: connection refused`:                     false,
	} {
		if got := IsPostgresAuthError(errString(s)); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
	if IsPostgresAuthError(nil) {
		t.Error("nil is not an auth error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// With PostgreSQL unreachable, a failed attempt is not repeated within the back-off, the error keeps the network's
// own reason, and no failed attempt leaves a pool running.
func TestPostgresConnectionBacksOffAndLeavesNothingRunning(t *testing.T) {
	addr := refusedAddress(t)
	c := NewPostgresConnection("x", addr, "app", "pg", NewFixed(map[string]Credential{"pg": {Username: "u", Password: "p", Version: "1"}}))
	c.ConnectionOptions = "sslmode=disable"
	c.RetryMin, c.RetryMax = 50*time.Millisecond, 200*time.Millisecond
	ctx := context.Background()
	noop := func(*databases.DXDatabaseTx) error { t.Fatal("the callback ran without a connection"); return nil }

	before := runtime.NumGoroutine()
	err := c.Tx(ctx, nil, sql.LevelReadCommitted, noop)
	if err == nil || !strings.Contains(err.Error(), "connection refused") || !strings.Contains(err.Error(), addr) {
		t.Fatalf("first attempt: %v", err)
	}
	err = c.Tx(ctx, nil, sql.LevelReadCommitted, noop)
	if err == nil || !strings.Contains(err.Error(), "no new connection attempt") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("second call within the back-off: %v", err)
	}
	if c.attempts != 1 {
		t.Fatalf("%d connection attempts within the back-off, want 1", c.attempts)
	}
	c.RetryMin, c.RetryMax = time.Millisecond, time.Millisecond
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Millisecond)
		_ = c.Tx(ctx, nil, sql.LevelReadCommitted, noop)
	}
	if c.attempts < 10 {
		t.Fatalf("only %d attempts made", c.attempts)
	}
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutines grew from %d to %d over %d failed attempts", before, after, c.attempts)
	}
}

func TestPostgresConnectionReportsAProviderFailure(t *testing.T) {
	c := NewPostgresConnection("x", "127.0.0.1:1", "app", "missing", NewFixed(nil))
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(*databases.DXDatabaseTx) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `reading credential "missing"`) || !strings.Contains(err.Error(), "127.0.0.1:1/app") {
		t.Fatalf("%v", err)
	}
}

func TestPostgresConnectionRefusesAfterClose(t *testing.T) {
	c := NewPostgresConnection("x", "127.0.0.1:1", "app", "pg", NewFixed(nil))
	_ = c.Close()
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(*databases.DXDatabaseTx) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("%v", err)
	}
}
