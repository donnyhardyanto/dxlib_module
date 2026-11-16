package session

// These tests run against a real Valkey. Start one and point the tests at it, as tools/test-valkey.sh does:
//
//	DXLIB_MODULE_TEST_VALKEY=127.0.0.1:16379 DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD=... go test ./module/session
//
// Each test makes its own ACL user limited to its own key prefix, so every store call below also proves the store
// works under such an ACL. Without the variables the tests are skipped.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/credentials"
)

// The commands the store's ACL user needs; README's session section repeats this list.
const aclCommands = "+ping +select +eval +evalsha +script|load +time +hset +hget +hmget +hdel +del +exists +pexpire +pttl +sadd +srem +smembers"

// The store's Connection is met by the credential-provider connection.
var _ Connection = (*credentials.RedisConnection)(nil)

type valkeyEnv struct {
	addr     string
	admin    *goredis.Client
	prefix   string
	aclUser  string
	provider *credentials.Fixed
	credName string
}

func newValkeyEnv(t *testing.T) *valkeyEnv {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_VALKEY")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_VALKEY is not set; run tools/test-valkey.sh")
	}
	admin := goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD")})
	t.Cleanup(func() { _ = admin.Close() })
	ctx := context.Background()
	if err := admin.Ping(ctx).Err(); err != nil {
		t.Fatalf("admin connection to Valkey at %s: %v", addr, err)
	}
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	e := &valkeyEnv{addr: addr, admin: admin, prefix: "test" + tag + ":session:", aclUser: "sess" + tag, credName: "valkey/sessions"}
	pw := "pw-" + uuid.NewString()
	if err := admin.Do(ctx, append([]interface{}{"ACL", "SETUSER", e.aclUser, "on", ">" + pw, "resetkeys", "~" + e.prefix + "*", "resetchannels", "-@all"}, splitArgs(aclCommands)...)...).Err(); err != nil {
		t.Fatalf("ACL SETUSER: %v", err)
	}
	t.Cleanup(func() {
		admin.Do(context.Background(), "ACL", "DELUSER", e.aclUser)
		keys, _ := admin.Keys(context.Background(), e.prefix+"*").Result()
		if len(keys) > 0 {
			admin.Del(context.Background(), keys...)
		}
	})
	e.provider = credentials.NewFixed(map[string]credentials.Credential{e.credName: {Username: e.aclUser, Password: pw, Version: "1"}})
	return e
}

func splitArgs(s string) []interface{} {
	var out []interface{}
	for _, f := range strings.Fields(s) {
		out = append(out, f)
	}
	return out
}

