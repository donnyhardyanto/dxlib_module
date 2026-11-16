package passkey

// The ceremonies, run without servers: challenges in memory (the Valkey store is tested in valkey_test.go),
// passkeys in a MemoryStore, sessions in a recording fake.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

// fakeSessions records the logins; only Create is used by the relying party.
type fakeSessions struct {
	session.Store
	mu     sync.Mutex
	logins []session.LoginInfo
	fail   error
}

func (f *fakeSessions) Create(_ context.Context, l session.LoginInfo) (string, *session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", nil, f.fail
	}
	f.logins = append(f.logins, l)
	return "token", &session.Session{ID: "id", UserUUID: l.UserUUID, AuthMethod: l.AuthMethod, Profile: l.Profile}, nil
}

type fakeAccounts struct {
	mu          sync.Mutex
	refused     map[string]error
	hasPassword map[string]bool
	passwordErr error
	asked       int
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{refused: map[string]error{}, hasPassword: map[string]bool{}}
}

func (f *fakeAccounts) LoginProfile(_ context.Context, u string) (session.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.refused[u]; err != nil {
		return session.Profile{}, err
	}
	return session.Profile{Username: "user-" + u[:8], DisplayName: "User", Status: "active", Roles: []string{"user"}, Version: 1}, nil
}

func (f *fakeAccounts) HasPassword(_ context.Context, u string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	if f.passwordErr != nil {
		return false, f.passwordErr
	}
	return f.hasPassword[u], nil
}

type rig struct {
	rp       *RelyingParty
	store    *MemoryStore
	sessions *fakeSessions
	accounts *fakeAccounts
	audit    *auth_audit.Memory
	chal     *memoryChallenges
	auth     *softAuthenticator
}

// nopConn satisfies New; the rig replaces the challenge store with one in memory.
type nopConn struct{}

func (nopConn) Eval(context.Context, string, string, []string, ...interface{}) (interface{}, error) {
	return nil, errors.New("nopConn: not used")
}

func testConfig() Config {
	return Config{RPID: testRPID, RPDisplayName: "Example", RPOrigins: []string{testOrigin}, KeyPrefix: "test:passkey:"}
}

func newRig(t *testing.T, cfg Config) *rig {
	t.Helper()
	r := &rig{store: NewMemoryStore(), sessions: &fakeSessions{}, accounts: newFakeAccounts(), audit: &auth_audit.Memory{},
		chal: &memoryChallenges{}, auth: newSoftAuthenticator()}
	rp, err := New(cfg, nopConn{}, r.store, r.sessions, r.accounts, r.audit)
	if err != nil {
		t.Fatal(err)
	}
	rp.challenges = r.chal
	r.rp = rp
	return r
}

var yes = func() *bool { b := true; return &b }()

func (r *rig) register(t *testing.T, user, name string) *softKey {
	t.Helper()
	ctx := context.Background()
	opts, h, err := r.rp.BeginRegistration(ctx, User{UUID: user, Username: "u", DisplayName: "U"})
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	resp, k := r.auth.create(t, opts, nil, flagUP|flagUV|flagBE|flagBS, yes)
	if _, err := r.rp.FinishRegistration(ctx, user, h, name, bytes.NewReader(resp), Client{Address: "192.0.2.1"}); err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	return k
}

func (r *rig) login(t *testing.T, k *softKey, flags byte, handle []byte) error {
	t.Helper()
	opts, h, err := r.rp.BeginLogin(context.Background())
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	k.counter++
	_, _, err = r.rp.FinishLogin(context.Background(), h, bytes.NewReader(r.auth.get(t, opts, k, flags, handle)), Client{Address: "192.0.2.1"})
	return err
}

func mustRefuse(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want an error wrapping ErrRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not name %q", err, want)
	}
}

func lastAudit(t *testing.T, a *auth_audit.Memory, kind auth_audit.Kind) auth_audit.Event {
	t.Helper()
	ev := a.OfKind(kind)
	if len(ev) == 0 {
		t.Fatalf("no %s audit record", kind)
	}
	return ev[len(ev)-1]
}

