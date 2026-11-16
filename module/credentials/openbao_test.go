package credentials

// The container tests run against a real OpenBao dev server, PostgreSQL and Valkey in one pod, as
// tools/test-credentials.sh starts them:
//
//	DXLIB_MODULE_TEST_OPENBAO=http://127.0.0.1:28200 DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN=...
//	DXLIB_MODULE_TEST_OPENBAO_POSTGRES=127.0.0.1:5432 (PostgreSQL as OpenBao reaches it)
//	DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:25432 DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=...
//
// Each test mounts its own engines, schema, role and policy. Without the variables they are skipped.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/databases"
	dxVault "github.com/donnyhardyanto/dxlib/vault"
	"github.com/google/uuid"
)

func TestNewOpenBaoRefusesBadSettings(t *testing.T) {
	good := map[string]OpenBaoSource{"pg": DatabaseRole("database", "app")}
	v := func(addr string) *dxVault.DXHashicorpVault { return dxVault.NewHashiCorpVault(addr, "t", "", "") }
	cases := []struct {
		name    string
		v       *dxVault.DXHashicorpVault
		sources map[string]OpenBaoSource
		opts    OpenBaoOptions
		want    string
	}{
		{"nil client", nil, good, OpenBaoOptions{}, "needs a dxlib vault client"},
		{"http", v("http://bao.test:8200"), good, OpenBaoOptions{}, "cross the network in clear"},
		{"no host", v("https://"), good, OpenBaoOptions{}, "https URL with a host"},
		{"no sources", v("https://bao.test:8200"), nil, OpenBaoOptions{}, "no sources"},
		{"dot segment", v("https://bao.test:8200"), map[string]OpenBaoSource{"x": KV("secret", "a/../b")}, OpenBaoOptions{}, "KV path"},
		{"query in mount", v("https://bao.test:8200"), map[string]OpenBaoSource{"x": KV("secret?x=1", "a")}, OpenBaoOptions{}, "mount"},
		{"role with slash", v("https://bao.test:8200"), map[string]OpenBaoSource{"x": DatabaseRole("database", "a/b")}, OpenBaoOptions{}, "database role"},
		{"no kind", v("https://bao.test:8200"), map[string]OpenBaoSource{"x": {Mount: "secret", Path: "a"}}, OpenBaoOptions{}, "no kind"},
	}
	for _, c := range cases {
		_, err := NewOpenBao(c.v, c.sources, c.opts)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

// The vault client reads VAULT_SKIP_VERIFY and VAULT_NAMESPACE from the environment: the first is refused, the
// second replaced by the namespace the service set.
func TestNewOpenBaoRefusesSkipVerifyAndSetsTheNamespace(t *testing.T) {
	t.Setenv("VAULT_SKIP_VERIFY", "true")
	_, err := NewOpenBao(dxVault.NewHashiCorpVault("https://bao.test:8200", "t", "", ""), map[string]OpenBaoSource{"x": KV("secret", "a")}, OpenBaoOptions{})
	if err == nil || !strings.Contains(err.Error(), "VAULT_SKIP_VERIFY") {
		t.Fatalf("with VAULT_SKIP_VERIFY=true: %v", err)
	}
	t.Setenv("VAULT_SKIP_VERIFY", "")
	t.Setenv("VAULT_NAMESPACE", "from-env")
	v := dxVault.NewHashiCorpVault("https://bao.test:8200", "t", "", "")
	if _, err := NewOpenBao(v, map[string]OpenBaoSource{"x": KV("secret", "a")}, OpenBaoOptions{}); err != nil {
		t.Fatal(err)
	}
	if ns := v.Client.Namespace(); ns != "" {
		t.Fatalf("namespace %q taken from the environment, want none", ns)
	}
	v2 := dxVault.NewHashiCorpVault("https://bao.test:8200", "t", "", "")
	if _, err := NewOpenBao(v2, map[string]OpenBaoSource{"x": KV("secret", "a")}, OpenBaoOptions{Namespace: "team"}); err != nil {
		t.Fatal(err)
	}
	if ns := v2.Client.Namespace(); ns != "team" {
		t.Fatalf("namespace %q, want team", ns)
	}
}

// ---- container helpers ----

type bao struct {
	t    *testing.T
	addr string
	root string
}

func newBaoEnv(t *testing.T) bao {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_OPENBAO")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_OPENBAO is not set; run tools/test-credentials.sh")
	}
	return bao{t: t, addr: addr, root: os.Getenv("DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN")}
}

