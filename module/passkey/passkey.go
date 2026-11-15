// Package passkey registers passkeys and logs users in with them: a WebAuthn relying party built on
// github.com/go-webauthn/webauthn.
//
// Every passkey is a discoverable credential created and used with user verification (the authenticator's PIN,
// fingerprint or face check), so a passkey alone logs the user in. A user may hold several passkeys; removing one is
// refused when it is the user's last way in (no other passkey and no password, asked through Accounts).
//
// A ceremony's challenge lives in Valkey for at most five minutes under Config.KeyPrefix + "c:" + SHA-256(handle),
// never in a table, and is taken (read and deleted in one step) before the browser's answer is checked, so each
// challenge is used at most once, also when the check fails. A successful login starts a session through
// session.Store with the auth method "passkey". Every registration, removal, login and failure is written to an
// auth_audit.Sink.
//
// The passkeys themselves are kept by a CredentialStore: DBStore keeps them in a table the service names, through
// dxlib's databases package; MemoryStore is for tests.
//
// No go-webauthn type is part of this package's API: the options for the browser are JSON bytes, the browser's
// answer is read from an io.Reader, and the library's credential record is kept as opaque JSON.
package passkey

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// AuthMethod is the session's auth method after a passkey login.
const AuthMethod = "passkey"

// Passkey is one registered passkey.
type Passkey struct {
	// ID is the credential id, base64url without padding. It is not secret (browsers are sent it), and it names
	// the passkey in List and Remove.
	ID       string
	UserUUID string
	// Name is the label the user gave it ("Laptop", "Phone").
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero until it is first used to log in
	// BackupEligible and BackupState tell a synced passkey (copied by the platform to the user's other devices)
	// from one bound to a single device.
	BackupEligible bool
	BackupState    bool
	// Credential is the WebAuthn credential record (public key, sign count, flags) as opaque JSON. A
	// CredentialStore keeps it as it is given.
	Credential []byte
}

// CredentialStore keeps the passkeys. Every method that names a user only touches that user's passkeys.
type CredentialStore interface {
	// Add stores a new passkey. ErrDuplicate when its ID is already registered (to any user); ErrTooMany when the
	// user already holds maxPerUser passkeys.
	Add(ctx context.Context, p Passkey, maxPerUser int) error
	// ByUser returns the user's passkeys, oldest first.
	ByUser(ctx context.Context, userUUID string) ([]Passkey, error)
	// ByID returns the passkey with this ID, whoever holds it. ErrNotFound when there is none.
	ByID(ctx context.Context, id string) (*Passkey, error)
	// RecordUse stores the credential record and time after a login with the passkey (its sign count and flags
	// change). ErrNotFound when the user holds no passkey with this ID.
	RecordUse(ctx context.Context, userUUID, id string, credential []byte, usedAt time.Time) error
	// Remove deletes one of the user's passkeys. Before deleting, and while no other Remove of the same user can
	// run, it calls allow with the number of passkeys the user would keep; an error from allow refuses the removal
	// and is returned. allow may be nil. ErrNotFound when the user holds no passkey with this ID, also when another
	// user does.
	Remove(ctx context.Context, userUUID, id string, allow func(remaining int) error) error
}

// Accounts is the service's side of a passkey login and removal.
type Accounts interface {
	// LoginProfile returns the profile a passkey login starts the session with, read from the authoritative store.
	// An error refuses the login: return one when the user is disabled, deleted or locked, or the store failed. Its
	// text goes into the audit record.
	LoginProfile(ctx context.Context, userUUID string) (session.Profile, error)
	// HasPassword reports whether the user can still log in with a password. Asked before a user's last passkey is
	// removed; an error refuses the removal.
	HasPassword(ctx context.Context, userUUID string) (bool, error)
}

// User is the logged-in user who registers a passkey.
type User struct {
	UUID string
	// Username and DisplayName are shown by the authenticator when the user picks a passkey.
	Username    string
	DisplayName string
}

// Client describes the caller, for the audit record only.
type Client struct {
	Address   string
	UserAgent string
}