func TestRegisterAndLoginWithAPasskey(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	ctx := context.Background()

	opts, h, err := r.rp.BeginRegistration(ctx, User{UUID: alice, Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	o := parseOptions(t, opts)
	if o.PublicKey.AuthenticatorSelection.ResidentKey != "required" || o.PublicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("registration must require a discoverable credential and user verification: %s", opts)
	}
	if _, ok := o.PublicKey.Extensions["credProps"]; !ok {
		t.Fatalf("registration does not ask for credProps: %s", opts)
	}
	if o.PublicKey.Timeout != int(DefaultChallengeTTL.Milliseconds()) {
		t.Fatalf("timeout %d ms, want %d", o.PublicKey.Timeout, DefaultChallengeTTL.Milliseconds())
	}
	resp, k := r.auth.create(t, opts, nil, flagUP|flagUV|flagBE|flagBS, yes)
	p, err := r.rp.FinishRegistration(ctx, alice, h, "  Laptop  ", bytes.NewReader(resp), Client{Address: "192.0.2.1", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != k.id() || p.UserUUID != alice || p.Name != "Laptop" || !p.BackupEligible || !p.BackupState {
		t.Fatalf("stored passkey %+v", p)
	}
	if !bytes.Equal(k.userHandle, userHandle(alice)) {
		t.Fatal("the user handle is not the uuid's bytes")
	}
	ev := lastAudit(t, r.audit, auth_audit.CredentialRegistered)
	if ev.Outcome != auth_audit.OutcomeOK || ev.UserUUID != alice || ev.Detail["passkey_id"] != k.id() || ev.Detail["passkey_name"] != "Laptop" {
		t.Fatalf("registration audit %+v", ev)
	}

	lopts, lh, err := r.rp.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lo := parseOptions(t, lopts)
	if lo.PublicKey.UserVerification != "required" {
		t.Fatalf("login must require user verification: %s", lopts)
	}
	if strings.Contains(string(lopts), "allowCredentials") {
		t.Fatalf("a discoverable login names no credentials: %s", lopts)
	}
	k.counter = 7
	tok, sess, err := r.rp.FinishLogin(ctx, lh, bytes.NewReader(r.auth.get(t, lopts, k, flagUP|flagUV|flagBE|flagBS, k.userHandle)), Client{Address: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" || sess.UserUUID != alice || sess.AuthMethod != AuthMethod {
		t.Fatalf("session %+v", sess)
	}
	l := r.sessions.logins[0]
	if l.AuthMethod != "passkey" || l.AuditDetail["passkey_id"] != k.id() || l.Profile.Username == "" {
		t.Fatalf("login info %+v", l)
	}
	stored, _ := r.store.ByID(ctx, k.id())
	c, err := decodeCredential(*stored)
	if err != nil {
		t.Fatal(err)
	}
	if c.Authenticator.SignCount != 7 || stored.LastUsedAt.IsZero() {
		t.Fatalf("sign count %d and last use %v were not recorded", c.Authenticator.SignCount, stored.LastUsedAt)
	}
	if n := len(r.audit.OfKind(auth_audit.LoginFailed)); n != 0 {
		t.Fatalf("%d login failures audited", n)
	}
}

func TestSeveralPasskeysPerUserAndTheUsersOwnAreExcluded(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k1 := r.register(t, alice, "Laptop")
	opts, _, err := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	if err != nil {
		t.Fatal(err)
	}
	o := parseOptions(t, opts)
	if len(o.PublicKey.ExcludeCredentials) != 1 || o.PublicKey.ExcludeCredentials[0].ID != k1.id() {
		t.Fatalf("the user's passkey is not excluded: %s", opts)
	}
	k2 := r.register(t, alice, "Phone")
	list, err := r.rp.List(context.Background(), alice)
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v, %d passkeys", err, len(list))
	}
	for _, k := range []*softKey{k1, k2} {
		if err := r.login(t, k, flagUP|flagUV, k.userHandle); err != nil {
			t.Fatalf("login with %s: %v", k.id(), err)
		}
	}
}

func TestTooManyPasskeysAreRefused(t *testing.T) {
	cfg := testConfig()
	cfg.MaxPerUser = 1
	r := newRig(t, cfg)
	alice := uuid.NewString()
	r.register(t, alice, "Laptop")
	if _, _, err := r.rp.BeginRegistration(context.Background(), User{UUID: alice}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("want ErrTooMany, got %v", err)
	}
	if e := lastAudit(t, r.audit, auth_audit.CredentialRegistered); e.Outcome != auth_audit.OutcomeFailed || e.UserUUID != alice ||
		!strings.Contains(e.Reason, ErrTooMany.Error()) {
		t.Fatalf("the refused start is not audited with its reason: %+v", e)
	}
}

// Two registrations started while the user held none, finished after the first fills the limit: the store refuses
// the second, and the caller can tell why.
func TestTooManyFromTheStoreReachesTheCaller(t *testing.T) {
	cfg := testConfig()
	cfg.MaxPerUser = 1
	r := newRig(t, cfg)
	ctx := context.Background()
	alice := uuid.NewString()
	o1, h1, err1 := r.rp.BeginRegistration(ctx, User{UUID: alice})
	o2, h2, err2 := r.rp.BeginRegistration(ctx, User{UUID: alice})
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	resp1, _ := r.auth.create(t, o1, nil, flagUP|flagUV, yes)
	resp2, _ := r.auth.create(t, o2, nil, flagUP|flagUV, yes)
	if _, err := r.rp.FinishRegistration(ctx, alice, h1, "One", bytes.NewReader(resp1), Client{}); err != nil {
		t.Fatal(err)
	}
	_, err := r.rp.FinishRegistration(ctx, alice, h2, "Two", bytes.NewReader(resp2), Client{})
	if !errors.Is(err, ErrRefused) || !errors.Is(err, ErrTooMany) {
		t.Fatalf("want an error wrapping ErrRefused and ErrTooMany, got %v", err)
	}
}

func TestRegistrationWithoutUserVerificationIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	resp, _ := r.auth.create(t, opts, nil, flagUP, yes)
	_, err := r.rp.FinishRegistration(context.Background(), alice, h, "Key", bytes.NewReader(resp), Client{})
	mustRefuse(t, err, "User verification")
	if l, _ := r.store.ByUser(context.Background(), alice); len(l) != 0 {
		t.Fatal("a passkey was stored")
	}
	ev := lastAudit(t, r.audit, auth_audit.CredentialRegistered)
	if ev.Outcome != auth_audit.OutcomeFailed || !strings.Contains(ev.Reason, "User verification") {
		t.Fatalf("refusal audit %+v", ev)
	}
}

func TestLoginWithoutUserVerificationIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	mustRefuse(t, r.login(t, k, flagUP, k.userHandle), "User verification")
	if len(r.sessions.logins) != 0 {
		t.Fatal("a session was started")
	}
	ev := lastAudit(t, r.audit, auth_audit.LoginFailed)
	if ev.UserUUID != alice || ev.Detail["passkey_id"] != k.id() || ev.Detail["auth_method"] != "passkey" {
		t.Fatalf("failure audit %+v", ev)
	}
}

func TestNonDiscoverableCredentialIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	no := false
	resp, _ := r.auth.create(t, opts, nil, flagUP|flagUV, &no)
	_, err := r.rp.FinishRegistration(context.Background(), alice, h, "Key", bytes.NewReader(resp), Client{})
	mustRefuse(t, err, "not discoverable")
}

