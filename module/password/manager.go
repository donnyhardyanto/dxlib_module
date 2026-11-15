package password

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// Manager sets, checks and changes passwords.
type Manager struct {
	cfg      Config
	store    Store
	transit  Transit
	breached BreachedList
	lockout  Lockout
	sessions session.Store
	accounts Accounts
	audit    auth_audit.Sink
	hasher   *hasher
	now      func() time.Time

	dummyOnce sync.Once
	dummyPHC  string
}

// New returns a Manager.
func New(cfg Config, deps Dependencies) (*Manager, error) {
	if err := deps.check(); err != nil {
		return nil, err
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: c, store: deps.Store, transit: deps.Transit, breached: deps.Breached, lockout: deps.Lockout,
		sessions: deps.Sessions, accounts: deps.Accounts, audit: deps.Audit, hasher: newHasher(c.MaxConcurrentHashes), now: time.Now}, nil
}

// Config returns the configuration with its defaults filled in.
func (m *Manager) Config() Config { return m.cfg }

// maxLoginNameBytes bounds the login name passed to Accounts.FindUser.
const maxLoginNameBytes = 320

// verified is a user whose password has just been checked.
type verified struct {
	userUUID   string
	rec        *Record
	normalized string
	outdated   bool
}

// detached gives a context for a step that must run even when the caller's context has ended (an audit record,
// a lockout count).
func (m *Manager) detached(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), m.cfg.OperationTimeout)
}

func (m *Manager) write(ctx context.Context, e auth_audit.Event) error {
	ctx, cancel := m.detached(ctx)
	defer cancel()
	e.Time = m.now()
	return m.audit.Write(ctx, e)
}

func (m *Manager) detail(client Client, step string) map[string]string {
	d := map[string]string{"auth_method": AuthMethod, "step": step}
	if client.Address != "" {
		d["client_address"] = truncate(client.Address, 128)
	}
	if client.UserAgent != "" {
		d["user_agent"] = truncate(client.UserAgent, 256)
	}
	return d
}