func (e *valkeyEnv) conn(t *testing.T, addr string) *credentials.RedisConnection {
	c := credentials.NewRedisConnection("sessions-test", addr, 0, e.credName, e.provider)
	c.CloseGrace = 10 * time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (e *valkeyEnv) store(t *testing.T, cfg Config, audit auth_audit.Sink, loader ProfileLoader) *ValkeyStore {
	return e.storeAt(t, e.addr, cfg, audit, loader)
}

func (e *valkeyEnv) storeAt(t *testing.T, addr string, cfg Config, audit auth_audit.Sink, loader ProfileLoader) *ValkeyStore {
	t.Helper()
	cfg.KeyPrefix = e.prefix
	s, err := NewValkeyStore(e.conn(t, addr), cfg, audit, loader)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func login(t *testing.T, s *ValkeyStore, user, name string) (string, *Session) {
	t.Helper()
	tok, sess, err := s.Create(context.Background(), LoginInfo{UserUUID: user, AuthMethod: "password",
		Profile: Profile{Username: name, DisplayName: name, Status: "active", Roles: []string{"user"}, Permissions: []string{"read"}, Version: 1}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tok, sess
}

func TestValkeyPropagationReachesEverySessionOfTheUser(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	tokA1, _ := login(t, s, alice, "alice")
	tokA2, _ := login(t, s, alice, "alice")
	tokB, _ := login(t, s, bob, "bob")

	res, err := s.PropagateProfile(ctx, alice, Profile{Username: "alice", DisplayName: "Alice Liddell", Status: "active",
		PhotoRef: "photos/new-key", PhotoVersion: 2, Roles: []string{"user", "editor"}, Permissions: []string{"read", "write"}, Version: 2})
	if err != nil {
		t.Fatalf("PropagateProfile: %v", err)
	}
	if res.Updated != 2 {
		t.Fatalf("updated %d sessions, want 2 (%+v)", res.Updated, res)
	}
	for _, tok := range []string{tokA1, tokA2} {
		got, err := s.Get(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		if got.Profile.DisplayName != "Alice Liddell" || got.Profile.PhotoRef != "photos/new-key" || got.Profile.PhotoVersion != 2 ||
			len(got.Profile.Roles) != 2 || got.Profile.Version != 2 {
			t.Fatalf("session %s did not get the new profile: %+v", got.ID, got.Profile)
		}
	}
	gotB, err := s.Get(ctx, tokB)
	if err != nil || gotB.Profile.DisplayName != "bob" {
		t.Fatalf("another user's session changed: %+v, %v", gotB, err)
	}

	// An older change arriving late does not overwrite the newer one.
	res, err = s.PropagateProfile(ctx, alice, Profile{DisplayName: "stale", Version: 1})
	if err != nil || res.Newer != 2 || res.Updated != 0 {
		t.Fatalf("stale propagation: %+v, %v", res, err)
	}
	if got, _ := s.Get(ctx, tokA1); got.Profile.DisplayName != "Alice Liddell" {
		t.Fatalf("stale version overwrote the profile: %+v", got.Profile)
	}
	if len(audit.OfKind(auth_audit.ProfilePropagated)) != 2 {
		t.Fatalf("propagation audit records: %+v", audit.Events())
	}
}

func TestValkeyStoresOnlyTheTokenHash(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{}, &auth_audit.Memory{}, nil)
	user := uuid.NewString()
	tok, sess := login(t, s, user, "u")
	ctx := context.Background()
	keys, err := e.admin.Keys(ctx, e.prefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{e.prefix + "s:" + sess.ID: true, e.prefix + "u:" + user: true}
	if len(keys) != 2 || !want[keys[0]] || !want[keys[1]] {
		t.Fatalf("keys %v, want %v", keys, want)
	}
	for _, k := range keys {
		if strings.Contains(k, tok) {
			t.Fatalf("key %q holds the token", k)
		}
	}
	vals, _ := e.admin.HGetAll(ctx, e.prefix+"s:"+sess.ID).Result()
	for f, v := range vals {
		if strings.Contains(v, tok) {
			t.Fatalf("field %s holds the token", f)
		}
	}
	if members, _ := e.admin.SMembers(ctx, e.prefix+"u:"+user).Result(); len(members) != 1 || members[0] != sess.ID {
		t.Fatalf("per-user index %v", members)
	}
	if !sess.AbsoluteExpiry.After(sess.CreatedAt) || sess.CSRFToken == "" {
		t.Fatalf("session fields: %+v", sess)
	}
}

func TestValkeyIdleExpirySlidesAndEnds(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{IdleTTL: 400 * time.Millisecond, AbsoluteTTL: 10 * time.Second}, &auth_audit.Memory{}, nil)
	tok, _ := login(t, s, uuid.NewString(), "u")
	ctx := context.Background()
	for i := 0; i < 4; i++ { // used every 250 ms, it outlives its 400 ms idle TTL
		time.Sleep(250 * time.Millisecond)
		if _, err := s.Get(ctx, tok); err != nil {
			t.Fatalf("Get after %d uses: %v", i, err)
		}
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := s.Get(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("idle session still there: %v", err)
	}
}

func TestValkeyAbsoluteExpiryEndsAnActiveSession(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{IdleTTL: 500 * time.Millisecond, AbsoluteTTL: 900 * time.Millisecond}, &auth_audit.Memory{}, nil)
	user := uuid.NewString()
	tok, sess := login(t, s, user, "u")
	ctx := context.Background()
	deadline := sess.AbsoluteExpiry
	for time.Now().Add(150 * time.Millisecond).Before(deadline) {
		if _, err := s.Get(ctx, tok); err != nil {
			t.Fatalf("Get before the absolute expiry: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Until(deadline) + 100*time.Millisecond)
	if _, err := s.Get(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session used all along outlived its absolute expiry: %v", err)
	}
	if ttl, _ := e.admin.PTTL(ctx, e.prefix+"u:"+user).Result(); ttl > 900*time.Millisecond {
		t.Fatalf("per-user index TTL %v outlives the absolute maximum", ttl)
	}
}

func TestValkeyRotateIssuesNewTokenKeepsExpiry(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	user := uuid.NewString()
	tok, sess := login(t, s, user, "u")
	ctx := context.Background()
	newTok, rotated, err := s.Rotate(ctx, tok, &Profile{Username: "u", DisplayName: "u", Roles: []string{"admin"}, Version: 5})
	if err != nil {
		t.Fatal(err)
	}
	if newTok == tok || rotated.ID == sess.ID || rotated.CSRFToken == sess.CSRFToken {
		t.Fatal("rotation kept the token, id or CSRF token")
	}
	if !rotated.CreatedAt.Equal(sess.CreatedAt) || !rotated.AbsoluteExpiry.Equal(sess.AbsoluteExpiry) {
		t.Fatalf("rotation moved the times: %+v vs %+v", rotated, sess)
	}
	if rotated.Profile.Roles[0] != "admin" || rotated.Profile.Version != 5 {
		t.Fatalf("rotation did not take the new profile: %+v", rotated.Profile)
	}
	if _, err := s.Get(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token still works: %v", err)
	}
	if got, err := s.Get(ctx, newTok); err != nil || got.Profile.Roles[0] != "admin" {
		t.Fatalf("new token: %+v, %v", got, err)
	}
	list, err := s.List(ctx, user)
	if err != nil || len(list) != 1 || list[0].ID != rotated.ID || list[0].CSRFToken != "" {
		t.Fatalf("List after rotation: %+v, %v", list, err)
	}
	if len(audit.OfKind(auth_audit.SessionRotated)) != 1 {
		t.Fatal("rotation not audited")
	}
}

func TestValkeyLogoutAndRevokeAll(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	ctx := context.Background()
	user := uuid.NewString()
	tok1, _ := login(t, s, user, "u")
	tok2, _ := login(t, s, user, "u")
	tok3, _ := login(t, s, user, "u")
	if err := s.Logout(ctx, tok1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, tok1); !errors.Is(err, ErrNotFound) {
		t.Fatal("logged-out session still readable")
	}
	if err := s.Logout(ctx, tok1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second logout: %v", err)
	}
	n, err := s.RevokeAll(ctx, user, "user disabled")
	if err != nil || n != 2 {
		t.Fatalf("RevokeAll = %d, %v", n, err)
	}
	for _, tok := range []string{tok2, tok3} {
		if _, err := s.Get(ctx, tok); !errors.Is(err, ErrNotFound) {
			t.Fatal("revoked session still readable")
		}
	}
	if len(audit.OfKind(auth_audit.Logout)) != 1 || len(audit.OfKind(auth_audit.SessionsRevoked)) != 1 || len(audit.OfKind(auth_audit.LoginSucceeded)) != 3 {
		t.Fatalf("audit records: %+v", audit.Events())
	}
}

// Another user's session id answers exactly as a missing one, so a caller cannot end or probe others' sessions.
func TestValkeyRevokeAnotherUsersSessionIsNotFound(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{}, &auth_audit.Memory{}, nil)
	ctx := context.Background()
	alice, mallory := uuid.NewString(), uuid.NewString()
	tokA, sessA := login(t, s, alice, "alice")
	login(t, s, mallory, "mallory")
	if err := s.RevokeSession(ctx, mallory, sessA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking another user's session: %v, want ErrNotFound", err)
	}
	if err := s.RevokeSession(ctx, mallory, strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking a missing session: %v, want ErrNotFound", err)
	}
	if _, err := s.Get(ctx, tokA); err != nil {
		t.Fatalf("alice's session was ended by mallory: %v", err)
	}
	if list, _ := s.List(ctx, mallory); len(list) != 1 {
		t.Fatalf("mallory sees %d sessions, want her own 1", len(list))
	}
	if err := s.RevokeSession(ctx, alice, sessA.ID); err != nil {
		t.Fatalf("alice revoking her own session: %v", err)
	}
}

func TestValkeyInjectionPayloadsStayData(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{}, &auth_audit.Memory{}, nil)
	ctx := context.Background()
	canary := e.prefix + "canary"
	if err := e.admin.Set(ctx, canary, "alive", 0).Err(); err != nil {
		t.Fatal(err)
	}
	payloads := []string{
		"*", "' OR '1'='1", "\r\nFLUSHALL\r\n", "]]..redis.call('FLUSHALL')..[[", "\"); redis.call('DEL', KEYS[1]); --",
		"%s%s%n", "{\"username\":\"admin\"}", "../" + e.prefix, "\x00\xff", strings.Repeat("A", 4096),
	}
	for _, p := range payloads {
		if _, _, err := s.Create(ctx, LoginInfo{UserUUID: p, AuthMethod: "password"}); err == nil {
			t.Errorf("Create accepted user uuid %q", p)
		}
		if _, _, err := s.Create(ctx, LoginInfo{UserUUID: uuid.NewString(), AuthMethod: p}); err == nil {
			t.Errorf("Create accepted auth method %q", p)
		}
		if _, err := s.Get(ctx, p); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Get(%q) = %v, want ErrInvalidToken", p, err)
		}
		if err := s.RevokeSession(ctx, uuid.NewString(), p); !errors.Is(err, ErrNotFound) {
			t.Errorf("RevokeSession(%q) = %v, want ErrNotFound", p, err)
		}
		if _, err := s.PropagateProfile(ctx, p, Profile{}); err == nil {
			t.Errorf("PropagateProfile accepted user uuid %q", p)
		}
	}
	// In profile fields the payloads are only text: stored and read back unchanged.
	user := uuid.NewString()
	for i, p := range payloads {
		tok, _, err := s.Create(ctx, LoginInfo{UserUUID: user, AuthMethod: "password", Profile: Profile{Username: p, DisplayName: p, Roles: []string{p}, Version: int64(i)}})
		if err != nil {
			t.Fatalf("Create with payload profile %q: %v", p, err)
		}
		got, err := s.Get(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ToValidUTF8(p, "�")
		if got.Profile.DisplayName != want || got.Profile.Username != want || got.Profile.Roles[0] != want {
			t.Errorf("payload %q came back as %q", p, got.Profile.DisplayName)
		}
	}
	if _, err := s.PropagateProfile(ctx, user, Profile{DisplayName: payloads[3], Version: 100}); err != nil {
		t.Fatal(err)
	}
	if v, err := e.admin.Get(ctx, canary).Result(); err != nil || v != "alive" {
		t.Fatalf("canary key changed: %q, %v", v, err)
	}
}

func TestValkeyACLLimitsTheStoreToItsPrefix(t *testing.T) {
	e := newValkeyEnv(t)
	c := e.conn(t, e.addr)
	ctx := context.Background()
	_, err := c.Command(ctx, "outside the prefix", "SET", "other:key", "x")
	if err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("write outside the prefix: %v, want NOPERM", err)
	}
	_, err = c.Command(ctx, "inside the prefix", "HSET", e.prefix+"x", "a", "b")
	if err != nil {
		t.Fatalf("write inside the prefix: %v", err)
	}
	_, err = c.Command(ctx, "flushall", "FLUSHALL")
	if err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("FLUSHALL: %v, want NOPERM", err)
	}
}

func TestValkeyCredentialRotationKeepsOpenSessions(t *testing.T) {
	e := newValkeyEnv(t)
	s := e.store(t, Config{}, &auth_audit.Memory{}, nil)
	ctx := context.Background()
	tok, _ := login(t, s, uuid.NewString(), "u")

	// The secret store rotates the password: Valkey takes only the new one, existing connections are cut, and
	// the provider hands out the new credential.
	newPw := "pw-" + uuid.NewString()
	if err := e.admin.Do(ctx, "ACL", "SETUSER", e.aclUser, "resetpass", ">"+newPw).Err(); err != nil {
		t.Fatal(err)
	}
	if err := e.admin.Do(ctx, "CLIENT", "KILL", "USER", e.aclUser).Err(); err != nil {
		t.Fatal(err)
	}
	e.provider.Set(e.credName, credentials.Credential{Username: e.aclUser, Password: newPw, Version: "2"})

	got, err := s.Get(ctx, tok)
	if err != nil {
		t.Fatalf("session after credential rotation: %v", err)
	}
	if got.Profile.Username != "u" {
		t.Fatalf("session lost its data: %+v", got)
	}

	// A dynamic credential close to its expiry is replaced by the provider's newer one before the next command,
	// while a credential with no expiry is kept until the server refuses it.
	c := e.conn(t, e.addr)
	c.RenewBefore = time.Minute
	ping := func() {
		t.Helper()
		if _, err := c.Command(ctx, "ping", "PING"); err != nil {
			t.Fatal(err)
		}
	}
	e.provider.Set(e.credName, credentials.Credential{Username: e.aclUser, Password: newPw, Version: "3", ExpiresAt: time.Now().Add(10 * time.Second)})
	ping()
	if v := c.CredentialVersion(); v != "3" {
		t.Fatalf("credential version %q, want 3", v)
	}
	ping() // the provider has nothing newer: the connection stays
	e.provider.Set(e.credName, credentials.Credential{Username: e.aclUser, Password: newPw, Version: "4", ExpiresAt: time.Now().Add(time.Hour)})
	ping()
	if v := c.CredentialVersion(); v != "4" {
		t.Fatalf("expiring credential not renewed: version %q, want 4", v)
	}
	e.provider.Set(e.credName, credentials.Credential{Username: e.aclUser, Password: newPw, Version: "5"})
	ping()
	if v := c.CredentialVersion(); v != "4" {
		t.Fatalf("a credential far from expiry was replaced without cause: version %q, want 4", v)
	}
	if v, _ := s.conn.(*credentials.RedisConnection); v.CredentialVersion() != "2" {
		t.Fatalf("store connection did not reconnect with the rotated credential: version %q, want 2", v.CredentialVersion())
	}
}

// startProxy forwards TCP to Valkey until stopped; stopping it cuts every connection and refuses new ones, as a
// Valkey that goes down does.
func startProxy(t *testing.T, target string) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			mu.Lock()
			conns = append(conns, c, u)
			mu.Unlock()
			go func() { _, _ = io.Copy(u, c); _ = u.Close() }()
			go func() { _, _ = io.Copy(c, u); _ = c.Close() }()
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			_ = ln.Close()
			mu.Lock()
			for _, c := range conns {
				_ = c.Close()
			}
			mu.Unlock()
		})
	}
	t.Cleanup(stop)
	return ln.Addr().String(), stop
}