// call makes one request as token and returns the answer's JSON; a status above 299 fails the test.
func (b bao) call(token, method, path string, body any) map[string]any {
	b.t.Helper()
	out, status, raw := b.try(token, method, path, body)
	if status > 299 {
		b.t.Fatalf("%s %s: HTTP %d: %s", method, path, status, raw)
	}
	return out
}

func (b bao) try(token, method, path string, body any) (map[string]any, int, string) {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.addr+"/v1/"+path, rd)
	req.Header.Set("X-Vault-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out, resp.StatusCode, string(raw)
}

func tag() string { return strings.ReplaceAll(uuid.NewString()[:8], "-", "") }

// token makes a token holding a policy with the given rules (HCL), with ttl ("" for the default) and renewable.
func (b bao) token(rules, ttl string) string {
	b.t.Helper()
	name := "p" + tag()
	b.call(b.root, "PUT", "sys/policies/acl/"+name, map[string]any{"policy": rules})
	body := map[string]any{"policies": []string{name}, "renewable": true, "no_default_policy": true}
	if ttl != "" {
		body["ttl"] = ttl
	}
	out := b.call(b.root, "POST", "auth/token/create", body)
	return out["auth"].(map[string]any)["client_token"].(string)
}

const tokenSelfRules = `
path "auth/token/lookup-self" { capabilities = ["read"] }
path "auth/token/renew-self" { capabilities = ["update"] }
path "sys/leases/renew" { capabilities = ["update"] }
path "sys/leases/revoke" { capabilities = ["update"] }
`

func vaultFor(b bao, token string) *dxVault.DXHashicorpVault {
	return dxVault.NewHashiCorpVault(b.addr, token, "", "")
}

