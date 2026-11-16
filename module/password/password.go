// Package password keeps users' passwords and logs users in with them.
//
// A password is hashed with Argon2id (64 MiB, 3 passes, 4 lanes: RFC 9106's second recommended set) under a random
// salt. The hash is then encrypted by a Transit key: OpenBaoTransit uses an OpenBao (or Vault) transit key of type
// aes256-gcm96 that cannot be exported, so a copy of the table alone is useless without the secret store. The
// key version that encrypted a row is stored beside it; after the key is rotated, RewrapAll re-encrypts the rows to
// the newest version without ever seeing a hash. InsecureTestTransit is an in-memory key ring for tests only.
//
// Each encrypted value carries the uuid of the user it belongs to, and a value read from another user's row is
// refused, so a hash copied from one row into another (say into the owner's) does not log anyone in.
//
// A new password has 15 to 128 characters (Unicode NFKC, counted as code points), holds none of the user's context
// words (username, e-mail, name: Accounts.ContextWords) and is not in a BreachedList (HashList for a local list,
// RangeAPI for a k-anonymity range service such as Pwned Passwords).
//
// A login checks Lockout first and is refused while the account is locked or when the lockout store cannot answer
// (fail closed). Every failure is written to the auth_audit.Sink as LoginFailed and fed to Lockout. A user whose
// password was set by someone else (Set with mustChange, ResetOwner) gets ErrMustChange and no session until they
// choose a new one with ChangeAndLogin. A successful login starts a session through session.Store with the auth
// method "password". A user with no other way in (no passkey, asked through Accounts) cannot remove their password.
//
// ResetOwner and RunResetOwner give a service the reset-owner host command: run on the server by the operator, it
// sets a random temporary password for the owner account, prints it once and forces a change at the next login.
package password

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

// AuthMethod is the session's auth method after a password login.
const AuthMethod = "password"

// Accounts is the service's side of a password login.
type Accounts interface {
	// FindUser resolves the name typed at login (a username, an e-mail) to the user's uuid. Return an error wrapping
	// ErrUnknownUser when there is no such user; any other error refuses the login as a store failure. Its text
	// goes into the audit record.
	FindUser(ctx context.Context, login string) (userUUID string, err error)
	// LoginProfile returns the profile a password login starts the session with, read from the authoritative
	// store. An error refuses the login: return one when the user is disabled, deleted or locked by the service,
	// or the store failed.
	LoginProfile(ctx context.Context, userUUID string) (session.Profile, error)
	// ContextWords returns words a new password of this user may not contain: the username, the e-mail's local
	// part, the display name's words. Words shorter than four characters are ignored.
	ContextWords(ctx context.Context, userUUID string) ([]string, error)
	// HasPasskey reports whether the user can still log in without a password. Asked before a password is
	// removed; an error refuses the removal.
	HasPasskey(ctx context.Context, userUUID string) (bool, error)
}

// Lockout counts failed logins and locks an account after too many. The accountlockout subpackage adapts
// module/account_lockout to it.
type Lockout interface {
	// Locked reports whether the user may not log in now. An error refuses the login (fail closed).
	Locked(ctx context.Context, userUUID string) (bool, error)
	// Failed records a failed login of the user.
	Failed(ctx context.Context, userUUID string, client Client) error
	// Succeeded records a successful login (it resets the failure count).
	Succeeded(ctx context.Context, userUUID string) error
	// Unlock ends a lock, as the reset-owner command does. reason goes to the lockout's own record.
	Unlock(ctx context.Context, userUUID, reason string) error
}

// Client describes the caller, for the audit record and the lockout only.
type Client struct {
	Address   string
	UserAgent string
}

var (
	// ErrRefused wraps every login or password change that did not pass: an unknown user, a wrong password, a
	// locked account, a refused account, a store that could not answer. Answer it as 401 without the detail; the
	// error's text holds the real reason for the log, and the audit record has it too. Test for ErrRefused before
	// any other error: it wraps its cause, so errors.Is may also find ErrLocked or the service's own error.
	ErrRefused = errors.New("password: login not accepted")
	// ErrLocked: the lockout says the account is locked. Always wrapped in ErrRefused.
	ErrLocked = errors.New("password: the account is locked after too many failed logins")
	// ErrMustChange: the password is right, but it was set by someone else and must be changed first
	// (must_change_password). No session is started: send the user to the change form (ChangeAndLogin).
	ErrMustChange = errors.New("password: the password must be changed before logging in")
	// ErrUnknownUser is what Accounts.FindUser wraps when no user has the login name.
	ErrUnknownUser = errors.New("password: no such user")
	// ErrNotFound: the user has no password.
	ErrNotFound = errors.New("password: the user has no password")
	// ErrLastWayIn: the password is the user's last way in (no passkey).
	ErrLastWayIn = errors.New("password: it is the user's last way in (no passkey)")
	// ErrConflict: the stored password changed while it was being replaced; read it again.
	ErrConflict = errors.New("password: the stored password changed meanwhile")
	// ErrPolicy wraps every new password the policy refuses, together with one of ErrTooShort, ErrTooLong,
	// ErrBreached, ErrContextWord, ErrSameAsCurrent or ErrNotUTF8. Its text is safe to show the user.
	ErrPolicy        = errors.New("password: the new password is not allowed")
	ErrTooShort      = errors.New("it is shorter than the minimum length")
	ErrTooLong       = errors.New("it is longer than the maximum length")
	ErrBreached      = errors.New("it appears in a list of breached passwords")
	ErrContextWord   = errors.New("it contains the user's name or another word of the account")
	ErrSameAsCurrent = errors.New("it is the same as the current password")
	ErrNotUTF8       = errors.New("it is not valid UTF-8 text")
)