func copyDetail(d map[string]string) map[string]string {
	out := make(map[string]string, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

// refuseLogin writes LoginFailed with the real cause, feeds the lockout when feed is set (a wrong password), and
// returns an error wrapping ErrRefused and the cause.
func (m *Manager) refuseLogin(ctx context.Context, userUUID string, cause error, feed bool, client Client, d map[string]string) error {
	reason := cause.Error()
	err := fmt.Errorf("%w: %w", ErrRefused, cause)
	if feed && userUUID != "" {
		lctx, cancel := m.detached(ctx)
		ferr := m.lockout.Failed(lctx, userUUID, client)
		cancel()
		if ferr != nil {
			reason += "; recording the failure in the lockout failed too: " + ferr.Error()
			err = fmt.Errorf("%w (recording the failure in the lockout failed too: %v)", err, ferr)
		}
	}
	if aerr := m.write(ctx, auth_audit.Event{Kind: auth_audit.LoginFailed, UserUUID: userUUID, Outcome: auth_audit.OutcomeFailed, Reason: reason, Detail: d}); aerr != nil {
		err = fmt.Errorf("%w (and its audit record could not be written: %v)", err, aerr)
	}
	return err
}

// burn spends the time of one password check, so a login for an unknown user (or one with no password) takes as
// long as a wrong password.
func (m *Manager) burn(ctx context.Context, pw string) {
	m.dummyOnce.Do(func() {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		m.dummyPHC, _ = m.hasher.hash(context.Background(), string(b))
	})
	if m.dummyPHC != "" && len(pw) <= 4*HighestMaxLength {
		_, _, _ = m.hasher.verify(ctx, m.dummyPHC, normalize(pw))
	}
}

// findUser resolves a login name, refusing (and auditing) an unknown one.
func (m *Manager) findUser(ctx context.Context, login, pw string, client Client, d map[string]string) (string, error) {
	d["login"] = truncate(login, 64)
	if login == "" || len(login) > maxLoginNameBytes || !utf8.ValidString(login) {
		m.burn(ctx, pw)
		return "", m.refuseLogin(ctx, "", fmt.Errorf("the login name is empty, longer than %d bytes or not UTF-8", maxLoginNameBytes), false, client, d)
	}
	u, err := m.accounts.FindUser(ctx, login)
	if err != nil {
		m.burn(ctx, pw)
		if errors.Is(err, ErrUnknownUser) {
			return "", m.refuseLogin(ctx, "", fmt.Errorf("no user has the login name %q: %w", truncate(login, 64), err), false, client, d)
		}
		return "", m.refuseLogin(ctx, "", fmt.Errorf("looking up the login name %q: %w", truncate(login, 64), err), false, client, d)
	}
	c, err := canonicalUUID(u)
	if err != nil {
		m.burn(ctx, pw)
		return "", m.refuseLogin(ctx, "", fmt.Errorf("Accounts.FindUser answered for %q: %w", truncate(login, 64), err), false, client, d)
	}
	delete(d, "login")
	return c, nil
}

// authenticate checks the user's password. Every failure is audited as LoginFailed and returned wrapping ErrRefused;
// a wrong password is fed to the lockout as well.
func (m *Manager) authenticate(ctx context.Context, userUUID, pw string, client Client, d map[string]string) (*verified, error) {
	locked, err := m.lockout.Locked(ctx, userUUID)
	if err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("asking the lockout whether user %s is locked: %w", userUUID, err), false, client, d)
	}
	if locked {
		return nil, m.refuseLogin(ctx, userUUID, ErrLocked, false, client, d)
	}
	rec, err := m.store.Get(ctx, userUUID)
	if errors.Is(err, ErrNotFound) {
		m.burn(ctx, pw)
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("user %s: %w", userUUID, ErrNotFound), true, client, d)
	}
	if err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("reading the stored password of user %s: %w", userUUID, err), false, client, d)
	}
	if utf8.RuneCountInString(pw) > HighestMaxLength {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("the password given is longer than %d characters, so it cannot be the stored one", HighestMaxLength), true, client, d)
	}
	pt, err := m.transit.Decrypt(ctx, rec.Ciphertext)
	if err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("decrypting the stored password of user %s (key version %d): %w", userUUID, rec.KeyVersion, err), false, client, d)
	}
	phc, err := open(userUUID, pt)
	if err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("user %s: %w", userUUID, err), false, client, d)
	}
	n := normalize(pw)
	ok, outdated, err := m.hasher.verify(ctx, phc, n)
	if err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("checking the password of user %s: %w", userUUID, err), false, client, d)
	}
	if !ok {
		return nil, m.refuseLogin(ctx, userUUID, errors.New("the password is wrong"), true, client, d)
	}
	if err := m.lockout.Succeeded(ctx, userUUID); err != nil {
		return nil, m.refuseLogin(ctx, userUUID, fmt.Errorf("the password is right, but the lockout could not record the success: %w", err), false, client, d)
	}
	return &verified{userUUID: userUUID, rec: rec, normalized: n, outdated: outdated}, nil
}

// profileOf asks the service whether the verified user may log in, refusing (and auditing) when not. It runs before
// ErrMustChange is answered and before a change is stored, so a disabled account cannot change its password either.
func (m *Manager) profileOf(ctx context.Context, v *verified, client Client, d map[string]string) (session.Profile, error) {
	profile, err := m.accounts.LoginProfile(ctx, v.userUUID)
	if err != nil {
		return session.Profile{}, m.refuseLogin(ctx, v.userUUID, fmt.Errorf("the account may not log in: %w", err), false, client, d)
	}
	return profile, nil
}

// startSession starts the session of a verified user whose profile was read by profileOf.
func (m *Manager) startSession(ctx context.Context, v *verified, profile session.Profile, client Client) (string, *session.Session, error) {
	extra := map[string]string{}
	if v.outdated {
		if err := m.rehash(ctx, v); err != nil {
			extra["password_rehash_error"] = truncate(err.Error(), 300)
		} else {
			extra["password_rehashed"] = "true"
		}
	}
	token, sess, err := m.sessions.Create(ctx, session.LoginInfo{UserUUID: v.userUUID, AuthMethod: AuthMethod, Profile: profile,
		ClientAddress: client.Address, UserAgent: client.UserAgent, AuditDetail: extra})
	if err != nil {
		return "", nil, fmt.Errorf("password: the password of user %s was right, but the session could not be started: %w", v.userUUID, err)
	}
	return token, sess, nil
}

