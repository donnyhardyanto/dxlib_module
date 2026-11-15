package password

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
)

var bg = context.Background()

func TestNewHashUsesRFC9106SecondRecommendedParameters(t *testing.T) {
	h := newHasher(1)
	phc, err := h.hash(bg, goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash %q does not use m=64 MiB, t=3, p=4", phc)
	}
	p, err := decodePHC(phc)
	if err != nil || len(p.salt) != 16 || len(p.tag) != 32 {
		t.Fatalf("decoded %+v, %v: want a 16-byte salt and a 32-byte tag", p, err)
	}
	ok, outdated, err := h.verify(bg, phc, goodPassword)
	if !ok || outdated || err != nil {
		t.Fatalf("verify right password: ok=%v outdated=%v err=%v", ok, outdated, err)
	}
	if ok, _, _ := h.verify(bg, phc, goodPassword+"!"); ok {
		t.Fatal("a wrong password verified")
	}
	again, _ := h.hash(bg, goodPassword)
	if again == phc {
		t.Fatal("two hashes of one password are equal: the salt is not random")
	}
}

func TestStoredHashParametersAreBounded(t *testing.T) {
	for _, s := range []string{
		"$argon2id$v=19$m=4194304,t=3,p=4$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn", // 4 GiB
		"$argon2id$v=19$m=65536,t=1000,p=4$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn",
		"$argon2id$v=19$m=65536,t=3,p=255$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn",
		"$argon2id$v=16$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn",
		"$argon2id$v=19$m=65536,t=3,p=4,x=1$c2FsdHNhbHRzYWx0$dGFndGFndGFndGFndGFndGFndGFndGFndGFn",
		"plain text",
	} {
		if _, err := decodePHC(s); !errors.Is(err, errPHC) {
			t.Errorf("decodePHC(%q) = %v, want errPHC", s, err)
		}
	}
}

func TestPolicy(t *testing.T) {
	r := newRig(t, nil, "this is a breached passphrase", "fifteen chars ok?")
	alice := r.accounts.add("alice_wonder")
	cases := []struct {
		name string
		pw   string
		want error
	}{
		{"14 characters", pw(14), ErrTooShort},
		{"15 characters", pw(15), nil},
		{"15 characters of 2-byte runes", strings.Repeat("é", 15), nil},
		{"14 characters with combining marks that NFKC composes", strings.Repeat("é", 14), ErrTooShort},
		{"129 characters", pw(129), ErrTooLong},
		{"128 characters", pw(128), nil},
		{"holds the username", "my name is ALICE_WONDER ok", ErrContextWord},
		{"a short context word is ignored", "ab ab ab ab ab ab ab ab", nil},
		{"breached as typed", "this is a breached passphrase", ErrBreached},
		{"breached as typed, plain", "fifteen chars ok?", ErrBreached},
		{"breached once NFKC turns the ligature into f i", "ﬁfteen chars ok?", ErrBreached},
		{"not UTF-8", "\xff\xfe" + pw(20), ErrNotUTF8},
	}
	for _, c := range cases {
		err := r.m.CheckPolicy(bg, alice, c.pw)
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: refused: %v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrPolicy) || !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want ErrPolicy and %v", c.name, err, c.want)
		}
	}
}

func TestABreachedListThatCannotAnswerRefusesTheChangeButNotAsPolicy(t *testing.T) {
	r := newRig(t, nil)
	r.m.breached = failingList{errors.New("range service unreachable: dial tcp 192.0.2.1:443: i/o timeout")}
	alice := r.accounts.add("alice")
	err := r.m.Set(bg, "", alice, goodPassword, false, Client{})
	if err == nil || errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("got %v, want a non-policy error naming the cause", err)
	}
	if ok, _ := r.m.Has(bg, alice); ok {
		t.Fatal("the password was stored although the breached check failed")
	}
	if ev := r.audit.OfKind(auth_audit.CredentialChanged); len(ev) != 1 || ev[0].Outcome != auth_audit.OutcomeFailed || !strings.Contains(ev[0].Reason, "i/o timeout") {
		t.Fatalf("audit %+v, want one failed CredentialChanged naming the cause", ev)
	}
}