func newProvider(t *testing.T, b bao, token string, sources map[string]OpenBaoSource, opts OpenBaoOptions) *OpenBao {
	t.Helper()
	opts.AllowHTTP = true
	p, err := NewOpenBao(vaultFor(b, token), sources, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// pgAdmin is a PostgresConnection as the server's superuser, through a fixed test credential.
func pgAdmin(t *testing.T) *PostgresConnection {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_POSTGRES")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES is not set; run tools/test-credentials.sh")
	}
	c := NewPostgresConnection("admin", addr, "postgres", "admin",
		NewFixed(map[string]Credential{"admin": {Username: "postgres", Password: os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"), Version: "1"}}))
	c.ConnectionOptions = "sslmode=disable"
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func execSQL(t *testing.T, c *PostgresConnection, stmts ...string) {
	t.Helper()
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		for _, s := range stmts {
			if _, err := dtx.ExecContext(dtx.Ctx, s); err != nil {
				return fmt.Errorf("%s: %w", s, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func queryInt(t *testing.T, c *PostgresConnection, q string, args ...any) int {
	t.Helper()
	var n int
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		return dtx.QueryRowContext(dtx.Ctx, q, args...).Scan(&n)
	})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// dbRole is one database secrets engine role on its own schema with a notes table.
type dbRole struct {
	mount, role, schema string
	admin               *PostgresConnection
}

// newDBRole mounts a database engine, configures it on the test PostgreSQL, makes a schema with a notes table and a
// role whose users may read and write it, with the given TTLs. Its revocation ends the user's sessions first, then
// drops it, as the README recommends.
func newDBRole(t *testing.T, b bao, ttl, maxTTL string) dbRole {
	t.Helper()
	admin := pgAdmin(t)
	pgFromBao := os.Getenv("DXLIB_MODULE_TEST_OPENBAO_POSTGRES")
	if pgFromBao == "" {
		t.Skip("DXLIB_MODULE_TEST_OPENBAO_POSTGRES is not set; run tools/test-credentials.sh")
	}
	tg := tag()
	r := dbRole{mount: "db" + tg, role: "r" + tg[:7], schema: "cred_" + tg, admin: admin}
	execSQL(t, admin, "CREATE SCHEMA "+r.schema, "CREATE TABLE "+r.schema+".notes (id serial PRIMARY KEY, body text NOT NULL, made_by text NOT NULL DEFAULT current_user)")
	t.Cleanup(func() {
		_ = admin.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
			_, err := dtx.ExecContext(dtx.Ctx, "DROP SCHEMA "+r.schema+" CASCADE")
			return err
		})
	})
	b.call(b.root, "POST", "sys/mounts/"+r.mount, map[string]any{"type": "database"})
	b.call(b.root, "POST", r.mount+"/config/pg", map[string]any{
		"plugin_name":    "postgresql-database-plugin",
		"connection_url": "postgresql://{{username}}:{{password}}@" + pgFromBao + "/postgres?sslmode=disable",
		"username":       "postgres",
		"password":       os.Getenv("DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD"),
		"allowed_roles":  r.role,
	})
	b.call(b.root, "POST", r.mount+"/roles/"+r.role, map[string]any{
		"db_name": "pg",
		"creation_statements": []string{
			`CREATE ROLE "{{name}}" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
			`GRANT USAGE ON SCHEMA ` + r.schema + ` TO "{{name}}";`,
			`GRANT SELECT, INSERT ON ` + r.schema + `.notes TO "{{name}}";`,
			`GRANT USAGE ON SEQUENCE ` + r.schema + `.notes_id_seq TO "{{name}}";`,
		},
		"revocation_statements": []string{
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '{{name}}';`,
			`REVOKE ALL ON ALL TABLES IN SCHEMA ` + r.schema + ` FROM "{{name}}";`,
			`REVOKE ALL ON ALL SEQUENCES IN SCHEMA ` + r.schema + ` FROM "{{name}}";`,
			`REVOKE USAGE ON SCHEMA ` + r.schema + ` FROM "{{name}}";`,
			`DROP ROLE IF EXISTS "{{name}}";`,
		},
		"default_ttl": ttl,
		"max_ttl":     maxTTL,
	})
	t.Cleanup(func() {
		b.try(b.root, "PUT", "sys/leases/revoke-prefix/"+r.mount+"/creds/"+r.role, nil)
		b.try(b.root, "DELETE", "sys/mounts/"+r.mount, nil)
	})
	return r
}

func (r dbRole) rules() string {
	return tokenSelfRules + `path "` + r.mount + `/creds/` + r.role + `" { capabilities = ["read"] }` + "\n"
}

// users counts the PostgreSQL roles the engine made for this role (the default username template carries the
// role's name, up to 8 characters).
func (r dbRole) users(t *testing.T) int {
	return queryInt(t, r.admin, "SELECT count(*) FROM pg_roles WHERE rolname LIKE $1", "v-%-"+r.role+"-%")
}

func (r dbRole) connection(t *testing.T, p Provider, name string) *PostgresConnection {
	t.Helper()
	c := NewPostgresConnection("app", os.Getenv("DXLIB_MODULE_TEST_POSTGRES"), "postgres", name, p)
	c.ConnectionOptions = "sslmode=disable"
	c.RetryMin, c.RetryMax = 50*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (r dbRole) insert(t *testing.T, c *PostgresConnection, body string) {
	t.Helper()
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		_, err := dtx.ExecContext(dtx.Ctx, "INSERT INTO "+r.schema+".notes (body) VALUES ($1)", body)
		return err
	})
	if err != nil {
		t.Fatalf("insert %q: %v", body, err)
	}
}

func currentUser(t *testing.T, c *PostgresConnection) string {
	t.Helper()
	var u string
	err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		return dtx.QueryRowContext(dtx.Ctx, "SELECT current_user").Scan(&u)
	})
	if err != nil {
		t.Fatalf("SELECT current_user: %v", err)
	}
	return u
}

// ---- KV ----

func TestOpenBaoKVReturnsTheLatestVersion(t *testing.T) {
	b := newBaoEnv(t)
	mount := "kv" + tag()
	b.call(b.root, "POST", "sys/mounts/"+mount, map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	t.Cleanup(func() { b.try(b.root, "DELETE", "sys/mounts/"+mount, nil) })
	tok := b.token(tokenSelfRules+`path "`+mount+`/data/*" { capabilities = ["read"] }`, "")
	p := newProvider(t, b, tok, map[string]OpenBaoSource{
		"valkey":  KV(mount, "valkey/sessions"),
		"objects": {Kind: SourceKV, Mount: mount, Path: "objects", UsernameKey: "access_key", PasswordKey: "secret_key"},
		"missing": KV(mount, "nothing/here"),
		"badkeys": KV(mount, "badkeys"),
	}, OpenBaoOptions{})
	ctx := context.Background()

	b.call(b.root, "POST", mount+"/data/valkey/sessions", map[string]any{"data": map[string]any{"username": "sess", "password": "one"}})
	c, err := p.Credential(ctx, "valkey")
	if err != nil || c.Username != "sess" || c.Password != "one" || c.Version != "1" || !c.ExpiresAt.IsZero() {
		t.Fatalf("first version: %v %v", c, err)
	}
	b.call(b.root, "POST", mount+"/data/valkey/sessions", map[string]any{"data": map[string]any{"username": "sess", "password": "two"}})
	c, err = p.Credential(ctx, "valkey")
	if err != nil || c.Password != "two" || c.Version != "2" {
		t.Fatalf("after a new version: %v %v", c, err)
	}

	b.call(b.root, "POST", mount+"/data/objects", map[string]any{"data": map[string]any{"access_key": "AK", "secret_key": "SK"}})
	if c, err := p.Credential(ctx, "objects"); err != nil || c.Username != "AK" || c.Password != "SK" {
		t.Fatalf("custom field names: %v %v", c, err)
	}
	if _, err := p.Credential(ctx, "missing"); err == nil || !strings.Contains(err.Error(), mount+"/data/nothing/here") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing secret: %v", err)
	}
	b.call(b.root, "POST", mount+"/data/badkeys", map[string]any{"data": map[string]any{"user": "x"}})
	if _, err := p.Credential(ctx, "badkeys"); err == nil || !strings.Contains(err.Error(), `"username"`) {
		t.Fatalf("secret without the fields: %v", err)
	}
	if _, err := p.Credential(ctx, "nope"); err == nil || !strings.Contains(err.Error(), `no OpenBao source named "nope"`) {
		t.Fatalf("unknown name: %v", err)
	}
	// A path outside the token's policy: the error carries OpenBao's own status and text.
	p2 := newProvider(t, b, tok, map[string]OpenBaoSource{"other": KV("secret", "x")}, OpenBaoOptions{})
	if _, err := p2.Credential(ctx, "other"); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("path outside the policy: %v", err)
	}
	if err := p.Check(ctx); err == nil || !strings.Contains(err.Error(), "2 of 4 sources") {
		t.Fatalf("Check with two bad sources: %v", err)
	}
}

// ---- database ----

// One lease serves every call; near its end it is renewed (same version, later end, and the user can still log in
// past the first end); at max_ttl a new lease is issued.
func TestOpenBaoDatabaseLeaseIsCachedRenewedAndReplaced(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "4s", "10s")
	p := newProvider(t, b, b.token(r.rules(), ""), map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	ctx := context.Background()

	first, err := p.Credential(ctx, "pg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Version, r.mount+"/creds/"+r.role+"/") || until(first.ExpiresAt) > 4*time.Second || until(first.ExpiresAt) < 3*time.Second {
		t.Fatalf("first credential %v: want a lease of %s ending in about 4 s", first, r.mount)
	}
	for i := 0; i < 5; i++ {
		c, err := p.Credential(ctx, "pg")
		if err != nil || c.Version != first.Version {
			t.Fatalf("call %d: %v %v, want the cached lease", i, c, err)
		}
	}
	if n := r.users(t); n != 1 {
		t.Fatalf("%d database users after six calls, want 1", n)
	}

	time.Sleep(2500 * time.Millisecond) // within half the 4 s lease of its end
	renewed, err := p.Credential(ctx, "pg")
	if err != nil || renewed.Version != first.Version || !renewed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("near the end: %v %v, want the same lease ending later than %s", renewed, err, first.ExpiresAt)
	}
	time.Sleep(time.Until(first.ExpiresAt) + 500*time.Millisecond) // past the first end
	c := r.connection(t, NewFixed(map[string]Credential{"pg": {Username: first.Username, Password: first.Password, Version: "x"}}), "pg")
	if u := currentUser(t, c); u != first.Username {
		t.Fatalf("login after the first end as %q, want %q (the renewal moves VALID UNTIL)", u, first.Username)
	}

	deadline := time.Now().Add(12 * time.Second)
	for {
		c, err := p.Credential(ctx, "pg")
		if err != nil {
			t.Fatal(err)
		}
		if c.Version != first.Version {
			if c.Username == first.Username {
				t.Fatalf("new lease with the old user %q", c.Username)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still lease %s past its max_ttl", first.Version)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := r.users(t); n != 0 {
		t.Fatalf("%d database users after Close, want 0 (every lease revoked)", n)
	}
}

func until(at time.Time) time.Duration { return time.Until(at) }

// A transaction open on the old credential when it rotates commits; the next transaction runs as the new user; the
// old pool is closed once that transaction ends; nothing written before the rotation is lost.
func TestPostgresConnectionRotationWhileATransactionIsOpen(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	p := newProvider(t, b, b.token(r.rules(), ""), map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	c := r.connection(t, p, "pg")
	ctx := context.Background()

	r.insert(t, c, "before")
	oldUser, oldVersion := currentUser(t, c), c.CredentialVersion()

	inTx, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- c.Tx(ctx, nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
			if _, err := dtx.ExecContext(dtx.Ctx, "INSERT INTO "+r.schema+".notes (body) VALUES ('during')"); err != nil {
				return err
			}
			close(inTx)
			<-release
			return nil
		})
	}()
	<-inTx

	p.Invalidate("pg", oldVersion) // the rotation: the provider issues a new lease
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if v := c.CredentialVersion(); v == oldVersion {
		t.Fatalf("still credential %s after the rotation", v)
	}
	newUser := currentUser(t, c)
	if newUser == oldUser {
		t.Fatalf("transaction after the rotation ran as the old user %q", oldUser)
	}
	r.insert(t, c, "after")
	c.mu.Lock()
	open := len(c.retired)
	c.mu.Unlock()
	if open != 1 {
		t.Fatalf("%d replaced pools kept open, want 1 (the open transaction's)", open)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the transaction open across the rotation: %v", err)
	}
	c.mu.Lock()
	open = len(c.retired)
	c.mu.Unlock()
	if open != 0 {
		t.Fatalf("%d replaced pools still open after their last transaction", open)
	}
	got := queryInt(t, c, "SELECT count(*) FROM "+r.schema+".notes WHERE (body = 'before' AND made_by = $1) OR (body = 'during' AND made_by = $1) OR (body = 'after' AND made_by = $2)", oldUser, newUser)
	if got != 3 {
		t.Fatalf("%d of the 3 rows written before, during and after the rotation found", got)
	}
}

// A lease that ran out while the connection was idle is replaced on the next transaction.
func TestPostgresConnectionReconnectsAfterTheLeaseEnded(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "2s", "2s")
	p := newProvider(t, b, b.token(r.rules(), ""), map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	c := r.connection(t, p, "pg")
	r.insert(t, c, "one")
	first := c.CredentialVersion()
	time.Sleep(3 * time.Second) // the lease ends; OpenBao drops the user and ends its sessions
	r.insert(t, c, "two")
	if c.CredentialVersion() == first || c.attempts != 2 {
		t.Fatalf("credential %q after the lease ended (first %q), %d connection attempts (want 2)", c.CredentialVersion(), first, c.attempts)
	}
	if n := queryInt(t, c, "SELECT count(*) FROM "+r.schema+".notes"); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

// A lease revoked out of band: the server refuses the cached credential as transactions begin; the connection tells
// the provider (Invalidate), takes a new lease once for all the calls refused together, and runs each callback once.
func TestPostgresConnectionReplacesARevokedLeaseOnce(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	p := newProvider(t, b, b.token(r.rules(), ""), map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	c := r.connection(t, p, "pg")
	r.insert(t, c, "before")
	first := c.CredentialVersion()

	b.call(b.root, "PUT", "sys/leases/revoke", map[string]any{"lease_id": first})
	var mu sync.Mutex
	calls := 0
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := c.Tx(context.Background(), nil, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
				mu.Lock()
				calls++
				mu.Unlock()
				_, err := dtx.ExecContext(dtx.Ctx, "INSERT INTO "+r.schema+".notes (body) VALUES ($1)", fmt.Sprint("after ", i))
				return err
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("transaction after the revocation: %v", err)
	}
	if calls != 8 || c.attempts != 2 || c.CredentialVersion() == first {
		t.Fatalf("%d callbacks (want 8), %d connection attempts (want 2), credential %q (first %q)", calls, c.attempts, c.CredentialVersion(), first)
	}
	if n := queryInt(t, c, "SELECT count(*) FROM "+r.schema+".notes"); n != 9 {
		t.Fatalf("%d rows, want 9", n)
	}
}

// ---- the provider's own token ----

// The provider renews its token as it goes, so its leases (children of the token) outlive the token's first TTL.
func TestOpenBaoRenewsItsToken(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	tok := b.token(r.rules(), "4s")
	p := newProvider(t, b, tok, map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	ctx := context.Background()
	first, err := p.Credential(ctx, "pg")
	if err != nil {
		t.Fatal(err)
	}
	if until(first.ExpiresAt) > 4*time.Second {
		t.Fatalf("ExpiresAt %s is past the token's end (about 4 s away)", first.ExpiresAt)
	}
	for i := 0; i < 8; i++ {
		time.Sleep(time.Second)
		c, err := p.Credential(ctx, "pg")
		if err != nil {
			t.Fatalf("after %d s: %v", i+1, err)
		}
		if c.Version != first.Version {
			t.Fatalf("after %d s: a new lease %s, want %s kept alive", i+1, c.Version, first.Version)
		}
	}
	if n := r.users(t); n != 1 {
		t.Fatalf("%d database users 8 s after a 4 s token was made, want its 1 lease alive", n)
	}
}

// A revoked token (and with it every lease it issued) is replaced from the TokenSource as a transaction begins:
// its sessions ended (57P01), the old user refused (28P01), the provider told (Invalidate), OpenBao refuses the token
// (403), a new token, a new lease. Without a TokenSource, the error carries OpenBao's own 403.
func TestOpenBaoTakesANewTokenWhenRefused(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	ctx := context.Background()
	asked := 0
	tokPlain, tokSource := b.token(r.rules(), ""), b.token(r.rules(), "")
	plain := newProvider(t, b, tokPlain, map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{})
	withSource := newProvider(t, b, tokSource, map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{
		TokenSource: func(context.Context) (string, error) { asked++; return b.token(r.rules(), ""), nil },
	})
	cPlain, cSource := r.connection(t, plain, "pg"), r.connection(t, withSource, "pg")
	r.insert(t, cPlain, "plain before")
	r.insert(t, cSource, "source before")
	firstSource := cSource.CredentialVersion()

	b.call(b.root, "POST", "auth/token/revoke", map[string]any{"token": tokPlain})
	b.call(b.root, "POST", "auth/token/revoke", map[string]any{"token": tokSource})
	deadline := time.Now().Add(10 * time.Second)
	for r.users(t) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d database users still there 10 s after their tokens were revoked", r.users(t))
		}
		time.Sleep(100 * time.Millisecond)
	}

	r.insert(t, cSource, "source after")
	if asked != 1 || cSource.CredentialVersion() == firstSource {
		t.Fatalf("TokenSource asked %d times (want 1), credential %q (first %q)", asked, cSource.CredentialVersion(), firstSource)
	}
	err := cPlain.Tx(ctx, nil, sql.LevelReadCommitted, func(*databases.DXDatabaseTx) error {
		t.Fatal("the callback ran with a revoked credential")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("revoked token without a TokenSource: %v, want OpenBao's 403", err)
	}
}

// A token that reaches its explicit max TTL is replaced from the TokenSource before it ends, and the lease it issued
// (which ends with it) is replaced at once, with no Invalidate: ExpiresAt never passes the issuing token's end.
func TestOpenBaoReplacesLeasesOfAReplacedToken(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	name := "p" + tag()
	b.call(b.root, "PUT", "sys/policies/acl/"+name, map[string]any{"policy": r.rules()})
	short := b.call(b.root, "POST", "auth/token/create", map[string]any{"policies": []string{name}, "no_default_policy": true,
		"renewable": true, "ttl": "6s", "explicit_max_ttl": "6s"})["auth"].(map[string]any)["client_token"].(string)
	asked := 0
	p := newProvider(t, b, short, map[string]OpenBaoSource{"pg": DatabaseRole(r.mount, r.role)}, OpenBaoOptions{
		TokenSource: func(context.Context) (string, error) { asked++; return b.token(r.rules(), ""), nil },
	})
	ctx := context.Background()
	first, err := p.Credential(ctx, "pg")
	if err != nil {
		t.Fatal(err)
	}
	if until(first.ExpiresAt) > 6*time.Second {
		t.Fatalf("first ExpiresAt %s is past the token's end (6 s)", first.ExpiresAt)
	}
	time.Sleep(3500 * time.Millisecond) // within half the token's 6 s of its end
	second, err := p.Credential(ctx, "pg")
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || second.Version == first.Version || until(second.ExpiresAt) < 50*time.Second {
		t.Fatalf("near the token's end: asked %d (want 1), credential %v (first %v), want a new lease of about 1 minute", asked, second, first)
	}
	time.Sleep(time.Until(first.ExpiresAt) + time.Second)
	if n := r.users(t); n != 1 {
		t.Fatalf("%d database users after the first token ended, want only the new lease's", n)
	}
	c := r.connection(t, p, "pg")
	if u := currentUser(t, c); u != second.Username {
		t.Fatalf("connected as %q, want the new lease's %q", u, second.Username)
	}
}

// OpenBao answers 403 for a path outside the token's policy as it does for a dead token. A source outside the policy
// leaves the TokenSource unasked and the database lease in place (lookup-self still accepts the token). Once the
// token is revoked, eight calls refused together take one new token, and the lease that ended with it is replaced.
func TestOpenBaoKeepsATokenRefusedOnlyForAPath(t *testing.T) {
	b := newBaoEnv(t)
	r := newDBRole(t, b, "1m", "10m")
	mount := "kv" + tag()
	b.call(b.root, "POST", "sys/mounts/"+mount, map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	t.Cleanup(func() { b.try(b.root, "DELETE", "sys/mounts/"+mount, nil) })
	b.call(b.root, "POST", mount+"/data/good", map[string]any{"data": map[string]any{"username": "u", "password": "p"}})
	b.call(b.root, "POST", mount+"/data/bad", map[string]any{"data": map[string]any{"username": "u", "password": "p"}})
	rules := r.rules() + `path "` + mount + `/data/good" { capabilities = ["read"] }` + "\n"
	tok := b.token(rules, "")
	var asked atomic.Int32
	p := newProvider(t, b, tok, map[string]OpenBaoSource{
		"pg": DatabaseRole(r.mount, r.role), "good": KV(mount, "good"), "bad": KV(mount, "bad"),
	}, OpenBaoOptions{TokenSource: func(context.Context) (string, error) { asked.Add(1); return b.token(rules, ""), nil }})
	ctx := context.Background()
	first, err := p.Credential(ctx, "pg")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := p.Credential(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("source outside the policy: %v, want OpenBao's 403", err)
		}
	}
	if err := p.Check(ctx); err == nil || !strings.Contains(err.Error(), "1 of 3 sources") {
		t.Fatalf("Check with one source outside the policy: %v", err)
	}
	again, err := p.Credential(ctx, "pg")
	if n := asked.Load(); err != nil || n != 0 || again.Version != first.Version || p.tokenGeneration() != 0 {
		t.Fatalf("after 403s outside the policy: TokenSource asked %d times (want 0), lease %q (want %q), token generation %d (want 0), %v",
			n, again.Version, first.Version, p.tokenGeneration(), err)
	}
	if n := r.users(t); n != 1 {
		t.Fatalf("%d database users, want the one lease", n)
	}

	b.call(b.root, "POST", "auth/token/revoke", map[string]any{"token": tok})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c, err := p.Credential(ctx, "good"); err != nil || c.Password != "p" {
				errs <- fmt.Errorf("%v %v", c, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("KV read after the token was revoked: %v", err)
	}
	if n := asked.Load(); n != 1 || p.tokenGeneration() != 1 {
		t.Fatalf("eight 403s on a revoked token: TokenSource asked %d times (want 1), token generation %d (want 1)", n, p.tokenGeneration())
	}
	next, err := p.Credential(ctx, "pg")
	if err != nil || next.Version == first.Version {
		t.Fatalf("after the token was replaced: lease %q (first %q), %v; want a new lease", next.Version, first.Version, err)
	}
}

// ---- Valkey with KV credentials ----

// The Valkey password rotated in OpenBao KV and on the server while data is stored: the connection is refused,
// takes the new KV version and reconnects, and the stored data is still there.
func TestOpenBaoKVRotationKeepsValkeyData(t *testing.T) {
	b := newBaoEnv(t)
	addr, admin, user, pw := valkeyUser(t)
	ctx := context.Background()
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "+get", "+set", "+evalsha", "+eval").Err(); err != nil {
		t.Fatal(err)
	}
	mount := "kv" + tag()
	b.call(b.root, "POST", "sys/mounts/"+mount, map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	t.Cleanup(func() { b.try(b.root, "DELETE", "sys/mounts/"+mount, nil) })
	b.call(b.root, "POST", mount+"/data/valkey", map[string]any{"data": map[string]any{"username": user, "password": pw}})
	p := newProvider(t, b, b.token(tokenSelfRules+`path "`+mount+`/data/valkey" { capabilities = ["read"] }`, ""),
		map[string]OpenBaoSource{"valkey": KV(mount, "valkey")}, OpenBaoOptions{})
	c := NewRedisConnection("sessions", addr, 0, "valkey", p)
	t.Cleanup(func() { _ = c.Close() }) // the default CloseGrace: a 10 ms grace is BUG-003's likely cause

	key := user + ":session"
	if _, err := c.Command(ctx, "store", "SET", key, "open"); err != nil {
		t.Fatal(err)
	}
	pw2 := rotateOnServer(t, admin, user)
	b.call(b.root, "POST", mount+"/data/valkey", map[string]any{"data": map[string]any{"username": user, "password": pw2}})
	v, err := c.Command(ctx, "read", "GET", key)
	if err != nil || v != "open" {
		t.Fatalf("after the rotation: %v %v, want the stored value", v, err)
	}
	if c.CredentialVersion() != "2" {
		t.Fatalf("credential version %q, want KV version 2", c.CredentialVersion())
	}
}
