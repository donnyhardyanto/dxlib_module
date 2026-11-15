package password

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// fakeAccounts knows users by login name.
type fakeAccounts struct {
	mu        sync.Mutex
	byLogin   map[string]string // login -> uuid
	names     map[string]string // uuid -> username
	passkeys  map[string]bool
	refused   map[string]error // uuid -> LoginProfile error
	findErr   error
	wordsErr  error
	passkeyEr error
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{byLogin: map[string]string{}, names: map[string]string{}, passkeys: map[string]bool{}, refused: map[string]error{}}
}

func (a *fakeAccounts) add(username string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	u := uuid.NewString()
	a.byLogin[username] = u
	a.names[u] = username
	return u
}

func (a *fakeAccounts) FindUser(_ context.Context, login string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.findErr != nil {
		return "", a.findErr
	}
	u, ok := a.byLogin[login]
	if !ok {
		return "", fmt.Errorf("fake accounts: %w", ErrUnknownUser)
	}
	return u, nil
}

func (a *fakeAccounts) LoginProfile(_ context.Context, userUUID string) (session.Profile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.refused[userUUID]; err != nil {
		return session.Profile{}, err
	}
	return session.Profile{Username: a.names[userUUID], DisplayName: a.names[userUUID], Status: "active", Version: 1}, nil
}

func (a *fakeAccounts) ContextWords(_ context.Context, userUUID string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wordsErr != nil {
		return nil, a.wordsErr
	}
	return []string{a.names[userUUID], "ab"}, nil
}

func (a *fakeAccounts) HasPasskey(_ context.Context, userUUID string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.passkeys[userUUID], a.passkeyEr
}

// fakeLockout counts failures and locks at max.
type fakeLockout struct {
	mu        sync.Mutex
	failures  map[string]int
	locked    map[string]bool
	unlocked  []string
	successes int
	max       int
	err       error
}

func newFakeLockout() *fakeLockout {
	return &fakeLockout{failures: map[string]int{}, locked: map[string]bool{}, max: 5}
}

func (l *fakeLockout) Locked(_ context.Context, u string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locked[u], l.err
}

func (l *fakeLockout) Failed(_ context.Context, u string, _ Client) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[u]++
	if l.failures[u] >= l.max {
		l.locked[u] = true
	}
	return nil
}

func (l *fakeLockout) Succeeded(context.Context, string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.successes++
	return nil
}

func (l *fakeLockout) Unlock(_ context.Context, u, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	delete(l.locked, u)
	l.failures[u] = 0
	l.unlocked = append(l.unlocked, u)
	return nil
}

func (l *fakeLockout) failuresOf(u string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failures[u]
}

// fakeSessions records the sessions started and revoked.
type fakeSessions struct {
	mu      sync.Mutex
	open    map[string]int // user -> open sessions
	logins  []session.LoginInfo
	revoked map[string]int
	n       int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{open: map[string]int{}, revoked: map[string]int{}}
}

var errNotInFake = errors.New("not used by module/password")

func (s *fakeSessions) Create(_ context.Context, l session.LoginInfo) (string, *session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	s.open[l.UserUUID]++
	s.logins = append(s.logins, l)
	return fmt.Sprintf("token-%d", s.n), &session.Session{ID: fmt.Sprintf("id-%d", s.n), UserUUID: l.UserUUID, AuthMethod: l.AuthMethod, Profile: l.Profile}, nil
}
func (s *fakeSessions) Get(context.Context, string) (*session.Session, error) {
	return nil, errNotInFake
}
func (s *fakeSessions) Rotate(context.Context, string, *session.Profile) (string, *session.Session, error) {
	return "", nil, errNotInFake
}
func (s *fakeSessions) Logout(context.Context, string) error { return errNotInFake }
func (s *fakeSessions) List(context.Context, string) ([]session.Session, error) {
	return nil, errNotInFake
}
func (s *fakeSessions) RevokeSession(context.Context, string, string) error { return errNotInFake }
func (s *fakeSessions) RevokeAll(_ context.Context, u, _ string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.open[u]
	s.open[u] = 0
	s.revoked[u] += n
	return n, nil
}
func (s *fakeSessions) PropagateProfile(context.Context, string, session.Profile) (session.PropagationResult, error) {
	return session.PropagationResult{}, errNotInFake
}
func (s *fakeSessions) InvalidateProfiles(context.Context, string) error { return errNotInFake }

func (s *fakeSessions) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logins)
}

// rig is a Manager on fakes.
type rig struct {
	m        *Manager
	store    Store
	transit  *InsecureTestTransit
	accounts *fakeAccounts
	lockout  *fakeLockout
	sessions *fakeSessions
	audit    *auth_audit.Memory
}

func newRig(t *testing.T, store Store, breached ...string) *rig {
	t.Helper()
	if store == nil {
		store = NewMemoryStore()
	}
	r := &rig{store: store, transit: NewInsecureTestTransit(), accounts: newFakeAccounts(), lockout: newFakeLockout(), sessions: newFakeSessions(), audit: &auth_audit.Memory{}}
	m, err := New(Config{}, Dependencies{Store: store, Transit: r.transit, Breached: NewHashListOf(breached...), Lockout: r.lockout,
		Sessions: r.sessions, Accounts: r.accounts, Audit: r.audit})
	if err != nil {
		t.Fatal(err)
	}
	r.m = m
	return r
}

// a password of the given length that no test list holds.
func pw(n int) string { return strings.Repeat("x", n-4) + "Q7#z" }

const goodPassword = "correct horse battery staple"
const otherPassword = "another fine passphrase here"