type failingList struct{ err error }

func (f failingList) Breached(context.Context, string) (bool, error) { return false, f.err }

func TestSetThenLoginStartsAPasswordSession(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	rec, err := r.store.Get(bg, alice)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Ciphertext, "argon2") || rec.KeyVersion != 1 || rec.MustChange {
		t.Fatalf("stored %+v: want an opaque transit ciphertext of key version 1, must_change false", rec)
	}
	token, s, err := r.m.Login(bg, "alice", goodPassword, Client{Address: "198.51.100.7", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || s.UserUUID != alice || s.AuthMethod != AuthMethod || s.Profile.Username != "alice" {
		t.Fatalf("session %+v token %q", s, token)
	}
	if r.lockout.successes != 1 {
		t.Fatalf("lockout successes %d, want 1", r.lockout.successes)
	}
	if ev := r.audit.OfKind(auth_audit.CredentialChanged); len(ev) != 1 || ev[0].Outcome != auth_audit.OutcomeOK || ev[0].UserUUID != alice {
		t.Fatalf("audit %+v, want one CredentialChanged ok", ev)
	}
	for _, e := range r.audit.Events() {
		for k, v := range e.Detail {
			if strings.Contains(v, goodPassword) || strings.Contains(e.Reason, goodPassword) {
				t.Fatalf("audit record %+v holds the password in %s", e, k)
			}
		}
	}
}

func TestAWrongPasswordIsAuditedAndCountedUntilTheLockoutLocks(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, _, err := r.m.Login(bg, "alice", otherPassword, Client{Address: "203.0.113.9"})
		if !errors.Is(err, ErrRefused) || errors.Is(err, ErrLocked) {
			t.Fatalf("attempt %d: %v, want ErrRefused (not locked yet)", i, err)
		}
	}
	if r.lockout.failuresOf(alice) != 5 {
		t.Fatalf("lockout counted %d failures, want 5", r.lockout.failuresOf(alice))
	}
	_, _, err := r.m.Login(bg, "alice", goodPassword, Client{})
	if !errors.Is(err, ErrRefused) || !errors.Is(err, ErrLocked) {
		t.Fatalf("right password while locked: %v, want ErrRefused wrapping ErrLocked", err)
	}
	failed := r.audit.OfKind(auth_audit.LoginFailed)
	if len(failed) != 6 || failed[0].UserUUID != alice || failed[0].Reason != "the password is wrong" || failed[0].Detail["client_address"] != "203.0.113.9" {
		t.Fatalf("LoginFailed records %+v", failed)
	}
	if !strings.Contains(failed[5].Reason, "locked") || r.lockout.failuresOf(alice) != 5 {
		t.Fatalf("a locked login must be audited and not counted again: %+v, failures %d", failed[5], r.lockout.failuresOf(alice))
	}
	if r.sessions.loginCount() != 0 {
		t.Fatal("a session was started")
	}
}

func TestALockoutThatCannotAnswerRefusesTheLogin(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	r.lockout.err = errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")
	_, _, err := r.m.Login(bg, "alice", goodPassword, Client{})
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("got %v, want ErrRefused naming the lockout's error", err)
	}
	if r.sessions.loginCount() != 0 {
		t.Fatal("a session was started without the lockout")
	}
}