// rehash stores a fresh hash after a login whose stored hash used older Argon2id parameters.
func (m *Manager) rehash(ctx context.Context, v *verified) error {
	phc, err := m.hasher.hash(ctx, v.normalized)
	if err != nil {
		return err
	}
	ct, err := m.transit.Encrypt(ctx, seal(v.userUUID, phc))
	if err != nil {
		return fmt.Errorf("encrypting the new hash: %w", err)
	}
	r := *v.rec
	r.Ciphertext, r.KeyVersion = ct.Text, ct.KeyVersion
	return m.store.Swap(ctx, v.rec.Ciphertext, r)
}

// Login checks a user's password and starts their session. login is the name the user typed; Accounts.FindUser
// turns it into the user's uuid. An error wrapping ErrRefused is any refusal (audited as LoginFailed, a wrong
// password also counted by the lockout); ErrMustChange means the password is right but must be changed first, and
// no session is started.
func (m *Manager) Login(ctx context.Context, login, pw string, client Client) (token string, s *session.Session, err error) {
	d := m.detail(client, "login")
	user, err := m.findUser(ctx, login, pw, client, d)
	if err != nil {
		return "", nil, err
	}
	v, err := m.authenticate(ctx, user, pw, client, d)
	if err != nil {
		return "", nil, err
	}
	profile, err := m.profileOf(ctx, v, client, d)
	if err != nil {
		return "", nil, err
	}
	if v.rec.MustChange {
		err := fmt.Errorf("%w (user %s)", ErrMustChange, user)
		if aerr := m.write(ctx, auth_audit.Event{Kind: auth_audit.LoginFailed, UserUUID: user, Outcome: auth_audit.OutcomeFailed,
			Reason: "the password is right, but it was set by someone else and must be changed first (must_change_password)", Detail: d}); aerr != nil {
			err = fmt.Errorf("%w (and its audit record could not be written: %v)", err, aerr)
		}
		return "", nil, err
	}
	return m.startSession(ctx, v, profile, client)
}

// ChangeAndLogin is the change form shown after ErrMustChange (or any time the user is logged out): it checks the
// current password as Login does, stores the new one, ends every session of the user and starts a new one.
func (m *Manager) ChangeAndLogin(ctx context.Context, login, current, next string, client Client) (token string, s *session.Session, err error) {
	d := m.detail(client, "change_and_login")
	user, err := m.findUser(ctx, login, current, client, d)
	if err != nil {
		return "", nil, err
	}
	return m.changeVerified(ctx, user, current, next, client, d)
}

// Change replaces the password of a logged-in user who gives their current one. Every session of the user ends,
// the caller's included, and a new session is started for the caller: send its token as the new cookie.
func (m *Manager) Change(ctx context.Context, userUUID, current, next string, client Client) (token string, s *session.Session, err error) {
	d := m.detail(client, "change")
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return "", nil, m.refuseLogin(ctx, "", err, false, client, d)
	}
	return m.changeVerified(ctx, u, current, next, client, d)
}

func (m *Manager) changeVerified(ctx context.Context, user, current, next string, client Client, d map[string]string) (string, *session.Session, error) {
	v, err := m.authenticate(ctx, user, current, client, d)
	if err != nil {
		return "", nil, err
	}
	profile, err := m.profileOf(ctx, v, client, d)
	if err != nil {
		return "", nil, err
	}
	rec, err := m.replace(ctx, "", user, v, next, false, d)
	if err != nil {
		return "", nil, err
	}
	return m.startSession(ctx, &verified{userUUID: user, rec: rec}, profile, client)
}

// Set gives a user a new password chosen by someone else (actorUUID: an administrator; empty when the service sets
// a user's first password). With mustChange the user has to choose their own at the next login. Every session of
// the user ends.
func (m *Manager) Set(ctx context.Context, actorUUID, userUUID, next string, mustChange bool, client Client) error {
	d := m.detail(client, "set")
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return m.changeFailed(ctx, actorUUID, "", err, d)
	}
	if actorUUID != "" {
		a, err := canonicalUUID(actorUUID)
		if err != nil {
			return m.changeFailed(ctx, "", u, fmt.Errorf("actor: %w", err), d)
		}
		actorUUID = a
	}
	_, err = m.replace(ctx, actorUUID, u, nil, next, mustChange, d)
	return err
}

