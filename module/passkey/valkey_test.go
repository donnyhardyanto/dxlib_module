package passkey

// These tests run against a real Valkey, as tools/test-valkey.sh starts one:
//
//	DXLIB_MODULE_TEST_VALKEY=127.0.0.1:16379 DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD=... go test ./module/passkey
//
// The challenges are kept by an ACL user limited to its own key prefix and to the commands README lists for
// passkeys, and the sessions by another limited to the session store's. Without the variables they are skipped.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/credentials"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// The commands the passkey challenge user needs; README's passkey section repeats this list.
const passkeyACLCommands = "+ping +select +eval +evalsha +script|load +set +get +del"

// The session store's list, as in module/session's tests and README.
const sessionACLCommands = "+ping +select +eval +evalsha +script|load +time +hset +hget +hmget +hdel +del +exists +pexpire +pttl +sadd +srem +smembers"

var _ Connection = (*credentials.RedisConnection)(nil)

type valkeyEnv struct {
	addr     string
	admin    *goredis.Client
	tag      string
	provider *credentials.Fixed
}

func newValkeyEnv(t *testing.T) *valkeyEnv {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_VALKEY")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_VALKEY is not set; run tools/test-valkey.sh")
	}
	admin := goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD")})
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("admin connection to Valkey at %s: %v", addr, err)
	}
	return &valkeyEnv{addr: addr, admin: admin, tag: strings.ReplaceAll(uuid.NewString()[:8], "-", ""), provider: credentials.NewFixed(nil)}
}

// conn makes an ACL user limited to prefix and commands, and a connection that logs in as it.
func (e *valkeyEnv) conn(t *testing.T, addr, user, prefix, commands string) *credentials.RedisConnection {
	t.Helper()
	ctx := context.Background()
	pw := "pw-" + uuid.NewString()
	args := []interface{}{"ACL", "SETUSER", user, "on", ">" + pw, "resetkeys", "~" + prefix + "*", "resetchannels", "-@all"}
	for _, c := range strings.Fields(commands) {
		args = append(args, c)
	}
	if err := e.admin.Do(ctx, args...).Err(); err != nil {
		t.Fatalf("ACL SETUSER %s: %v", user, err)
	}
	t.Cleanup(func() {
		e.admin.Do(context.Background(), "ACL", "DELUSER", user)
		if keys, _ := e.admin.Keys(context.Background(), prefix+"*").Result(); len(keys) > 0 {
			e.admin.Del(context.Background(), keys...)
		}
	})
	e.provider.Set("valkey/"+user, credentials.Credential{Username: user, Password: pw, Version: "1"})
	c := credentials.NewRedisConnection(user, addr, 0, "valkey/"+user, e.provider)
	c.CloseGrace = 10 * time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	return c
}

type valkeyRig struct {
	*rig
	sessions *session.ValkeyStore
	prefix   string
}