var (
	// ErrRefused wraps every ceremony that did not pass (a wrong, replayed or expired challenge, a bad signature, no
	// user verification, an unknown passkey, a refused account). Answer it as 401 or 400 without the detail; the
	// error's text holds the real reason for the log, and the audit record has it too.
	ErrRefused = errors.New("passkey: not accepted")
	// ErrNotFound: no such passkey for this user. Answer it as 404.
	ErrNotFound = errors.New("passkey: not found")
	// ErrLastWayIn: the passkey is the user's last way in (no other passkey and no password).
	ErrLastWayIn = errors.New("passkey: it is the user's last way in (no other passkey and no password)")
	// ErrDuplicate: the credential is already registered.
	ErrDuplicate = errors.New("passkey: this passkey is already registered")
	// ErrTooMany: the user already holds Config.MaxPerUser passkeys.
	ErrTooMany = errors.New("passkey: the user holds the most passkeys allowed")
)

// Config is set by the service.
type Config struct {
	// RPID is the relying party id: the site's registrable domain or a host under it, for example
	// "login.example.test". Passkeys are bound to it.
	RPID string
	// RPDisplayName is the name the authenticator shows, for example "Example".
	RPDisplayName string
	// RPOrigins are the exact origins the pages run on, for example "https://login.example.test".
	RPOrigins []string
	// KeyPrefix starts every Valkey key, for example "myapp:passkey:". Letters, digits and : _ . - only.
	KeyPrefix string
	// ChallengeTTL is how long a ceremony may take. Default and longest 5 minutes.
	ChallengeTTL time.Duration
	// MaxPerUser bounds the passkeys of one user. Default 20.
	MaxPerUser int
	// OperationTimeout bounds the clean-up steps that run after the caller's context has ended (deleting a
	// passkey whose audit record could not be written). Default 5 seconds.
	OperationTimeout time.Duration
}

const (
	DefaultChallengeTTL     = 5 * time.Minute
	MaxChallengeTTL         = 5 * time.Minute
	DefaultMaxPerUser       = 20
	DefaultOperationTimeout = 5 * time.Second
	// MaxResponseBytes bounds the browser's answer read by FinishRegistration and FinishLogin.
	MaxResponseBytes = 64 << 10
	// MaxNameLength bounds a passkey's name, in characters.
	MaxNameLength = 64
)

var keyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9:_.\-]{1,64}$`)

func (c Config) withDefaults() (Config, error) {
	if !keyPrefixPattern.MatchString(c.KeyPrefix) {
		return c, fmt.Errorf("passkey: Config.KeyPrefix %q must be 1 to 64 letters, digits or : _ . -", c.KeyPrefix)
	}
	if c.RPID == "" || c.RPDisplayName == "" || len(c.RPOrigins) == 0 {
		return c, errors.New("passkey: Config needs RPID, RPDisplayName and at least one of RPOrigins")
	}
	if c.ChallengeTTL == 0 {
		c.ChallengeTTL = DefaultChallengeTTL
	}
	if c.ChallengeTTL < time.Second || c.ChallengeTTL > MaxChallengeTTL {
		return c, fmt.Errorf("passkey: Config.ChallengeTTL %s must be between 1s and %s", c.ChallengeTTL, MaxChallengeTTL)
	}
	if c.MaxPerUser == 0 {
		c.MaxPerUser = DefaultMaxPerUser
	}
	if c.MaxPerUser < 1 {
		return c, fmt.Errorf("passkey: Config.MaxPerUser %d must be at least 1", c.MaxPerUser)
	}
	if c.OperationTimeout == 0 {
		c.OperationTimeout = DefaultOperationTimeout
	}
	return c, nil
}

// canonicalUUID parses a user uuid and returns its canonical lower-case form.
func canonicalUUID(s string) (string, error) {
	t := strings.TrimSpace(s)
	u, err := uuid.Parse(t)
	if err != nil || u == uuid.Nil || len(t) != 36 {
		return "", fmt.Errorf("passkey: %q is not a user uuid", truncate(s, 64))
	}
	return u.String(), nil
}

// CheckName returns the passkey name cleaned of surrounding space, or an error when it is empty, longer than
// MaxNameLength characters, not UTF-8, or holds a control character.
func CheckName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", errors.New("passkey: the name is empty")
	}
	if !utf8.ValidString(n) {
		return "", errors.New("passkey: the name is not UTF-8")
	}
	if utf8.RuneCountInString(n) > MaxNameLength {
		return "", fmt.Errorf("passkey: the name is longer than %d characters", MaxNameLength)
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return "", errors.New("passkey: the name holds a control character")
		}
	}
	return n, nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// maxIDLength is the base64url length of WebAuthn's longest credential id (1023 bytes).
const maxIDLength = 1364

// checkID refuses anything but a base64url credential id before it reaches a store.
func checkID(id string) error {
	if len(id) > maxIDLength || !idPattern.MatchString(id) {
		return ErrNotFound
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