func TestValkeyDownRefusesLoginWithTheRealReason(t *testing.T) {
	e := newValkeyEnv(t)

	// Never reachable: a port nobody listens on.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	refused := ln.Addr().String()
	_ = ln.Close()
	audit := &auth_audit.Memory{}
	s := e.storeAt(t, refused, Config{}, audit, nil)
	_, _, err := s.Create(context.Background(), LoginInfo{UserUUID: uuid.NewString(), AuthMethod: "password"})
	if err == nil || !strings.Contains(err.Error(), "login refused") || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), refused) {
		t.Fatalf("login with Valkey unreachable: %v", err)
	}
	if f := audit.OfKind(auth_audit.LoginFailed); len(f) != 1 || !strings.Contains(f[0].Reason, "refused") {
		t.Fatalf("refused login not audited with its reason: %+v", audit.Events())
	}

	// Goes down while in use.
	addr, stop := startProxy(t, e.addr)
	audit2 := &auth_audit.Memory{}
	s2 := e.storeAt(t, addr, Config{}, audit2, nil)
	user := uuid.NewString()
	tok, _ := login(t, s2, user, "u")
	stop()
	if _, err := s2.Get(context.Background(), tok); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Get with Valkey down: %v, want the connection error (never a silent not-found)", err)
	} else {
		t.Logf("Get with Valkey down: %v", err)
	}
	_, _, err = s2.Create(context.Background(), LoginInfo{UserUUID: user, AuthMethod: "password"})
	if err == nil || !strings.Contains(err.Error(), "login refused") {
		t.Fatalf("login with Valkey gone: %v", err)
	}
	t.Logf("login with Valkey down: %v", err)
}