func (e *valkeyEnv) rig(t *testing.T, cfg Config, addr string) *valkeyRig {
	t.Helper()
	cfg.KeyPrefix = "test" + e.tag + ":passkey:"
	sessPrefix := "test" + e.tag + ":session:"
	audit := &auth_audit.Memory{}
	sessions, err := session.NewValkeyStore(e.conn(t, e.addr, "sess"+e.tag, sessPrefix, sessionACLCommands), session.Config{KeyPrefix: sessPrefix}, audit, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{store: NewMemoryStore(), accounts: newFakeAccounts(), audit: audit, auth: newSoftAuthenticator()}
	rp, err := New(cfg, e.conn(t, addr, "pk"+e.tag, cfg.KeyPrefix, passkeyACLCommands), r.store, sessions, r.accounts, audit)
	if err != nil {
		t.Fatal(err)
	}
	r.rp = rp
	return &valkeyRig{rig: r, sessions: sessions, prefix: cfg.KeyPrefix}
}

func (r *valkeyRig) registerKey(t *testing.T, user string) *softKey {
	t.Helper()
	ctx := context.Background()
	opts, h, err := r.rp.BeginRegistration(ctx, User{UUID: user, Username: "alice"})
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	resp, k := r.auth.create(t, opts, nil, flagUP|flagUV|flagBE|flagBS, yes)
	if _, err := r.rp.FinishRegistration(ctx, user, h, "Laptop", bytes.NewReader(resp), Client{}); err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	return k
}

func TestValkeyPasskeyLoginStartsASession(t *testing.T) {
	e := newValkeyEnv(t)
	r := e.rig(t, testConfig(), e.addr)
	ctx := context.Background()
	alice := uuid.NewString()
	k := r.registerKey(t, alice)

	opts, h, err := r.rp.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k.counter = 1
	tok, sess, err := r.rp.FinishLogin(ctx, h, bytes.NewReader(r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)), Client{Address: "192.0.2.7"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.sessions.Get(ctx, tok)
	if err != nil {
		t.Fatalf("the session is not in Valkey: %v", err)
	}
	if got.ID != sess.ID || got.UserUUID != alice || got.AuthMethod != "passkey" {
		t.Fatalf("session %+v", got)
	}
	ev := lastAudit(t, r.audit, auth_audit.LoginSucceeded)
	if ev.UserUUID != alice || ev.Detail["auth_method"] != "passkey" || ev.Detail["passkey_id"] != k.id() || ev.Detail["client_address"] != "192.0.2.7" {
		t.Fatalf("login audit %+v", ev)
	}
}

func TestValkeyChallengeIsHashedTakenOnceAndLivesFiveMinutes(t *testing.T) {
	e := newValkeyEnv(t)
	r := e.rig(t, testConfig(), e.addr)
	ctx := context.Background()
	alice := uuid.NewString()
	k := r.registerKey(t, alice)

	opts, h, err := r.rp.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := e.admin.Keys(ctx, r.prefix+"c:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("challenge keys %v (%v), want one", keys, err)
	}
	if strings.Contains(keys[0], h) || len(strings.TrimPrefix(keys[0], r.prefix+"c:")) != 64 {
		t.Fatalf("the key %q is not the prefix and a SHA-256 of the handle", keys[0])
	}
	ttl, err := e.admin.PTTL(ctx, keys[0]).Result()
	if err != nil || ttl > 5*time.Minute || ttl < 5*time.Minute-10*time.Second {
		t.Fatalf("challenge TTL %s (%v), want 5 minutes", ttl, err)
	}
	k.counter = 1
	resp := r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)
	if _, _, err := r.rp.FinishLogin(ctx, h, bytes.NewReader(resp), Client{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.admin.Exists(ctx, keys[0]).Result(); n != 0 {
		t.Fatal("the challenge is still in Valkey after its use")
	}
	_, _, err = r.rp.FinishLogin(ctx, h, bytes.NewReader(resp), Client{})
	mustRefuse(t, err, "unknown, already used or expired")
}

func TestValkeyAnExpiredChallengeIsRefused(t *testing.T) {
	e := newValkeyEnv(t)
	cfg := testConfig()
	cfg.ChallengeTTL = time.Second
	r := e.rig(t, cfg, e.addr)
	alice := uuid.NewString()
	k := r.registerKey(t, alice)
	opts, h, err := r.rp.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	k.counter = 1
	_, _, err = r.rp.FinishLogin(context.Background(), h, bytes.NewReader(r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)), Client{})
	mustRefuse(t, err, "unknown, already used or expired")
}

func TestValkeyDownRefusesLoginWithTheRealReason(t *testing.T) {
	e := newValkeyEnv(t)
	r := e.rig(t, testConfig(), "127.0.0.1:1") // nothing listens there
	_, _, err := r.rp.BeginLogin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connection refused") || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("want the dial error naming the address, got %v", err)
	}
	_, _, err = r.rp.FinishLogin(context.Background(), strings.Repeat("A", 43), strings.NewReader("{}"), Client{})
	mustRefuse(t, err, "connection refused")
}
