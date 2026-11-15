package password

// These tests run against a real PostgreSQL, as tools/test-postgres.sh starts one:
//
//	DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:15432 DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=... go test ./module/password
//
// Each test makes its own schema, the table README documents, and a role holding only SELECT, INSERT, UPDATE and
// DELETE on it; the store connects as that role through dxlib. Without the variables they are skipped.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/google/uuid"
)

var _ Database = (*databases.DXDatabase)(nil)

func pgDatabase(t *testing.T, addr, user, password string) *databases.DXDatabase {
	t.Helper()
	d := &databases.DXDatabase{NameId: "password-test-" + user, IsConfigured: true, DatabaseType: base.DXDatabaseTypePostgreSQL,
		Address: addr, UserName: user, UserPassword: password, DatabaseName: "postgres", ConnectionOptions: "sslmode=disable"}
	cs, err := d.GetConnectionString()
	if err != nil {
		t.Fatal(err)
	}
	d.ConnectionString = cs
	if err := d.Connect(); err != nil {
		t.Fatalf("connecting to PostgreSQL at %s as %s: %v", addr, user, err)
	}
	t.Cleanup(func() { _ = d.Disconnect() })
	return d
}

func newPGStore(t *testing.T) *DBStore {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_POSTGRES")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES is not set; run tools/test-postgres.sh")
	}
	admin := pgDatabase(t, addr, "postgres", os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"))
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	schema, role, pw := "pw_"+tag, "pww_"+tag, "pw"+strings.ReplaceAll(uuid.NewString(), "-", "")
	table := schema + ".passwords"
	ddl := []string{
		"CREATE SCHEMA " + schema,
		`CREATE TABLE ` + table + ` (
			user_uuid            uuid PRIMARY KEY,
			ciphertext           text NOT NULL,
			key_version          integer NOT NULL,
			must_change_password boolean NOT NULL,
			changed_at           timestamptz NOT NULL
		)`,
		"CREATE INDEX ON " + table + " (key_version)",
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + pw + "'",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON " + table + " TO " + role,
	}
	ctx := context.Background()
	for _, s := range ddl {
		if _, err := admin.Connection.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Connection.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_, _ = admin.Connection.ExecContext(context.Background(), "DROP ROLE "+role)
	})
	s, err := NewDBStore(pgDatabase(t, addr, role, pw), table)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPostgresStoreKeepsEveryFieldAsGiven(t *testing.T) {
	s := newPGStore(t)
	alice := uuid.NewString()
	rec := Record{UserUUID: alice, Ciphertext: `vault:v7:Robert'); DROP TABLE passwords;-- "\ ünïcode`, KeyVersion: 7, MustChange: true,
		ChangedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := s.Put(bg, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(bg, alice)
	if err != nil {
		t.Fatal(err)
	}
	if *got != rec {
		t.Fatalf("stored %+v\nread   %+v", rec, *got)
	}
	if _, err := s.Get(bg, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user: %v, want ErrNotFound", err)
	}
	rec.Ciphertext, rec.KeyVersion, rec.MustChange = "vault:v8:new", 8, false
	if err := s.Put(bg, rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(bg, alice); *got != rec {
		t.Fatalf("after replace %+v", *got)
	}
}

func TestPostgresSwapOnlyReplacesTheValueItWasGiven(t *testing.T) {
	s := newPGStore(t)
	alice, bob := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_ = s.Put(bg, Record{UserUUID: alice, Ciphertext: "vault:v1:a", KeyVersion: 1, ChangedAt: now})
	_ = s.Put(bg, Record{UserUUID: bob, Ciphertext: "vault:v1:a", KeyVersion: 1, ChangedAt: now})
	if err := s.Swap(bg, "vault:v1:stale", Record{UserUUID: alice, Ciphertext: "vault:v2:x", KeyVersion: 2, ChangedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale swap: %v, want ErrConflict", err)
	}
	if err := s.Swap(bg, "vault:v1:a", Record{UserUUID: alice, Ciphertext: "vault:v2:x", KeyVersion: 2, ChangedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(bg, alice); got.Ciphertext != "vault:v2:x" || got.KeyVersion != 2 {
		t.Fatalf("after the swap alice holds %+v, want vault:v2:x at key version 2", got)
	}
	if got, _ := s.Get(bg, bob); got.Ciphertext != "vault:v1:a" {
		t.Fatalf("swapping alice's row changed bob's: %+v", got)
	}
	if err := s.Swap(bg, "x", Record{UserUUID: uuid.NewString(), Ciphertext: "y", KeyVersion: 2, ChangedAt: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v, want ErrNotFound", err)
	}
}

func TestPostgresByKeyVersionPagesThroughOneVersion(t *testing.T) {
	s := newPGStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 5; i++ {
		v := 1
		if i == 4 {
			v = 2
		}
		_ = s.Put(bg, Record{UserUUID: uuid.NewString(), Ciphertext: "c", KeyVersion: v, ChangedAt: now})
	}
	rr, err := s.ByKeyVersion(bg, 1, 3)
	if err != nil || len(rr) != 3 {
		t.Fatalf("%d rows, %v; want 3", len(rr), err)
	}
	all, _ := s.ByKeyVersion(bg, 1, 0)
	if len(all) != 4 {
		t.Fatalf("%d rows of version 1, want 4", len(all))
	}
}

func TestPostgresRemoveAsksFirstAndKeepsTheRowWhenRefused(t *testing.T) {
	s := newPGStore(t)
	alice := uuid.NewString()
	_ = s.Put(bg, Record{UserUUID: alice, Ciphertext: "c", KeyVersion: 1, ChangedAt: time.Now()})
	if err := s.Remove(bg, alice, func() error { return ErrLastWayIn }); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Get(bg, alice); err != nil {
		t.Fatalf("the refused removal deleted the row: %v", err)
	}
	if err := s.Remove(bg, alice, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(bg, alice, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
}

func TestPostgresTwoFirstPasswordsAtOnceGiveOneConflictNotACommitError(t *testing.T) {
	s := newPGStore(t)
	for round := 0; round < 10; round++ {
		u := uuid.NewString()
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = s.Put(bg, Record{UserUUID: u, Ciphertext: "c" + string(rune('a'+i)), KeyVersion: 1, ChangedAt: time.Now()})
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Fatalf("round %d writer %d: %v, want nil or ErrConflict", round, i, err)
			}
			if err != nil && !strings.Contains(err.Error(), "23505") {
				t.Fatalf("round %d writer %d: the conflict lost its cause: %v", round, i, err)
			}
		}
		if _, err := s.Get(bg, u); err != nil {
			t.Fatalf("round %d: no row: %v", round, err)
		}
	}
}

func TestPostgresTheWholeFlowOnTheDBStore(t *testing.T) {
	s := newPGStore(t)
	r := newRig(t, s)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, true, Client{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); !errors.Is(err, ErrMustChange) {
		t.Fatalf("got %v", err)
	}
	if _, _, err := r.m.ChangeAndLogin(bg, "alice", goodPassword, otherPassword, Client{}); err != nil {
		t.Fatal(err)
	}
	r.transit.Rotate()
	if res, err := r.m.RewrapAll(bg, 10); err != nil || res.Rewrapped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	r.transit.MinDecryptionVersion = 2
	if _, _, err := r.m.Login(bg, "alice", otherPassword, Client{}); err != nil {
		t.Fatal(err)
	}
	r.accounts.passkeys[alice] = true
	if err := r.m.Remove(bg, "", alice, Client{}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTableNameIsChecked(t *testing.T) {
	for _, name := range []string{"", "Passwords", "a.b.c", "x; DROP TABLE y", `"q"`, "a-b"} {
		if _, err := NewDBStore(&databases.DXDatabase{}, name); err == nil {
			t.Errorf("table name %q accepted", name)
		}
	}
}