func TestAnUnknownUserIsRefusedAuditedAndTakesAHash(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err := r.m.Login(bg, "mallory'; DROP TABLE users;--", goodPassword, Client{})
	took := time.Since(start)
	if !errors.Is(err, ErrRefused) || !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("got %v, want ErrRefused wrapping ErrUnknownUser", err)
	}
	if r.m.dummyPHC == "" {
		t.Fatal("no dummy hash was computed for the unknown user")
	}
	start = time.Now()
	_, _, _ = r.m.Login(bg, "alice", otherPassword, Client{})
	wrong := time.Since(start)
	if took < wrong/3 {
		t.Errorf("an unknown user took %s, a wrong password %s: the unknown user must cost a hash too", took, wrong)
	}
	ev := r.audit.OfKind(auth_audit.LoginFailed)
	if len(ev) != 2 || ev[0].UserUUID != "" || ev[0].Detail["login"] != "mallory'; DROP TABLE users;--" {
		t.Fatalf("audit %+v", ev)
	}
}

func TestAUserWithoutAPasswordIsRefused(t *testing.T) {
	r := newRig(t, nil)
	r.accounts.add("bob")
	_, _, err := r.m.Login(bg, "bob", goodPassword, Client{})
	if !errors.Is(err, ErrRefused) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrRefused wrapping ErrNotFound", err)
	}
}

func TestARefusedAccountGetsNoSession(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	disabled := errors.New("user is disabled")
	r.accounts.refused[alice] = disabled
	_, _, err := r.m.Login(bg, "alice", goodPassword, Client{})
	if !errors.Is(err, ErrRefused) || !errors.Is(err, disabled) || r.sessions.loginCount() != 0 {
		t.Fatalf("got %v, sessions %d", err, r.sessions.loginCount())
	}
	if r.lockout.failuresOf(alice) != 0 {
		t.Fatal("a refused account with the right password was counted as a failed guess")
	}
}