func TestValkeyPropagationFailureDeletesSessions(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	ctx := context.Background()
	user := uuid.NewString()
	tok1, _ := login(t, s, user, "u")
	tok2, _ := login(t, s, user, "u")

	// The update cannot be written (HSET taken away), but deleting still works.
	if err := e.admin.Do(ctx, "ACL", "SETUSER", e.aclUser, "-hset").Err(); err != nil {
		t.Fatal(err)
	}
	_, err := s.PropagateProfile(ctx, user, Profile{DisplayName: "new", Version: 2})
	var perr *PropagationError
	if !errors.As(err, &perr) || perr.Deleted != 2 || perr.DeleteErr != nil || !strings.Contains(err.Error(), "no permissions") {
		t.Fatalf("PropagateProfile with HSET refused: %v", err)
	}
	for _, tok := range []string{tok1, tok2} {
		if _, err := s.Get(ctx, tok); !errors.Is(err, ErrNotFound) {
			t.Fatalf("session survived a failed propagation: %v", err)
		}
	}
	if f := audit.OfKind(auth_audit.PropagationFailed); len(f) != 1 || !strings.Contains(f[0].Reason, "no permissions") || f[0].Detail["sessions_deleted"] != "2" {
		t.Fatalf("failed propagation audit: %+v", f)
	}
}