func TestAChallengeIsUsedOnce(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	opts, h, _ := r.rp.BeginLogin(context.Background())
	k.counter = 1
	resp := r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)
	if _, _, err := r.rp.FinishLogin(context.Background(), h, bytes.NewReader(resp), Client{}); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.rp.FinishLogin(context.Background(), h, bytes.NewReader(resp), Client{})
	mustRefuse(t, err, "unknown, already used or expired")

	// A refused answer uses the challenge up too.
	opts, h, _ = r.rp.BeginLogin(context.Background())
	bad := r.auth.get(t, opts, k, flagUP, k.userHandle)
	_, _, err = r.rp.FinishLogin(context.Background(), h, bytes.NewReader(bad), Client{})
	mustRefuse(t, err, "User verification")
	k.counter = 5
	good := r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)
	_, _, err = r.rp.FinishLogin(context.Background(), h, bytes.NewReader(good), Client{})
	mustRefuse(t, err, "unknown, already used or expired")
}

func TestAnExpiredChallengeIsRefused(t *testing.T) {
	cfg := testConfig()
	cfg.ChallengeTTL = time.Second
	r := newRig(t, cfg)
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	opts, h, _ := r.rp.BeginLogin(context.Background())
	time.Sleep(1100 * time.Millisecond)
	k.counter = 1
	_, _, err := r.rp.FinishLogin(context.Background(), h, bytes.NewReader(r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)), Client{})
	mustRefuse(t, err, "unknown, already used or expired")
}