func (m *Manager) changeFailed(ctx context.Context, actorUUID, userUUID string, cause error, d map[string]string) error {
	err := cause
	if aerr := m.write(ctx, auth_audit.Event{Kind: auth_audit.CredentialChanged, UserUUID: userUUID, ActorUUID: actorUUID, Outcome: auth_audit.OutcomeFailed,
		Reason: cause.Error(), Detail: d}); aerr != nil {
		err = fmt.Errorf("%w (and its audit record could not be written: %v)", err, aerr)
	}
	return err
}

// replace checks the new password against the policy, stores it and ends every session of the user. current is the
// user's verified old password, or nil when someone else sets it.
func (m *Manager) replace(ctx context.Context, actorUUID, user string, current *verified, next string, mustChange bool, d map[string]string) (*Record, error) {
	d = copyDetail(d)
	n, err := m.checkPolicy(ctx, user, next)
	if err == nil && current != nil && n == current.normalized {
		err = fmt.Errorf("%w: %w", ErrPolicy, ErrSameAsCurrent)
	}
	if err != nil {
		return nil, m.changeFailed(ctx, actorUUID, user, err, d)
	}
	rec, err := m.store1(ctx, user, n, mustChange, current)
	if err != nil {
		return nil, m.changeFailed(ctx, actorUUID, user, err, d)
	}
	d["key_version"] = strconv.Itoa(rec.KeyVersion)
	d["must_change_password"] = strconv.FormatBool(mustChange)
	revoked, rerr := m.sessions.RevokeAll(ctx, user, "password changed")
	d["sessions_ended"] = strconv.Itoa(revoked)
	e := auth_audit.Event{Kind: auth_audit.CredentialChanged, UserUUID: user, ActorUUID: actorUUID, Outcome: auth_audit.OutcomeOK, Detail: d}
	if rerr != nil {
		e.Reason = "the password was changed, but ending the user's sessions failed: " + rerr.Error()
	}
	aerr := m.write(ctx, e)
	switch {
	case rerr != nil:
		return nil, fmt.Errorf("password: the password of user %s was changed, but their sessions could not be ended: %w", user, rerr)
	case aerr != nil:
		return nil, fmt.Errorf("password: the password of user %s was changed, but its audit record could not be written: %w", user, aerr)
	}
	return rec, nil
}

// store1 hashes, encrypts and stores a password that passed the policy. With current set it replaces exactly the
// verified record, so a change made meanwhile by someone else is not overwritten (ErrConflict).
func (m *Manager) store1(ctx context.Context, user, normalized string, mustChange bool, current *verified) (*Record, error) {
	phc, err := m.hasher.hash(ctx, normalized)
	if err != nil {
		return nil, err
	}
	ct, err := m.transit.Encrypt(ctx, seal(user, phc))
	if err != nil {
		return nil, fmt.Errorf("password: encrypting the new password of user %s: %w", user, err)
	}
	rec := Record{UserUUID: user, Ciphertext: ct.Text, KeyVersion: ct.KeyVersion, MustChange: mustChange, ChangedAt: m.now().UTC().Truncate(time.Microsecond)}
	if current != nil {
		err = m.store.Swap(ctx, current.rec.Ciphertext, rec)
	} else {
		err = m.store.Put(ctx, rec)
	}
	if err != nil {
		return nil, fmt.Errorf("password: storing the new password of user %s: %w", user, err)
	}
	return &rec, nil
}

// Has reports whether the user has a password. Wire it to passkey.Accounts.HasPassword.
func (m *Manager) Has(ctx context.Context, userUUID string) (bool, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return false, err
	}
	_, err = m.store.Get(ctx, u)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("password: reading whether user %s has a password: %w", u, err)
	}
	return true, nil
}

// MustChange reports whether the user's password has to be changed at the next login.
func (m *Manager) MustChange(ctx context.Context, userUUID string) (bool, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return false, err
	}
	r, err := m.store.Get(ctx, u)
	if err != nil {
		return false, err
	}
	return r.MustChange, nil
}

