package user_photo

// These tests run against a real Valkey, as tools/test-valkey.sh starts one:
//
//	DXLIB_MODULE_TEST_VALKEY=127.0.0.1:16379 DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD=... go test ./module/user_photo
//
// The sessions are kept by an ACL user limited to the session store's prefix and commands. Without the variables
// they are skipped.

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/credentials"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// The session store's command list, as in module/session's tests and README.
const sessionACLCommands = "+ping +select +eval +evalsha +script|load +time +hset +hget +hmget +hdel +del +exists +pexpire +pttl +sadd +srem +smembers"

func valkeySessions(t *testing.T, loader session.ProfileLoader, audit auth_audit.Sink) *session.ValkeyStore {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_VALKEY")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_VALKEY is not set; run tools/test-valkey.sh")
	}
	admin := goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD")})
	t.Cleanup(func() { _ = admin.Close() })
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	user, prefix, pw := "photo"+tag, "test"+tag+":session:", "pw-"+uuid.NewString()
	args := []interface{}{"ACL", "SETUSER", user, "on", ">" + pw, "resetkeys", "~" + prefix + "*", "resetchannels", "-@all"}
	for _, c := range strings.Fields(sessionACLCommands) {
		args = append(args, c)
	}
	if err := admin.Do(bg, args...).Err(); err != nil {
		t.Fatalf("ACL SETUSER %s at %s: %v", user, addr, err)
	}
	t.Cleanup(func() {
		admin.Do(context.Background(), "ACL", "DELUSER", user)
		if keys, _ := admin.Keys(context.Background(), prefix+"*").Result(); len(keys) > 0 {
			admin.Del(context.Background(), keys...)
		}
	})
	provider := credentials.NewFixed(map[string]credentials.Credential{"valkey/" + user: {Username: user, Password: pw, Version: "1"}})
	conn := credentials.NewRedisConnection(user, addr, 0, "valkey/"+user, provider)
	conn.CloseGrace = 10 * time.Millisecond
	t.Cleanup(func() { _ = conn.Close() })
	s, err := session.NewValkeyStore(conn, session.Config{KeyPrefix: prefix}, audit, loader)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// serviceUsers is the service's own user table: its profile versions are a counter (1, 2, 3...), far below any
// timestamp, so a photo change that raised the version onto another scale would show here.
type serviceUsers struct {
	mu    sync.Mutex
	names map[string]string
	vers  map[string]int64
}

func (s *serviceUsers) LoadProfile(_ context.Context, u string) (session.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return session.Profile{Username: "user-" + u[:4], DisplayName: s.names[u], Status: "active", Version: s.vers[u]}, nil
}

func (s *serviceUsers) rename(u, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names[u] = name
	s.vers[u]++
}

func TestValkeyPhotoChangeKeepsLoginsAndLaterChangesWorking(t *testing.T) {
	alice := uuid.NewString()
	users := &serviceUsers{names: map[string]string{alice: "Alice"}, vers: map[string]int64{alice: 3}}
	store, objects, audit := NewMemoryStore(), NewMemoryObjectStore(), &auth_audit.Memory{}
	profiles, err := NewProfiles(users, store, Config{})
	if err != nil {
		t.Fatal(err)
	}
	sessions := valkeySessions(t, profiles, audit)
	m, err := New(Config{KeyPrefix: "user-photo/"}, Dependencies{Store: store, Objects: objects, Sessions: sessions, Profiles: profiles, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	login := func() (string, *session.Session) {
		t.Helper()
		// As password's and passkey's Accounts.LoginProfile must: the service's profile, filled through Profiles.
		p, err := profiles.LoadProfile(bg, alice)
		if err != nil {
			t.Fatal(err)
		}
		tok, s, err := sessions.Create(bg, session.LoginInfo{UserUUID: alice, AuthMethod: "password", Profile: p})
		if err != nil {
			t.Fatalf("login refused: %v", err)
		}
		return tok, s
	}
	tok1, s1 := login()
	tok2, _ := login()

	ch, err := m.Set(bg, s1, alice, bytes.NewReader(solidPNG(t, 100, 100, color.NRGBA{200, 0, 0, 255})))
	if err != nil || ch.SessionsErr != nil {
		t.Fatalf("set: %v / %v", err, ch.SessionsErr)
	}
	if ch.Propagation.Updated != 2 {
		t.Fatalf("propagation %+v: want both sessions updated", ch.Propagation)
	}
	for i, tok := range []string{tok1, tok2} {
		got, err := sessions.Get(bg, tok)
		if err != nil || got.Profile.PhotoRef != ch.Photo.Ref || got.Profile.PhotoVersion != 1 || got.Profile.Version != 3 {
			t.Fatalf("session %d after the photo change: %+v %v", i+1, got, err)
		}
	}

	// A new login after the photo change is accepted: the version marker is still on the service's scale.
	tok3, _ := login()

	// A later change of the service's own data reaches every session, the photo kept.
	users.rename(alice, "Alice Liddell")
	p, err := profiles.LoadProfile(bg, alice)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sessions.PropagateProfile(bg, alice, p)
	if err != nil || res.Updated != 3 || res.Newer != 0 {
		t.Fatalf("name change: %+v %v; want all 3 sessions updated, none counted newer", res, err)
	}
	for i, tok := range []string{tok1, tok2, tok3} {
		got, err := sessions.Get(bg, tok)
		if err != nil || got.Profile.DisplayName != "Alice Liddell" || got.Profile.PhotoRef != ch.Photo.Ref {
			t.Fatalf("session %d after the name change: %+v %v", i+1, got, err)
		}
	}

	// A second photo, then a removal, each reach all three.
	ch2, err := m.Set(bg, s1, alice, bytes.NewReader(solidPNG(t, 100, 100, color.NRGBA{0, 0, 200, 255})))
	if err != nil || ch2.Propagation.Updated != 3 {
		t.Fatalf("second photo: %+v %v", ch2, err)
	}
	if _, err := m.Remove(bg, s1, alice); err != nil {
		t.Fatal(err)
	}
	for i, tok := range []string{tok1, tok2, tok3} {
		got, err := sessions.Get(bg, tok)
		if err != nil || got.Profile.PhotoRef != "" || got.Profile.PhotoVersion != 0 || got.Profile.Version != 4 {
			t.Fatalf("session %d after the removal: %+v %v", i+1, got, err)
		}
	}

	// A cached profile dropped from a session is rebuilt through Profiles, photo included.
	_, _ = m.Set(bg, s1, alice, bytes.NewReader(solidPNG(t, 100, 100, color.White)))
	if err := sessions.InvalidateProfiles(bg, alice); err != nil {
		t.Fatal(err)
	}
	got, err := sessions.Get(bg, tok2)
	if err != nil || got.Profile.PhotoVersion != 4 || got.Profile.DisplayName != "Alice Liddell" {
		t.Fatalf("rebuilt profile %+v %v", got, err)
	}
}