func TestChallengeTTLLongerThanFiveMinutesIsRefused(t *testing.T) {
	cfg := testConfig()
	cfg.ChallengeTTL = 6 * time.Minute
	if _, err := New(cfg, nopConn{}, NewMemoryStore(), &fakeSessions{}, newFakeAccounts(), &auth_audit.Memory{}); err == nil {
		t.Fatal("a 6 minute challenge TTL was accepted")
	}
}

func TestARegistrationChallengeCannotLogIn(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	k.counter = 1
	_, _, err := r.rp.FinishLogin(context.Background(), h, bytes.NewReader(r.auth.get(t, opts, k, flagUP|flagUV, k.userHandle)), Client{})
	mustRefuse(t, err, "issued for a register, not a login")
}

func TestAnotherUsersRegistrationChallengeIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice, bob := uuid.NewString(), uuid.NewString()
	opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	resp, _ := r.auth.create(t, opts, nil, flagUP|flagUV, yes)
	_, err := r.rp.FinishRegistration(context.Background(), bob, h, "Key", bytes.NewReader(resp), Client{})
	mustRefuse(t, err, "issued to user "+alice)
	if l, _ := r.store.ByUser(context.Background(), bob); len(l) != 0 {
		t.Fatal("bob got a passkey")
	}
}

func TestADuplicateCredentialIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice, bob := uuid.NewString(), uuid.NewString()
	k := r.register(t, alice, "Laptop")
	// An authenticator that ignores the exclusion list and answers with the same credential, for alice and for bob.
	for _, u := range []string{alice, bob} {
		opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: u})
		resp, _ := r.auth.create(t, opts, k, flagUP|flagUV, yes)
		_, err := r.rp.FinishRegistration(context.Background(), u, h, "Again", bytes.NewReader(resp), Client{})
		mustRefuse(t, err, ErrDuplicate.Error())
		if !errors.Is(err, ErrDuplicate) {
			t.Fatalf("errors.Is(err, ErrDuplicate) is false for %v", err)
		}
	}
	if p, _ := r.store.ByID(context.Background(), k.id()); p.UserUUID != alice {
		t.Fatal("the passkey changed hands")
	}
}

func TestALoginNamingAnotherUserHandleIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice, bob := uuid.NewString(), uuid.NewString()
	k := r.register(t, alice, "Laptop")
	r.register(t, bob, "Laptop")
	mustRefuse(t, r.login(t, k, flagUP|flagUV, userHandle(bob)), "another user handle")
	if len(r.sessions.logins) != 0 {
		t.Fatal("a session was started")
	}
}

func TestAnUnknownPasskeyIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	k := newKey(t)
	k.userHandle = userHandle(uuid.NewString())
	mustRefuse(t, r.login(t, k, flagUP|flagUV, k.userHandle), ErrNotFound.Error())
	ev := lastAudit(t, r.audit, auth_audit.LoginFailed)
	if ev.UserUUID != "" || ev.Detail["passkey_id"] != k.id() {
		t.Fatalf("failure audit %+v", ev)
	}
}

func TestAWrongOriginIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	r.auth.origin = "https://login.example.test.evil.test"
	mustRefuse(t, r.login(t, k, flagUP|flagUV, k.userHandle), "did not verify")
}

func TestACounterThatGoesBackIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	k.counter = 9
	if err := r.login(t, k, flagUP|flagUV, k.userHandle); err != nil { // signs with 10
		t.Fatal(err)
	}
	k.counter = 3
	mustRefuse(t, r.login(t, k, flagUP|flagUV, k.userHandle), "may have been copied") // signs with 4
	if len(r.sessions.logins) != 1 {
		t.Fatalf("%d sessions started", len(r.sessions.logins))
	}
}

func TestADisabledUserCannotLogIn(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	r.accounts.refused[alice] = errors.New("user is disabled")
	mustRefuse(t, r.login(t, k, flagUP|flagUV, k.userHandle), "user is disabled")
	if len(r.sessions.logins) != 0 {
		t.Fatal("a session was started")
	}
	if ev := lastAudit(t, r.audit, auth_audit.LoginFailed); !strings.Contains(ev.Reason, "user is disabled") {
		t.Fatalf("failure audit %+v", ev)
	}
}

func TestASessionStoreFailureIsReturnedWithItsCause(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop")
	r.sessions.fail = errors.New("session: login refused, the session store could not save the session: dial tcp 127.0.0.1:1: connection refused")
	err := r.login(t, k, flagUP|flagUV, k.userHandle)
	if err == nil || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("want the session store's error, got %v", err)
	}
}

