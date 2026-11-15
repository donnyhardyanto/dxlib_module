package user_photo

// These tests run against a real PostgreSQL, as tools/test-postgres.sh starts one:
//
//	DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:15432 DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=... go test ./module/user_photo
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
	d := &databases.DXDatabase{NameId: "user-photo-test-" + user, IsConfigured: true, DatabaseType: base.DXDatabaseTypePostgreSQL,
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

func newPGStore(t *testing.T) (*DBStore, *databases.DXDatabase, string) {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_POSTGRES")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES is not set; run tools/test-postgres.sh")
	}
	admin := pgDatabase(t, addr, "postgres", os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"))
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	schema, role, pw := "up_"+tag, "upw_"+tag, "pw"+strings.ReplaceAll(uuid.NewString(), "-", "")
	table := schema + ".user_photos"
	ddl := []string{
		"CREATE SCHEMA " + schema,
		`CREATE TABLE ` + table + ` (
			user_uuid  uuid PRIMARY KEY,
			object_key text NOT NULL,
			version    bigint NOT NULL,
			changed_at timestamptz NOT NULL
		)`,
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
	return s, admin, table
}

func TestPostgresStoreReplaceRemoveAndDelete(t *testing.T) {
	s, _, _ := newPGStore(t)
	alice, bob := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.Get(bg, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before any photo: %v", err)
	}
	if _, _, err := s.Replace(bg, alice, "", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing a photo that does not exist: %v", err)
	}
	before, after, err := s.Replace(bg, alice, "user-photo/aaa.webp", at)
	if err != nil || before != nil || after.Version != 1 || after.ObjectKey != "user-photo/aaa.webp" {
		t.Fatalf("first: %+v %+v %v", before, after, err)
	}
	before, after, err = s.Replace(bg, alice, "user-photo/bbb.webp", at.Add(time.Second))
	if err != nil || before == nil || before.ObjectKey != "user-photo/aaa.webp" || before.Version != 1 || after.Version != 2 {
		t.Fatalf("second: %+v %+v %v", before, after, err)
	}
	got, err := s.Get(bg, alice)
	if err != nil || got.UserUUID != alice || got.ObjectKey != "user-photo/bbb.webp" || got.Version != 2 || !got.ChangedAt.Equal(at.Add(time.Second)) {
		t.Fatalf("get: %+v %v", got, err)
	}
	before, after, err = s.Replace(bg, alice, "", at)
	if err != nil || before.ObjectKey != "user-photo/bbb.webp" || after.ObjectKey != "" || after.Version != 3 {
		t.Fatalf("removal: %+v %+v %v", before, after, err)
	}
	if _, _, err := s.Replace(bg, alice, "", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	// Bob's row is his own.
	if _, _, err := s.Replace(bg, bob, "user-photo/ccc.webp", at); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Delete(bg, alice)
	if err != nil || rec.Version != 3 {
		t.Fatalf("delete: %+v %v", rec, err)
	}
	if _, err := s.Delete(bg, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if got, err := s.Get(bg, bob); err != nil || got.ObjectKey != "user-photo/ccc.webp" {
		t.Fatalf("bob after alice's delete: %+v %v", got, err)
	}
}

func TestPostgresStoreTakesInjectionPayloadsAsValues(t *testing.T) {
	s, admin, table := newPGStore(t)
	alice := uuid.NewString()
	payload := `user-photo/x'); DELETE FROM ` + table + `;-- "\ ünïcode`
	if _, _, err := s.Replace(bg, alice, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(bg, alice); got.ObjectKey != payload {
		t.Fatalf("object key came back as %q", got.ObjectKey)
	}
	for _, u := range []string{"' OR '1'='1", alice + "' OR '1'='1", "x'; DROP TABLE " + table + ";--"} {
		if _, err := s.Get(bg, u); err == nil {
			t.Fatalf("Get(%q) found a row", u)
		}
		if _, err := s.Delete(bg, u); err == nil {
			t.Fatalf("Delete(%q) succeeded", u)
		}
	}
	var n int
	if err := admin.Connection.QueryRowContext(bg, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows after the payloads: %d %v", n, err)
	}
}

func TestPostgresStoreFirstPhotosRacingGiveOneVersionEach(t *testing.T) {
	s, _, _ := newPGStore(t)
	alice := uuid.NewString()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key, _ := newObjectKey("user-photo/")
			_, _, errs[i] = s.Replace(bg, alice, key, time.Now())
			if errors.Is(errs[i], ErrConflict) {
				_, _, errs[i] = s.Replace(bg, alice, key, time.Now())
			}
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("racing replace: %v", err)
		}
	}
	got, err := s.Get(bg, alice)
	if err != nil || got.Version != int64(ok) {
		t.Fatalf("version %d after %d successful replaces (%v)", got.Version, ok, err)
	}
}

func TestNewDBStoreRefusesBadTableNames(t *testing.T) {
	for _, name := range []string{"", "Users", "a.b.c", "x; DROP TABLE y", "photos--", `"quoted"`} {
		if _, err := NewDBStore(&databases.DXDatabase{}, name); err == nil {
			t.Fatalf("table name %q accepted", name)
		}
	}
}