func TestAHashCopiedIntoAnotherUsersRowDoesNotLogIn(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	owner := r.accounts.add("owner")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Set(bg, "", owner, otherPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	stolen, _ := r.store.Get(bg, alice)
	forged := *stolen
	forged.UserUUID = owner
	if err := r.store.Put(bg, forged); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.m.Login(bg, "owner", goodPassword, Client{})
	if !errors.Is(err, ErrRefused) || !errors.Is(err, errOtherUser) {
		t.Fatalf("got %v, want a refusal naming the copied row", err)
	}
	if r.sessions.loginCount() != 0 {
		t.Fatal("a session was started")
	}
}

func TestMustChangeGivesNoSessionUntilTheUserChoosesAPassword(t *testing.T) {
	r := newRig(t, nil)
	admin := r.accounts.add("admin")
	alice := r.accounts.add("alice")
	r.sessions.open[alice] = 2
	if err := r.m.Set(bg, admin, alice, goodPassword, true, Client{}); err != nil {
		t.Fatal(err)
	}
	if r.sessions.revoked[alice] != 2 {
		t.Fatalf("Set ended %d sessions, want 2", r.sessions.revoked[alice])
	}
	if ev := r.audit.OfKind(auth_audit.CredentialChanged); len(ev) != 1 || ev[0].ActorUUID != admin || ev[0].Detail["must_change_password"] != "true" {
		t.Fatalf("audit %+v", ev)
	}
	_, _, err := r.m.Login(bg, "alice", goodPassword, Client{})
	if !errors.Is(err, ErrMustChange) || errors.Is(err, ErrRefused) || r.sessions.loginCount() != 0 {
		t.Fatalf("got %v with %d sessions, want ErrMustChange and none", err, r.sessions.loginCount())
	}
	if _, _, err := r.m.ChangeAndLogin(bg, "alice", goodPassword, goodPassword, Client{}); !errors.Is(err, ErrSameAsCurrent) {
		t.Fatalf("same password: %v, want ErrSameAsCurrent", err)
	}
	if _, _, err := r.m.ChangeAndLogin(bg, "alice", "not the current one at all", otherPassword, Client{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("wrong current password: %v, want ErrRefused", err)
	}
	if r.lockout.failuresOf(alice) != 1 {
		t.Fatalf("a wrong current password in the change form was counted %d times, want 1", r.lockout.failuresOf(alice))
	}
	token, s, err := r.m.ChangeAndLogin(bg, "alice", goodPassword, otherPassword, Client{})
	if err != nil || token == "" || s.UserUUID != alice {
		t.Fatalf("change: %v", err)
	}
	if must, _ := r.m.MustChange(bg, alice); must {
		t.Fatal("must_change_password is still set")
	}
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("old password: %v", err)
	}
	if _, _, err := r.m.Login(bg, "alice", otherPassword, Client{}); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestChangeEndsEverySessionAndStartsANewOne(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); err != nil {
			t.Fatal(err)
		}
	}
	token, _, err := r.m.Change(bg, alice, goodPassword, otherPassword, Client{})
	if err != nil || token == "" {
		t.Fatal(err)
	}
	if r.sessions.revoked[alice] != 3 || r.sessions.open[alice] != 1 {
		t.Fatalf("revoked %d open %d, want 3 and 1", r.sessions.revoked[alice], r.sessions.open[alice])
	}
	if _, _, err := r.m.Change(bg, "not-a-uuid", goodPassword, otherPassword, Client{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("malformed uuid: %v", err)
	}
}

func TestAChangeDoesNotOverwriteAPasswordSetMeanwhile(t *testing.T) {
	store := &racingStore{MemoryStore: NewMemoryStore()}
	r := newRig(t, store)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	store.beforeSwap = func() { _ = r.m.Set(bg, "", alice, "an administrator chose this one", true, Client{}) }
	_, _, err := r.m.Change(bg, alice, goodPassword, otherPassword, Client{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if must, _ := r.m.MustChange(bg, alice); !must {
		t.Fatal("the administrator's password was overwritten")
	}
}

// racingStore runs beforeSwap once, just before the first Swap.
type racingStore struct {
	*MemoryStore
	beforeSwap func()
}

func (s *racingStore) Swap(ctx context.Context, old string, r Record) error {
	if f := s.beforeSwap; f != nil {
		s.beforeSwap = nil
		f()
	}
	return s.MemoryStore.Swap(ctx, old, r)
}

func TestAnOutdatedHashIsReplacedAtLogin(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	old := encodePHC(argonParams{memory: 8 * 1024, passes: 1, lanes: 1, salt: []byte("0123456789abcdef"), version: 0x13,
		tag: argonTag(goodPassword, []byte("0123456789abcdef"), 1, 8*1024, 1)})
	ct, _ := r.transit.Encrypt(bg, seal(alice, old))
	if err := r.store.Put(bg, Record{UserUUID: alice, Ciphertext: ct.Text, KeyVersion: ct.KeyVersion, ChangedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); err != nil {
		t.Fatal(err)
	}
	rec, _ := r.store.Get(bg, alice)
	pt, _ := r.transit.Decrypt(bg, rec.Ciphertext)
	phc, _ := open(alice, pt)
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("after login the hash is %q, want the current parameters", phc)
	}
	if r.sessions.logins[0].AuditDetail["password_rehashed"] != "true" {
		t.Fatalf("session audit detail %v", r.sessions.logins[0].AuditDetail)
	}
}

func TestRewrapAllMovesEveryRowToTheNewestKeyVersion(t *testing.T) {
	r := newRig(t, nil)
	var users []string
	for _, name := range []string{"u1", "u2", "u3", "u4", "u5"} {
		u := r.accounts.add(name)
		users = append(users, u)
		if err := r.m.Set(bg, "", u, goodPassword, false, Client{}); err != nil {
			t.Fatal(err)
		}
		if name == "u2" || name == "u4" {
			r.transit.Rotate()
		}
	}
	res, err := r.m.RewrapAll(bg, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.LatestVersion != 3 || res.Rewrapped != 4 || res.Changed != 0 {
		t.Fatalf("result %+v, want latest 3, 4 rewrapped (u1, u2 at v1; u3, u4 at v2; u5 at v3)", res)
	}
	r.transit.MinDecryptionVersion = 3
	for i, u := range users {
		rec, _ := r.store.Get(bg, u)
		if rec.KeyVersion != 3 {
			t.Fatalf("user %d at key version %d", i, rec.KeyVersion)
		}
		if _, _, err := r.m.Login(bg, r.accounts.names[u], goodPassword, Client{}); err != nil {
			t.Fatalf("user %d cannot log in after the rewrap: %v", i, err)
		}
	}
	if res, err := r.m.RewrapAll(bg, 2); err != nil || res.Rewrapped != 0 {
		t.Fatalf("second run %+v %v, want nothing to do", res, err)
	}
}

func TestRewrapAllLeavesARowChangedMeanwhile(t *testing.T) {
	store := &racingStore{MemoryStore: NewMemoryStore()}
	r := newRig(t, store)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	r.transit.Rotate()
	store.beforeSwap = func() { _ = r.m.Set(bg, "", alice, otherPassword, false, Client{}) }
	res, err := r.m.RewrapAll(bg, 10)
	if err != nil || res.Changed != 1 || res.Rewrapped != 0 {
		t.Fatalf("%+v %v, want one changed row", res, err)
	}
	if _, _, err := r.m.Login(bg, "alice", otherPassword, Client{}); err != nil {
		t.Fatalf("the change made meanwhile was lost: %v", err)
	}
}

func TestRemoveRefusesTheLastWayIn(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Remove(bg, "", alice, Client{}); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("got %v, want ErrLastWayIn", err)
	}
	if ok, _ := r.m.Has(bg, alice); !ok {
		t.Fatal("the password was removed")
	}
	r.accounts.passkeys[alice] = true
	if err := r.m.Remove(bg, "", alice, Client{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.m.Has(bg, alice); ok {
		t.Fatal("the password is still there")
	}
	if err := r.m.Remove(bg, "", alice, Client{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	if err := r.m.Remove(bg, "", "../../etc", Client{}); err == nil {
		t.Fatal("a malformed uuid was accepted")
	}
	ev := r.audit.OfKind(auth_audit.CredentialRemoved)
	if len(ev) != 4 || ev[0].Outcome != auth_audit.OutcomeFailed || ev[1].Outcome != auth_audit.OutcomeOK || ev[3].Outcome != auth_audit.OutcomeFailed {
		t.Fatalf("CredentialRemoved records %+v, want failed, ok, failed, failed", ev)
	}
}

func TestResetOwnerCommand(t *testing.T) {
	r := newRig(t, nil)
	owner := r.accounts.add("owner")
	if err := r.m.Set(bg, "", owner, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	r.lockout.locked[owner] = true
	r.sessions.open[owner] = 2
	findOwner := func(context.Context) (string, error) { return owner, nil }

	var out, errOut bytes.Buffer
	if code := RunResetOwner(bg, r.m, findOwner, nil, &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatalf("without --confirm: status %d, stdout %q", code, out.String())
	}
	if _, _, err := r.m.Login(bg, "owner", goodPassword, Client{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("without --confirm something changed: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if code := RunResetOwner(bg, r.m, findOwner, []string{"--confirm"}, &out, &errOut); code != 0 {
		t.Fatalf("status %d: %s", code, errOut.String())
	}
	temp := strings.TrimSuffix(out.String(), "\n")
	if len(temp) != TemporaryLength || strings.ContainsAny(temp, "0O1lI \n") {
		t.Fatalf("stdout %q: want one line holding only a %d-character temporary password", out.String(), TemporaryLength)
	}
	if strings.Contains(errOut.String(), temp) {
		t.Fatal("the temporary password was also written to stderr")
	}
	if len(r.lockout.unlocked) != 1 || r.sessions.revoked[owner] != 2 {
		t.Fatalf("unlocked %v, sessions ended %d", r.lockout.unlocked, r.sessions.revoked[owner])
	}
	ev := r.audit.OfKind(auth_audit.CredentialChanged)
	last := ev[len(ev)-1]
	if last.UserUUID != owner || last.Detail["actor"] != "host command reset-owner" || last.Detail["sessions_ended"] != "2" {
		t.Fatalf("audit %+v", last)
	}
	if _, _, err := r.m.Login(bg, "owner", temp, Client{}); !errors.Is(err, ErrMustChange) {
		t.Fatalf("login with the temporary password: %v, want ErrMustChange", err)
	}
	if _, _, err := r.m.ChangeAndLogin(bg, "owner", temp, otherPassword, Client{}); err != nil {
		t.Fatal(err)
	}

	if code := RunResetOwner(bg, r.m, findOwner, []string{"--confirm", "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("an extra argument: status %d", code)
	}
	r.lockout.err = errors.New("redis down")
	out.Reset()
	if code := RunResetOwner(bg, r.m, findOwner, []string{"--confirm"}, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Fatalf("a lockout that cannot unlock: status %d, stdout %q; want 1 and nothing printed", code, out.String())
	}
}

func TestHashSlotsHonourTheContext(t *testing.T) {
	h := newHasher(1)
	if err := h.acquire(bg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, 20*time.Millisecond)
	defer cancel()
	_, err := h.hash(ctx, goodPassword)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "Argon2id slot") {
		t.Fatalf("got %v, want a slot wait that names the deadline", err)
	}
	h.release()
}

func TestAnAuditStoreThatFailsIsNamed(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	r.audit.Fail = errors.New("audit database read-only")
	_, _, err := r.m.Login(bg, "alice", otherPassword, Client{})
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "audit database read-only") {
		t.Fatalf("got %v", err)
	}
	err = r.m.Set(bg, "", alice, "a third long passphrase", false, Client{})
	if err == nil || !strings.Contains(err.Error(), "audit record could not be written") {
		t.Fatalf("got %v", err)
	}
}

func TestConfigAndDependencies(t *testing.T) {
	if _, err := New(Config{MinLength: 12}, Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	r := newRig(t, nil)
	deps := Dependencies{Store: r.store, Transit: r.transit, Breached: NewHashListOf(), Lockout: r.lockout, Sessions: r.sessions, Accounts: r.accounts, Audit: r.audit}
	if _, err := New(Config{MinLength: 14}, deps); err == nil {
		t.Fatal("a minimum below 15 was accepted")
	}
	if _, err := New(Config{MaxLength: 2000}, deps); err == nil {
		t.Fatal("a maximum above 1024 was accepted")
	}
	m, err := New(Config{}, deps)
	if err != nil || m.Config().MinLength != 15 || m.Config().MaxLength != 128 || m.Config().MaxConcurrentHashes != 4 {
		t.Fatalf("defaults %+v %v", m.Config(), err)
	}
}

func TestADisabledAccountCannotChangeAForcedPassword(t *testing.T) {
	r := newRig(t, nil)
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, true, Client{}); err != nil {
		t.Fatal(err)
	}
	disabled := errors.New("user is disabled")
	r.accounts.refused[alice] = disabled
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); !errors.Is(err, ErrRefused) || errors.Is(err, ErrMustChange) {
		t.Fatalf("login: %v, want ErrRefused, not ErrMustChange", err)
	}
	before, _ := r.store.Get(bg, alice)
	if _, _, err := r.m.ChangeAndLogin(bg, "alice", goodPassword, otherPassword, Client{}); !errors.Is(err, disabled) {
		t.Fatalf("change: %v, want the account's refusal", err)
	}
	if after, _ := r.store.Get(bg, alice); after.Ciphertext != before.Ciphertext || !after.MustChange {
		t.Fatal("a disabled account changed its password")
	}
}