func TestMalformedAndOversizedAnswersAreRefused(t *testing.T) {
	r := newRig(t, testConfig())
	_, _, err := r.rp.FinishLogin(context.Background(), "not-a-handle", strings.NewReader("{}"), Client{})
	mustRefuse(t, err, "handle is malformed")
	_, h, _ := r.rp.BeginLogin(context.Background())
	_, _, err = r.rp.FinishLogin(context.Background(), h, bytes.NewReader(bytes.Repeat([]byte("a"), MaxResponseBytes+1)), Client{})
	mustRefuse(t, err, "larger than")
	_, h, _ = r.rp.BeginLogin(context.Background())
	_, _, err = r.rp.FinishLogin(context.Background(), h, strings.NewReader(`{"id":"' OR 1=1 --"}`), Client{})
	mustRefuse(t, err, "not a WebAuthn assertion")
}

func TestAChallengeStoreFailureRefusesBeginWithTheCause(t *testing.T) {
	r := newRig(t, testConfig())
	r.chal.fail = errors.New("passkey challenge store on valkey 127.0.0.1:1: dial tcp 127.0.0.1:1: connect: connection refused")
	if _, _, err := r.rp.BeginLogin(context.Background()); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("want the real reason, got %v", err)
	}
}

func TestRemovingPasskeys(t *testing.T) {
	r := newRig(t, testConfig())
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	k1 := r.register(t, alice, "Laptop")
	k2 := r.register(t, alice, "Phone")
	kb := r.register(t, bob, "Laptop")

	// Another user's passkey answers as a missing one.
	if err := r.rp.Remove(ctx, alice, kb.id(), Client{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing bob's passkey as alice: %v", err)
	}
	if p, _ := r.store.ByID(ctx, kb.id()); p == nil {
		t.Fatal("bob's passkey is gone")
	}
	if err := r.rp.Remove(ctx, alice, "'; DELETE FROM passkeys; --", Client{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an injection payload as id: %v", err)
	}
	// One of two goes without asking about a password.
	if err := r.rp.Remove(ctx, alice, k1.id(), Client{}); err != nil {
		t.Fatal(err)
	}
	if r.accounts.asked != 0 {
		t.Fatal("asked about a password while another passkey remains")
	}
	// The last one stays while the user has no password, or the answer is unknown.
	if err := r.rp.Remove(ctx, alice, k2.id(), Client{}); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("want ErrLastWayIn, got %v", err)
	}
	r.accounts.passwordErr = errors.New("user store: connection reset by peer")
	if err := r.rp.Remove(ctx, alice, k2.id(), Client{}); err == nil || !strings.Contains(err.Error(), "connection reset by peer") {
		t.Fatalf("want the hook's error, got %v", err)
	}
	r.accounts.passwordErr = nil
	if p, _ := r.store.ByID(ctx, k2.id()); p == nil {
		t.Fatal("the last passkey was removed")
	}
	r.accounts.hasPassword[alice] = true
	if err := r.rp.Remove(ctx, alice, k2.id(), Client{}); err != nil {
		t.Fatalf("with a password the last passkey may go: %v", err)
	}
	var ok, failed int
	for _, e := range r.audit.OfKind(auth_audit.CredentialRemoved) {
		if e.Outcome == auth_audit.OutcomeOK {
			ok++
		} else {
			failed++
			if e.Reason == "" {
				t.Fatalf("refusal audited without its reason: %+v", e)
			}
		}
	}
	if ok != 2 || failed != 4 {
		t.Fatalf("removal audits: %d ok, %d failed; want 2 and 4", ok, failed)
	}
}

func TestARegistrationWhoseAuditFailsLeavesNoPasskey(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	opts, h, _ := r.rp.BeginRegistration(context.Background(), User{UUID: alice})
	resp, _ := r.auth.create(t, opts, nil, flagUP|flagUV, yes)
	r.audit.Fail = errors.New("audit store: disk full")
	_, err := r.rp.FinishRegistration(context.Background(), alice, h, "Key", bytes.NewReader(resp), Client{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("want the audit store's error, got %v", err)
	}
	if l, _ := r.store.ByUser(context.Background(), alice); len(l) != 0 {
		t.Fatal("an unaudited passkey was kept")
	}
}

func TestPasskeyNames(t *testing.T) {
	for _, bad := range []string{"", "   ", "tab\there", "nul\x00", strings.Repeat("x", MaxNameLength+1), "\xff"} {
		if _, err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q) accepted it", bad)
		}
	}
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	payload := `Robert'); DROP TABLE passkeys;-- <script>`
	r.register(t, alice, payload)
	l, _ := r.rp.List(context.Background(), alice)
	if len(l) != 1 || l[0].Name != payload {
		t.Fatalf("name not kept as given: %+v", l)
	}
}