func TestValkeyPropagationWithValkeyDownReportsBothFailures(t *testing.T) {
	e := newValkeyEnv(t)
	addr, stop := startProxy(t, e.addr)
	audit := &auth_audit.Memory{}
	s := e.storeAt(t, addr, Config{OperationTimeout: 2 * time.Second}, audit, nil)
	user := uuid.NewString()
	login(t, s, user, "u")
	stop()
	_, err := s.PropagateProfile(context.Background(), user, Profile{DisplayName: "new", Version: 2})
	var perr *PropagationError
	if !errors.As(err, &perr) || perr.UpdateErr == nil || perr.DeleteErr == nil {
		t.Fatalf("PropagateProfile with Valkey down: %v", err)
	}
	if len(audit.OfKind(auth_audit.PropagationFailed)) != 1 {
		t.Fatal("failure not audited")
	}
	t.Logf("propagation with Valkey down: %v", err)
}

func TestValkeyProfileRebuiltFromTheAuthorityOnAMiss(t *testing.T) {
	e := newValkeyEnv(t)
	calls := 0
	loader := ProfileLoaderFunc(func(_ context.Context, u string) (Profile, error) {
		calls++
		return Profile{Username: "u", DisplayName: "from database", Version: 9}, nil
	})
	s := e.store(t, Config{}, &auth_audit.Memory{}, loader)
	ctx := context.Background()
	user := uuid.NewString()
	tok, _ := login(t, s, user, "u")
	if err := s.InvalidateProfiles(ctx, user); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, tok)
	if err != nil || got.Profile.DisplayName != "from database" || calls != 1 {
		t.Fatalf("rebuild: %+v, %v, %d loader calls", got, err, calls)
	}
	if got, _ = s.Get(ctx, tok); got.Profile.DisplayName != "from database" || calls != 1 {
		t.Fatalf("rebuilt profile not cached again: %d loader calls", calls)
	}

	// No loader, or a failing one: the session is refused, never served without its profile.
	failing := e.store(t, Config{}, &auth_audit.Memory{}, ProfileLoaderFunc(func(context.Context, string) (Profile, error) {
		return Profile{}, fmt.Errorf("database unreachable")
	}))
	_ = s.InvalidateProfiles(ctx, user)
	if _, err := failing.Get(ctx, tok); err == nil || !strings.Contains(err.Error(), "database unreachable") {
		t.Fatalf("Get with a failing loader: %v", err)
	}
}

