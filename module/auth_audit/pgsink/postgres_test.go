package pgsink

// These tests run against a real PostgreSQL, as tools/test-postgres.sh starts one:
//
//	DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:15432 DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=... go test ./module/auth_audit/pgsink
//
// Each test makes its own schema and the table the package documents, owned by the administrator, and a writer
// role holding INSERT on it and nothing else. The sink connects as the writer through credentials.PostgresConnection
// with the credential from a credentials.Fixed provider. Without the variables they are skipped.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/credentials"
)

var bg = context.Background()

type pgEnv struct {
	addr          string
	admin         *databases.DXDatabase
	table         string
	writer, wpass string
}

func pgDatabase(t *testing.T, addr, user, password string) *databases.DXDatabase {
	t.Helper()
	d := &databases.DXDatabase{NameId: "pgsink-test-" + user, IsConfigured: true, DatabaseType: base.DXDatabaseTypePostgreSQL,
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

// newPG makes the schema, the documented table and grants, and the writer role.
func newPG(t *testing.T) *pgEnv {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_POSTGRES")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES is not set; run tools/test-postgres.sh")
	}
	admin := pgDatabase(t, addr, "postgres", os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"))
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	schema, role, pw := "aud_"+tag, "audw_"+tag, "aw"+strings.ReplaceAll(uuid.NewString(), "-", "")
	table := schema + ".auth_events"
	ddl := []string{
		"CREATE SCHEMA " + schema,
		`CREATE TABLE ` + table + ` (
			id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			occurred_at timestamptz NOT NULL,
			recorded_at timestamptz NOT NULL DEFAULT now(),
			kind        text NOT NULL,
			user_uuid   text NOT NULL,
			actor_uuid  text NOT NULL,
			session_id  text NOT NULL,
			outcome     text NOT NULL,
			reason      text NOT NULL,
			detail      jsonb NOT NULL
		)`,
		"REVOKE ALL ON " + table + " FROM PUBLIC",
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + pw + "'",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT INSERT ON " + table + " TO " + role,
	}
	for _, s := range ddl {
		if _, err := admin.Connection.ExecContext(bg, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Connection.ExecContext(bg, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = admin.Connection.ExecContext(bg, "DROP ROLE "+role)
	})
	return &pgEnv{addr: addr, admin: admin, table: table, writer: role, wpass: pw}
}

// sink connects as the writer through a PostgresConnection whose credential comes from a provider.
func (p *pgEnv) sink(t *testing.T, table string) *DBSink {
	t.Helper()
	conn := credentials.NewPostgresConnection("pgsink-test", p.addr, "postgres", "audit-writer",
		credentials.NewFixed(map[string]credentials.Credential{"audit-writer": {Username: p.writer, Password: p.wpass, Version: "1"}}))
	conn.ConnectionOptions = "sslmode=disable"
	t.Cleanup(func() { _ = conn.Close() })
	s, err := NewDBSink(conn, table)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type row struct {
	occurredAt time.Time
	kind       string
	user       string
	actor      string
	session    string
	outcome    string
	reason     string
	detail     map[string]string
}

// rows reads the table back as the administrator; the writer may not read it.
func (p *pgEnv) rows(t *testing.T) []row {
	t.Helper()
	rs, err := p.admin.Connection.QueryContext(bg, "SELECT occurred_at, kind, user_uuid, actor_uuid, session_id, outcome, reason, detail::text FROM "+p.table+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		var dj string
		if err := rs.Scan(&r.occurredAt, &r.kind, &r.user, &r.actor, &r.session, &r.outcome, &r.reason, &dj); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(dj), &r.detail); err != nil {
			t.Fatalf("detail %s: %v", dj, err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// injectionPayloads are put into every field of an event, Detail's keys and values included.
var injectionPayloads = []string{
	`'); DROP TABLE auth_events; --`,
	`' OR '1'='1`,
	`"; DELETE FROM auth_events; --`,
	`\'; TRUNCATE auth_events; --`,
	`$1`, `$$ ; DROP SCHEMA public CASCADE; $$`, `:kind`, `:detail`, `?`, `@p1`,
	`__expr`, `NOW()`, `(SELECT current_user)`, `DEFAULT`, `NULL`,
	`{"a": 1}`, `"}, "x": "`, `\u0000`, `E'\\x00'`,
	"line\nbreak\r\ttab", `/* comment */`, `-- trailing`, `\\\`,
	"ünïcode ✓ 日本 🙂", strings.Repeat("x", 10000),
}

func eventOf(p string, at time.Time) auth_audit.Event {
	return auth_audit.Event{Time: at, Kind: auth_audit.Kind(p), UserUUID: p, ActorUUID: p, SessionID: p,
		Outcome: auth_audit.Outcome(p), Reason: p, Detail: map[string]string{p: p, "__expr": p, "login": p}}
}

func TestPostgresSinkStoresInjectionPayloadsInEveryFieldAsGiven(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table)
	at := time.Date(2026, 11, 16, 8, 30, 15, 123456000, time.FixedZone("x", 7*3600))
	for _, pl := range injectionPayloads {
		if err := s.Write(bg, eventOf(pl, at)); err != nil {
			t.Fatalf("payload %q: %v", pl, err)
		}
	}
	got := p.rows(t)
	if len(got) != len(injectionPayloads) {
		t.Fatalf("%d rows, want %d", len(got), len(injectionPayloads))
	}
	for i, pl := range injectionPayloads {
		r := got[i]
		for name, v := range map[string]string{"kind": r.kind, "user_uuid": r.user, "actor_uuid": r.actor,
			"session_id": r.session, "outcome": r.outcome, "reason": r.reason} {
			if v != pl {
				t.Errorf("payload %q: column %s holds %q", pl, name, v)
			}
		}
		want := map[string]string{pl: pl, "__expr": pl, "login": pl}
		if fmt.Sprint(r.detail) != fmt.Sprint(want) {
			t.Errorf("payload %q: detail %v, want %v", pl, r.detail, want)
		}
		if !r.occurredAt.Equal(at) {
			t.Errorf("payload %q: occurred_at %s, want %s", pl, r.occurredAt, at)
		}
	}
	// Nothing the payloads named ran: the schema, the table and its grants are as they were.
	var n int
	if err := p.admin.Connection.QueryRowContext(bg, "SELECT count(*) FROM "+p.table).Scan(&n); err != nil || n != len(injectionPayloads) {
		t.Fatalf("after the payloads: count %d, %v", n, err)
	}
	var privs string
	if err := p.admin.Connection.QueryRowContext(bg,
		"SELECT string_agg(privilege_type, ',' ORDER BY privilege_type) FROM information_schema.role_table_grants WHERE grantee = $1",
		p.writer).Scan(&privs); err != nil || privs != "INSERT" {
		t.Fatalf("the writer holds %q (%v), want INSERT alone", privs, err)
	}
}

// A NUL character or bytes that are not UTF-8, which PostgreSQL refuses, cannot keep an attempt out of the audit
// trail: the event is stored with U+FFFD in their place.
func TestPostgresSinkStoresNULAndInvalidUTF8(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table)
	for _, pl := range []string{"admin\x00", "\xff\xfeadmin", "a\x00b\xc3\x28c"} {
		if err := s.Write(bg, eventOf(pl, time.Now())); err != nil {
			t.Fatalf("payload %q: %v", pl, err)
		}
	}
	got := p.rows(t)
	want := []string{"admin�", "�admin", "a�b�(c"}
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].user != w || got[i].reason != w || got[i].kind != w || got[i].detail[w] != w || got[i].detail["login"] != w {
			t.Errorf("row %d: %+v, want every field %q", i, got[i], w)
		}
	}
}

// The writer appends and does nothing else: reading, changing, deleting and emptying the table are refused by
// PostgreSQL (SQLSTATE 42501, insufficient privilege).
func TestPostgresWriterCanOnlyInsert(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table)
	if err := s.Write(bg, auth_audit.Event{Kind: auth_audit.LoginSucceeded, UserUUID: uuid.NewString(), Outcome: auth_audit.OutcomeOK}); err != nil {
		t.Fatal(err)
	}
	w := pgDatabase(t, p.addr, p.writer, p.wpass)
	for _, stmt := range []string{
		"SELECT * FROM " + p.table,
		"UPDATE " + p.table + " SET reason = 'changed'",
		"DELETE FROM " + p.table,
		"TRUNCATE " + p.table,
		"ALTER TABLE " + p.table + " DROP COLUMN reason",
		"DROP TABLE " + p.table,
	} {
		_, err := w.Connection.ExecContext(bg, stmt)
		if err == nil || !(strings.Contains(err.Error(), "42501") || strings.Contains(err.Error(), "must be owner")) {
			t.Errorf("%s: %v, want permission denied", stmt, err)
		}
	}
	if got := p.rows(t); len(got) != 1 || got[0].reason != "" || got[0].kind != string(auth_audit.LoginSucceeded) {
		t.Fatalf("after the refused statements the table holds %+v", got)
	}
}

func TestPostgresSinkFillsTimeAndEmptyDetail(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table)
	before := time.Now().Add(-time.Second)
	if err := s.Write(bg, auth_audit.Event{Kind: auth_audit.Logout, UserUUID: "u"}); err != nil {
		t.Fatal(err)
	}
	got := p.rows(t)
	if len(got) != 1 || got[0].occurredAt.Before(before) || got[0].occurredAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("a zero Time stored as %+v, want about now", got)
	}
	if got[0].detail == nil || len(got[0].detail) != 0 {
		t.Fatalf("a nil Detail stored as %v, want {}", got[0].detail)
	}
}

func TestPostgresSinkConcurrentWrites(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table)
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Write(bg, auth_audit.Event{Kind: auth_audit.LoginFailed, UserUUID: fmt.Sprint(i), Outcome: auth_audit.OutcomeFailed})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := p.rows(t); len(got) != n {
		t.Fatalf("%d rows, want %d", len(got), n)
	}
}

// A failed write returns the database's own error with the table and the event's kind.
func TestPostgresSinkReturnsTheDatabaseError(t *testing.T) {
	p := newPG(t)
	s := p.sink(t, p.table+"_missing")
	err := s.Write(bg, auth_audit.Event{Kind: auth_audit.LoginFailed, UserUUID: "u1"})
	if err == nil {
		t.Fatal("a write to a missing table succeeded")
	}
	for _, want := range []string{p.table + "_missing", "login_failed", "42P01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