func TestBadUserUUIDsAreRefused(t *testing.T) {
	r := newRig(t, testConfig())
	bad := []string{"", "1", "00000000-0000-0000-0000-000000000000", "' OR '1'='1", uuid.NewString() + "x"}
	for _, u := range bad {
		if _, _, err := r.rp.BeginRegistration(context.Background(), User{UUID: u}); err == nil {
			t.Errorf("BeginRegistration accepted user %q", u)
		}
		if _, err := r.rp.List(context.Background(), u); err == nil {
			t.Errorf("List accepted user %q", u)
		}
		if err := r.rp.Remove(context.Background(), u, "AAAA", Client{Address: "192.0.2.9"}); err == nil {
			t.Errorf("Remove accepted user %q", u)
		}
	}
	// A refused registration start and a refused removal are audited like the other refusals, with the reason.
	for _, kind := range []auth_audit.Kind{auth_audit.CredentialRegistered, auth_audit.CredentialRemoved} {
		ev := r.audit.OfKind(kind)
		if len(ev) != len(bad) {
			t.Fatalf("%s: %d audit records, want %d", kind, len(ev), len(bad))
		}
		for _, e := range ev {
			if e.Outcome != auth_audit.OutcomeFailed || e.UserUUID != "" || !strings.Contains(e.Reason, "is not a user uuid") {
				t.Fatalf("%s: %+v", kind, e)
			}
		}
	}
}

func TestConfigIsChecked(t *testing.T) {
	for i, c := range []Config{
		{RPDisplayName: "x", RPOrigins: []string{testOrigin}, KeyPrefix: "p:"},
		{RPID: testRPID, RPDisplayName: "x", RPOrigins: []string{testOrigin}, KeyPrefix: "bad prefix"},
		{RPID: testRPID, RPDisplayName: "x", RPOrigins: []string{testOrigin}, KeyPrefix: "p:", MaxPerUser: -1},
	} {
		if _, err := New(c, nopConn{}, NewMemoryStore(), &fakeSessions{}, newFakeAccounts(), &auth_audit.Memory{}); err == nil {
			t.Errorf("config %d accepted", i)
		}
	}
	if _, err := New(testConfig(), nil, NewMemoryStore(), &fakeSessions{}, newFakeAccounts(), &auth_audit.Memory{}); err == nil {
		t.Error("a nil Connection was accepted")
	}
}

// A device-bound passkey that the platform later syncs reports BS at its next login; List shows it from then on.
func TestALoginStoresTheNewBackupState(t *testing.T) {
	r := newRig(t, testConfig())
	ctx := context.Background()
	alice := uuid.NewString()
	opts, h, err := r.rp.BeginRegistration(ctx, User{UUID: alice})
	if err != nil {
		t.Fatal(err)
	}
	resp, k := r.auth.create(t, opts, nil, flagUP|flagUV|flagBE, yes)
	if _, err := r.rp.FinishRegistration(ctx, alice, h, "Phone", bytes.NewReader(resp), Client{}); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.rp.List(ctx, alice); len(l) != 1 || !l[0].BackupEligible || l[0].BackupState {
		t.Fatalf("after registration: %+v", l)
	}
	k.backup = flagBE | flagBS
	if err := r.login(t, k, flagUP|flagUV, k.userHandle); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.rp.List(ctx, alice); len(l) != 1 || !l[0].BackupState || l[0].LastUsedAt.IsZero() {
		t.Fatalf("after a login reporting BS: %+v", l)
	}
}

func TestABackupFlagThatChangesIsRefused(t *testing.T) {
	r := newRig(t, testConfig())
	alice := uuid.NewString()
	k := r.register(t, alice, "Laptop") // registered backup eligible
	k.backup = 0
	mustRefuse(t, r.login(t, k, flagUP|flagUV, k.userHandle), "Backup Eligible flag inconsistency")
}
