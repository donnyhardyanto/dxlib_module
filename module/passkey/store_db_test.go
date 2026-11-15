package passkey

// These tests run against a real PostgreSQL, as tools/test-postgres.sh starts one:
//
//	DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:15432 DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=... go test ./module/passkey
//
// Each test makes its own schema, the table README documents, and a role holding only SELECT, INSERT, UPDATE and
// DELETE on it; the store connects as that role through dxlib. Without the variables they are skipped.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/google/uuid"
)

var _ Database = (*databases.DXDatabase)(nil)

func pgDatabase(t *testing.T, addr, user, password string) *databases.DXDatabase {
	t.Helper()
	d := &databases.DXDatabase{NameId: "passkey-test-" + user, IsConfigured: true, DatabaseType: base.DXDatabaseTypePostgreSQL,
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

// newPGStore makes a schema, the table and a writer role, and returns a DBStore connected as that role.
func newPGStore(t *testing.T) (*DBStore, string) {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_POSTGRES")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES is not set; run tools/test-postgres.sh")
	}
	admin := pgDatabase(t, addr, "postgres", os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"))
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	schema, role, pw := "pk_"+tag, "pkw_"+tag, "pw"+strings.ReplaceAll(uuid.NewString(), "-", "")
	table := schema + ".passkeys"
	ddl := []string{
		"CREATE SCHEMA " + schema,
		`CREATE TABLE ` + table + ` (
			id              text PRIMARY KEY,
			user_uuid       uuid NOT NULL,
			name            text NOT NULL,
			credential      text NOT NULL,
			backup_eligible boolean NOT NULL,
			backup_state    boolean NOT NULL,
			created_at      timestamptz NOT NULL,
			last_used_at    timestamptz
		)`,
		"CREATE INDEX ON " + table + " (user_uuid)",
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
	return s, table
}

func samplePasskey(user, name string) Passkey {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	return Passkey{ID: id, UserUUID: user, Name: name, CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		BackupEligible: true, BackupState: false, Credential: []byte(`{"id":"` + id + `","publicKey":"pQECAyYgASFYIA"}`)}
}

func TestPostgresStoreKeepsEveryFieldAsGiven(t *testing.T) {
	s, _ := newPGStore(t)
	ctx := context.Background()
	alice := uuid.NewString()
	p := samplePasskey(alice, `Robert'); DROP TABLE passkeys;-- "\ ünïcode`)
	if err := s.Add(ctx, p, 20); err != nil {
		t.Fatal(err)
	}
	got, err := s.ByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.UserUUID != alice || got.Name != p.Name || !bytes.Equal(got.Credential, p.Credential) ||
		got.BackupEligible != true || got.BackupState != false || !got.CreatedAt.Equal(p.CreatedAt) || !got.LastUsedAt.IsZero() {
		t.Fatalf("stored %+v\nread   %+v", p, *got)
	}
	used := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.RecordUse(ctx, alice, p.ID, []byte(`{"signCount":4}`), true, used); err != nil {
		t.Fatal(err)
	}
	list, err := s.ByUser(ctx, alice)
	if err != nil || len(list) != 1 {
		t.Fatalf("ByUser: %v, %d", err, len(list))
	}
	if string(list[0].Credential) != `{"signCount":4}` || !list[0].LastUsedAt.Equal(used) || !list[0].BackupState || !list[0].BackupEligible {
		t.Fatalf("after RecordUse: %+v", list[0])
	}
}

func TestPostgresStoreRefusesDuplicatesAndTooMany(t *testing.T) {
	s, _ := newPGStore(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	p := samplePasskey(alice, "Laptop")
	if err := s.Add(ctx, p, 2); err != nil {
		t.Fatal(err)
	}
	q := p
	q.UserUUID = bob
	if err := s.Add(ctx, q, 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("the same credential for bob: want ErrDuplicate, got %v", err)
	}
	if err := s.Add(ctx, samplePasskey(alice, "Phone"), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, samplePasskey(alice, "Tablet"), 2); !errors.Is(err, ErrTooMany) {
		t.Fatalf("a third passkey with a limit of 2: want ErrTooMany, got %v", err)
	}
}

// Two registrations of the same credential at once: the second one's check runs before the first commits, so it is
// the unique index that refuses its insert. It must come back as ErrDuplicate with the database's reason, from a
// transaction that was rolled back, not as a failed commit.
func TestPostgresConcurrentDuplicateInsertIsErrDuplicate(t *testing.T) {
	s, table := newPGStore(t)
	pg := s.db.(*databases.DXDatabase)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	p := samplePasskey(alice, "Laptop")
	inserted, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- pg.Tx(ctx, s.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
			if _, _, err := dtx.Insert(ctx, table, utils.JSON{colID: p.ID, colUserUUID: alice, colName: p.Name, colCredential: string(p.Credential),
				colBackupEligible: true, colBackupState: false, colCreatedAt: p.CreatedAt}, nil); err != nil {
				close(inserted)
				return err
			}
			close(inserted)
			<-release
			return nil
		})
	}()
	<-inserted
	q := p
	q.UserUUID = bob
	secondDone := make(chan error, 1)
	go func() { secondDone <- s.Add(ctx, q, 20) }()
	// Wait until the second insert waits on the first transaction's row lock, so the check has already passed.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pg.Connection.QueryRowContext(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE usename = current_user AND wait_event_type = 'Lock'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("the second Add never waited on the first insert (it returned %v)", <-secondDone)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first registration: %v", err)
	}
	err := <-secondDone
	if !errors.Is(err, ErrDuplicate) || !strings.Contains(err.Error(), "23505") {
		t.Fatalf("want ErrDuplicate with the unique violation 23505 in its text, got %v", err)
	}
	if strings.Contains(err.Error(), "COMMIT") {
		t.Fatalf("the aborted transaction was committed: %v", err)
	}
	if got, err := s.ByID(ctx, p.ID); err != nil || got.UserUUID != alice {
		t.Fatalf("the passkey after both: %+v, %v; want alice's", got, err)
	}
}