// Remove deletes the user's password (actorUUID: who asked, empty when it is the user). It returns ErrNotFound when
// the user has none, and ErrLastWayIn when they have no passkey to log in with instead. Every attempt is audited as
// CredentialRemoved.
func (m *Manager) Remove(ctx context.Context, actorUUID, userUUID string, client Client) error {
	d := m.detail(client, "remove")
	u, err := canonicalUUID(userUUID)
	fail := func(cause error) error {
		err := cause
		if aerr := m.write(ctx, auth_audit.Event{Kind: auth_audit.CredentialRemoved, UserUUID: u, ActorUUID: actorUUID, Outcome: auth_audit.OutcomeFailed,
			Reason: cause.Error(), Detail: d}); aerr != nil {
			err = fmt.Errorf("%w (and its audit record could not be written: %v)", err, aerr)
		}
		return err
	}
	if err != nil {
		return fail(err)
	}
	if actorUUID != "" {
		a, err := canonicalUUID(actorUUID)
		if err != nil {
			return fail(fmt.Errorf("actor: %w", err))
		}
		actorUUID = a
	}
	allow := func() error {
		has, err := m.accounts.HasPasskey(ctx, u)
		if err != nil {
			return fmt.Errorf("password: asking whether user %s has a passkey, before removing their password: %w", u, err)
		}
		if !has {
			return ErrLastWayIn
		}
		return nil
	}
	if err := m.store.Remove(ctx, u, allow); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrLastWayIn) {
			return fail(fmt.Errorf("user %s: %w", u, err))
		}
		return fail(fmt.Errorf("password: removing the password of user %s: %w", u, err))
	}
	if aerr := m.write(ctx, auth_audit.Event{Kind: auth_audit.CredentialRemoved, UserUUID: u, ActorUUID: actorUUID, Outcome: auth_audit.OutcomeOK, Detail: d}); aerr != nil {
		return fmt.Errorf("password: the password of user %s was removed, but its audit record could not be written: %w", u, aerr)
	}
	return nil
}

// RewrapResult counts what RewrapAll did.
type RewrapResult struct {
	LatestVersion int
	// Rewrapped rows now hold a ciphertext of LatestVersion.
	Rewrapped int
	// Changed rows were changed (or removed) by someone else while being rewrapped; they were left as they were.
	Changed int
}

// DefaultRewrapBatch is the batch size of RewrapAll when it is given 0.
const DefaultRewrapBatch = 100

// RewrapAll re-encrypts every stored password made with an older transit key version with the newest one, after
// the key was rotated. The hashes never leave the secret store. Run it from a job after each rotation; once it
// returns no error, the old key versions may be trimmed (min_decryption_version).
func (m *Manager) RewrapAll(ctx context.Context, batch int) (RewrapResult, error) {
	if batch <= 0 {
		batch = DefaultRewrapBatch
	}
	var res RewrapResult
	latest, err := m.transit.LatestVersion(ctx)
	if err != nil {
		return res, fmt.Errorf("password: reading the transit key's latest version: %w", err)
	}
	res.LatestVersion = latest
	for v := 1; v < latest; v++ {
		seen := map[string]bool{}
		for {
			rows, err := m.store.ByKeyVersion(ctx, v, batch)
			if err != nil {
				return res, fmt.Errorf("password: listing passwords of key version %d: %w", v, err)
			}
			if len(rows) == 0 {
				break
			}
			fresh := 0
			for _, r := range rows {
				if seen[r.UserUUID] {
					continue
				}
				seen[r.UserUUID] = true
				fresh++
				ct, err := m.transit.Rewrap(ctx, r.Ciphertext)
				if err != nil {
					return res, fmt.Errorf("password: rewrapping the password of user %s (key version %d): %w", r.UserUUID, v, err)
				}
				if ct.KeyVersion <= v {
					return res, fmt.Errorf("password: rewrapping the password of user %s gave key version %d, not newer than %d", r.UserUUID, ct.KeyVersion, v)
				}
				nr := r
				nr.Ciphertext, nr.KeyVersion = ct.Text, ct.KeyVersion
				switch err := m.store.Swap(ctx, r.Ciphertext, nr); {
				case err == nil:
					res.Rewrapped++
				case errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound):
					res.Changed++
				default:
					return res, fmt.Errorf("password: storing the rewrapped password of user %s: %w", r.UserUUID, err)
				}
			}
			if fresh == 0 {
				return res, fmt.Errorf("password: %d rows of key version %d stay at that version after being rewrapped (first: user %s)", len(rows), v, rows[0].UserUUID)
			}
		}
	}
	return res, nil
}