// Config is set by the service.
type Config struct {
	// MinLength is the shortest password allowed, in characters. Default and lowest 15.
	MinLength int
	// MaxLength is the longest password allowed, in characters. Default 128, at most 1024.
	MaxLength int
	// MaxConcurrentHashes bounds the Argon2id computations running at once; each holds 64 MiB. Default 4. A
	// login waits for a free slot until its context ends.
	MaxConcurrentHashes int
	// OperationTimeout bounds the steps that run after the caller's context has ended (the audit record of a
	// failure whose context was cancelled). Default 5 seconds.
	OperationTimeout time.Duration
}

const (
	DefaultMinLength           = 15
	LowestMinLength            = 15
	DefaultMaxLength           = 128
	HighestMaxLength           = 1024
	DefaultMaxConcurrentHashes = 4
	DefaultOperationTimeout    = 5 * time.Second
	// MinContextWordLength: shorter context words are not checked.
	MinContextWordLength = 4
)

func (c Config) withDefaults() (Config, error) {
	if c.MinLength == 0 {
		c.MinLength = DefaultMinLength
	}
	if c.MinLength < LowestMinLength {
		return c, fmt.Errorf("password: Config.MinLength %d is below the lowest allowed, %d", c.MinLength, LowestMinLength)
	}
	if c.MaxLength == 0 {
		c.MaxLength = DefaultMaxLength
	}
	if c.MaxLength < c.MinLength || c.MaxLength > HighestMaxLength {
		return c, fmt.Errorf("password: Config.MaxLength %d must be between MinLength %d and %d", c.MaxLength, c.MinLength, HighestMaxLength)
	}
	if c.MaxConcurrentHashes == 0 {
		c.MaxConcurrentHashes = DefaultMaxConcurrentHashes
	}
	if c.MaxConcurrentHashes < 1 {
		return c, fmt.Errorf("password: Config.MaxConcurrentHashes %d must be at least 1", c.MaxConcurrentHashes)
	}
	if c.OperationTimeout == 0 {
		c.OperationTimeout = DefaultOperationTimeout
	}
	return c, nil
}

// Dependencies are the parts a Manager works with. All are required.
type Dependencies struct {
	Store    Store
	Transit  Transit
	Breached BreachedList
	Lockout  Lockout
	Sessions session.Store
	Accounts Accounts
	Audit    auth_audit.Sink
}

func (d Dependencies) check() error {
	var missing []string
	if d.Store == nil {
		missing = append(missing, "Store")
	}
	if d.Transit == nil {
		missing = append(missing, "Transit")
	}
	if d.Breached == nil {
		missing = append(missing, "Breached")
	}
	if d.Lockout == nil {
		missing = append(missing, "Lockout")
	}
	if d.Sessions == nil {
		missing = append(missing, "Sessions")
	}
	if d.Accounts == nil {
		missing = append(missing, "Accounts")
	}
	if d.Audit == nil {
		missing = append(missing, "Audit")
	}
	if len(missing) > 0 {
		return fmt.Errorf("password: New is missing Dependencies.%s", strings.Join(missing, ", Dependencies."))
	}
	if _, insecure := d.Transit.(*InsecureTestTransit); insecure && !underGoTest() {
		return errors.New("password: New refuses Dependencies.Transit = *InsecureTestTransit outside `go test`: its keys live in this process only, so every password stored with it is unreadable after a restart; use OpenBaoTransit")
	}
	return nil
}

// underGoTest reports whether this binary is a `go test` binary; a test replaces it to check the refusal.
var underGoTest = testing.Testing

// canonicalUUID parses a user uuid and returns its canonical lower-case form.
func canonicalUUID(s string) (string, error) {
	t := strings.TrimSpace(s)
	u, err := uuid.Parse(t)
	if err != nil || u == uuid.Nil || len(t) != 36 {
		return "", fmt.Errorf("password: %q is not a user uuid", truncate(s, 64))
	}
	return u.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