func TestPostgresStoreKeepsUsersApart(t *testing.T) {
	s, table := newPGStore(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	p := samplePasskey(alice, "Laptop")
	if err := s.Add(ctx, p, 20); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, bob, p.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob removing alice's passkey: %v", err)
	}
	if err := s.RecordUse(ctx, bob, p.ID, []byte(`{}`), false, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob recording a use of alice's passkey: %v", err)
	}
	if l, _ := s.ByUser(ctx, bob); len(l) != 0 {
		t.Fatalf("bob lists %d passkeys", len(l))
	}
	for _, payload := range []string{"' OR '1'='1", p.ID + "' --", "x'; DELETE FROM " + table + "; --", `\x00`} {
		if _, err := s.ByID(ctx, payload); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ByID(%q): %v", payload, err)
		}
		if err := s.Remove(ctx, alice, payload, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Remove(%q): %v", payload, err)
		}
	}
	if l, _ := s.ByUser(ctx, alice); len(l) != 1 {
		t.Fatalf("alice has %d passkeys after the payloads, want 1", len(l))
	}
}

func TestPostgresConcurrentRemovalsLeaveTheLastWayIn(t *testing.T) {
	s, _ := newPGStore(t)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		alice := uuid.NewString()
		a, b := samplePasskey(alice, "A"), samplePasskey(alice, "B")
		for _, p := range []Passkey{a, b} {
			if err := s.Add(ctx, p, 20); err != nil {
				t.Fatal(err)
			}
		}
		allow := func(remaining int) error {
			time.Sleep(50 * time.Millisecond) // hold the locks while the other removal waits
			if remaining == 0 {
				return ErrLastWayIn
			}
			return nil
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, id := range []string{a.ID, b.ID} {
			wg.Add(1)
			go func(i int, id string) {
				defer wg.Done()
				<-start
				errs[i] = s.Remove(ctx, alice, id, allow)
			}(i, id)
		}
		close(start)
		wg.Wait()
		ok, last := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrLastWayIn):
				last++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		l, err := s.ByUser(ctx, alice)
		if err != nil || ok != 1 || last != 1 || len(l) != 1 {
			t.Fatalf("round %d: %d removed, %d refused, %d left (%v); want 1, 1, 1", round, ok, last, len(l), err)
		}
	}
}

func TestPostgresCeremoniesOnTheDBStore(t *testing.T) {
	s, _ := newPGStore(t)
	r := newRig(t, testConfig())
	r.rp.store = s
	ctx := context.Background()
	alice := uuid.NewString()
	k1 := r.register(t, alice, "Laptop")
	k2 := r.register(t, alice, "Phone")
	for _, k := range []*softKey{k1, k2} {
		k.counter = 3
		if err := r.login(t, k, flagUP|flagUV, k.userHandle); err != nil {
			t.Fatalf("login with %s: %v", k.id(), err)
		}
	}
	if err := r.rp.Remove(ctx, alice, k1.id(), Client{}); err != nil {
		t.Fatal(err)
	}
	if err := r.rp.Remove(ctx, alice, k2.id(), Client{}); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("want ErrLastWayIn, got %v", err)
	}
	k2.counter = 1
	mustRefuse(t, r.login(t, k2, flagUP|flagUV, k2.userHandle), "may have been copied")
}

func TestNewDBStoreChecksTheTableName(t *testing.T) {
	for _, bad := range []string{"", "Passkeys", "a.b.c", "x; DROP TABLE y", `"quoted"`, "1table", strings.Repeat("a", 64)} {
		if _, err := NewDBStore(&databases.DXDatabase{}, bad); err == nil {
			t.Errorf("NewDBStore accepted %q", bad)
		}
	}
	for _, good := range []string{"passkeys", "auth.passkeys", "_p.p_1"} {
		if _, err := NewDBStore(&databases.DXDatabase{}, good); err != nil {
			t.Errorf("NewDBStore refused %q: %v", good, err)
		}
	}
}