func TestValkeyLoginRefusedWhenTheAuditCannotBeWritten(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{Fail: errors.New("audit database read-only")}
	s := e.store(t, Config{}, audit, nil)
	user := uuid.NewString()
	_, _, err := s.Create(context.Background(), LoginInfo{UserUUID: user, AuthMethod: "password"})
	if err == nil || !strings.Contains(err.Error(), "audit database read-only") {
		t.Fatalf("Create with the audit down: %v", err)
	}
	if list, _ := s.List(context.Background(), user); len(list) != 0 {
		t.Fatalf("an unaudited session was left: %+v", list)
	}
}

// A login that read the profile just before a change was propagated must not store the older copy: the session
// then holds the propagated version (reloaded from the authority), or the login is refused.
func TestValkeyLoginRacingAPropagationNeverKeepsTheOlderProfile(t *testing.T) {
	e := newValkeyEnv(t)
	ctx := context.Background()
	user := uuid.NewString()
	current := Profile{Username: "u", DisplayName: "after the change", Roles: []string{"user"}, Version: 2}
	loader := ProfileLoaderFunc(func(context.Context, string) (Profile, error) { return current, nil })
	s := e.store(t, Config{}, &auth_audit.Memory{}, loader)

	// The change (an administrator removes the admin role) is committed and propagated while the user has no
	// session yet; then the login that read version 1 finishes.
	if res, err := s.PropagateProfile(ctx, user, current); err != nil || res.Updated != 0 {
		t.Fatalf("PropagateProfile with no session: %+v, %v", res, err)
	}
	tok, sess, err := s.Create(ctx, LoginInfo{UserUUID: user, AuthMethod: "password",
		Profile: Profile{Username: "u", DisplayName: "before the change", Roles: []string{"user", "admin"}, Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Profile.Version != 2 || len(sess.Profile.Roles) != 1 {
		t.Fatalf("Create kept the older profile: %+v", sess.Profile)
	}
	if got, err := s.Get(ctx, tok); err != nil || got.Profile.Version != 2 || got.Profile.DisplayName != "after the change" {
		t.Fatalf("Get after the racing login: %+v, %v", got, err)
	}

	// A rotation carrying the older profile does not bring it back.
	_, rotated, err := s.Rotate(ctx, tok, &Profile{Username: "u", Roles: []string{"user", "admin"}, Version: 1})
	if err != nil || rotated.Profile.Version != 2 || len(rotated.Profile.Roles) != 1 {
		t.Fatalf("Rotate with an older profile: %+v, %v", rotated, err)
	}

	// A rebuild from a lagging authority (version 1) is refused, never stored.
	lagging := e.store(t, Config{}, &auth_audit.Memory{}, ProfileLoaderFunc(func(context.Context, string) (Profile, error) {
		return Profile{Username: "u", Roles: []string{"user", "admin"}, Version: 1}, nil
	}))
	tok2, _, err := s.Create(ctx, LoginInfo{UserUUID: user, AuthMethod: "password", Profile: current})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateProfiles(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err := lagging.Get(ctx, tok2); err == nil || !strings.Contains(err.Error(), "older than") {
		t.Fatalf("rebuild from a lagging authority: %v", err)
	}

	// Without a loader the racing login is refused and leaves no session.
	noLoader := e.store(t, Config{}, &auth_audit.Memory{}, nil)
	other := uuid.NewString()
	if _, err := noLoader.PropagateProfile(ctx, other, Profile{Version: 5}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := noLoader.Create(ctx, LoginInfo{UserUUID: other, AuthMethod: "password", Profile: Profile{Version: 4}}); err == nil || !strings.Contains(err.Error(), "login refused") {
		t.Fatalf("racing login without a loader: %v", err)
	}
	if list, _ := noLoader.List(ctx, other); len(list) != 0 {
		t.Fatalf("refused login left a session: %+v", list)
	}
}

func TestValkeyRotationRefusedWhenTheAuditCannotBeWritten(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	user := uuid.NewString()
	tok, _ := login(t, s, user, "u")
	audit.Fail = errors.New("audit database read-only")
	newTok, _, err := s.Rotate(context.Background(), tok, nil)
	if err == nil || newTok != "" || !strings.Contains(err.Error(), "audit database read-only") {
		t.Fatalf("Rotate with the audit down: %q, %v", newTok, err)
	}
	if list, _ := s.List(context.Background(), user); len(list) != 0 {
		t.Fatalf("an unaudited rotated session was left: %+v", list)
	}
}

// A rotation whose session has no cached profile and whose rebuild fails ends the session: the new token is never
// handed out, so the new session must not stay behind without its audit record.
func TestValkeyRotationRefusedWhenTheProfileCannotBeRebuilt(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, ProfileLoaderFunc(func(context.Context, string) (Profile, error) {
		return Profile{}, errors.New("database unreachable")
	}))
	ctx := context.Background()
	user := uuid.NewString()
	tok, _ := login(t, s, user, "u")
	if err := s.InvalidateProfiles(ctx, user); err != nil {
		t.Fatal(err)
	}
	newTok, _, err := s.Rotate(ctx, tok, nil)
	if err == nil || newTok != "" || !strings.Contains(err.Error(), "database unreachable") || !strings.Contains(err.Error(), "rotation refused") {
		t.Fatalf("Rotate with a failing rebuild: %q, %v", newTok, err)
	}
	if list, err := s.List(ctx, user); err != nil || len(list) != 0 {
		t.Fatalf("a rotated session without its token or audit record was left: %+v, %v", list, err)
	}
	if n, _ := e.admin.SCard(ctx, e.prefix+"u:"+user).Result(); n != 0 {
		t.Fatalf("the user's index still lists %d sessions", n)
	}
	if r := audit.OfKind(auth_audit.SessionRotated); len(r) != 0 {
		t.Fatalf("a refused rotation was audited as done: %+v", r)
	}
}

func TestValkeyLoginAuditCarriesTheCallersDetailButNotOverItsOwn(t *testing.T) {
	e := newValkeyEnv(t)
	audit := &auth_audit.Memory{}
	s := e.store(t, Config{}, audit, nil)
	user := uuid.NewString()
	_, sess, err := s.Create(context.Background(), LoginInfo{UserUUID: user, AuthMethod: "passkey", ClientAddress: "192.0.2.9",
		Profile:     Profile{Username: "u", Status: "active", Version: 1},
		AuditDetail: map[string]string{"passkey_id": "abc", "auth_method": "password", "client_address": "198.51.100.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ev := audit.OfKind(auth_audit.LoginSucceeded)
	if len(ev) != 1 || ev[0].SessionID != sess.ID {
		t.Fatalf("login audits %+v", ev)
	}
	d := ev[0].Detail
	if d["passkey_id"] != "abc" || d["auth_method"] != "passkey" || d["client_address"] != "192.0.2.9" {
		t.Fatalf("audit detail %v", d)
	}
}
