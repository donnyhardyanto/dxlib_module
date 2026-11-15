// Package session is a session store in Valkey (or Redis) that carries the user's profile, so a request needs no
// database read to know who is calling and what they may do.
//
// A browser holds a 256-bit random token in a __Host- cookie (Secure, HttpOnly, SameSite=Strict). Valkey holds
// the session under prefix + "s:" + SHA-256(token), never the token itself, with a sliding idle TTL capped by an
// absolute expiry, both as Valkey TTLs. A per-user set (prefix + "u:" + user uuid) lists every session of a user,
// so a change to the user reaches all of them at once (PropagateProfile) and disabling a user ends them all
// (RevokeAll). The database stays the authority: the profile in a session is a cache, rebuilt through a
// ProfileLoader when it is missing.
//
// Every key starts with Config.KeyPrefix, so the Valkey user can be limited to that prefix by ACL. Run Valkey with
// maxmemory-policy noeviction, so sessions are never dropped silently. One node or a Sentinel-managed primary;
// Valkey Cluster is not supported (the scripts touch keys found in the per-user set).
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Profile is the user's data a session carries. The database is its authority.
type Profile struct {
	Username     string   `json:"username"`
	DisplayName  string   `json:"display_name"`
	Status       string   `json:"status"`
	PhotoRef     string   `json:"photo_ref,omitempty"`
	PhotoVersion int64    `json:"photo_version,omitempty"`
	Roles        []string `json:"roles"`
	Permissions  []string `json:"permissions"`
	// Version orders changes: a copy with a lower version never replaces a higher one, so two updates that reach
	// Valkey out of order leave the newer one. Take it from the database row (a version column or the update
	// time in microseconds). It is stored beside the profile, not inside it.
	Version int64 `json:"-"`
}

// Session is one logged-in browser or device.
type Session struct {
	// ID is the hex SHA-256 of the token: safe to show the user and to name in an audit record, useless as a
	// cookie.
	ID         string
	UserUUID   string
	AuthMethod string
	// CSRFToken is sent to the page and checked on every state-changing request (CheckCSRF). Empty in List.
	CSRFToken      string
	CreatedAt      time.Time
	AbsoluteExpiry time.Time
	Profile        Profile
}

// CheckCSRF compares a request's CSRF token with the session's in constant time.
func (s *Session) CheckCSRF(given string) bool {
	if s == nil || s.CSRFToken == "" || given == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.CSRFToken), []byte(given)) == 1
}

// LoginInfo describes a successful authentication that should become a session.
type LoginInfo struct {
	UserUUID string
	// AuthMethod names how the user proved who they are ("password", "passkey"); lower case letters, digits, _ and -.
	AuthMethod string
	Profile    Profile
	// ClientAddress and UserAgent go to the audit record only.
	ClientAddress string
	UserAgent     string
	// AuditDetail adds facts of the login to its audit record (for example the passkey used). Its keys never
	// replace auth_method, client_address or user_agent. Never put a secret in it.
	AuditDetail map[string]string
}

// ProfileLoader reads a user's profile from the authoritative store, to rebuild a session's cached copy.
type ProfileLoader interface {
	LoadProfile(ctx context.Context, userUUID string) (Profile, error)
}

// ProfileLoaderFunc adapts a function to ProfileLoader.
type ProfileLoaderFunc func(ctx context.Context, userUUID string) (Profile, error)

// LoadProfile calls f.
func (f ProfileLoaderFunc) LoadProfile(ctx context.Context, userUUID string) (Profile, error) {
	return f(ctx, userUUID)
}

// PropagationResult counts what a profile propagation did.
type PropagationResult struct {
	Updated int // sessions now holding the new profile
	Newer   int // sessions left alone because they already hold a newer version
	Stale   int // index entries dropped because their session had expired
}

// Store is the session store's interface; ValkeyStore implements it.
type Store interface {
	// Create starts a session for a user who has just authenticated and returns its token (for the cookie only).
	Create(ctx context.Context, login LoginInfo) (token string, s *Session, err error)
	// Get returns the session of a token and slides its idle TTL. ErrNotFound when it is missing or expired.
	Get(ctx context.Context, token string) (*Session, error)
	// Rotate replaces a session's token (and CSRF token), keeping its creation time and absolute expiry. Call it
	// at every privilege change; a non-nil profile replaces the cached one in the same step.
	Rotate(ctx context.Context, token string, profile *Profile) (newToken string, s *Session, err error)
	// Logout deletes the session of a token.
	Logout(ctx context.Context, token string) error
	// List returns a user's open sessions, without their CSRF tokens.
	List(ctx context.Context, userUUID string) ([]Session, error)
	// RevokeSession ends one session of the calling user by its ID. ErrNotFound when it is not the caller's.
	RevokeSession(ctx context.Context, callerUserUUID, sessionID string) error
	// RevokeAll ends every session of a user, as when the user is disabled or deleted.
	RevokeAll(ctx context.Context, userUUID, reason string) (int, error)
	// PropagateProfile writes a changed profile into every session of the user. Call it after the database
	// commit. On failure every session of the user is deleted (fail closed) and a *PropagationError returned.
	PropagateProfile(ctx context.Context, userUUID string, p Profile) (PropagationResult, error)
	// InvalidateProfiles drops the cached profile from every session of the user, so the next Get of each
	// reloads it through the ProfileLoader.
	InvalidateProfiles(ctx context.Context, userUUID string) error
}

var (
	// ErrNotFound: no such session, or it expired, or it belongs to someone else. Answer it as 401 (own session)
	// or 404 (a session named by id), never with more detail.
	ErrNotFound = errors.New("session: not found or expired")
	// ErrInvalidToken: the cookie value is not a token this package issued (wrong length or characters).
	// Treat it as ErrNotFound when answering.
	ErrInvalidToken = errors.New("session: malformed session token")
)

// PropagationError is returned when the profile could not be written into the user's sessions. Deleted counts the
// sessions removed to fail closed; DeleteErr is set when even that failed, and then the old sessions may still be
// readable once Valkey answers again.
type PropagationError struct {
	UserUUID  string
	Version   int64
	UpdateErr error
	Deleted   int
	DeleteErr error
}

func (e *PropagationError) Error() string {
	if e.DeleteErr != nil {
		return fmt.Sprintf("session: writing profile version %d into the sessions of user %s failed (%v), and deleting those sessions to fail closed failed too (%v)", e.Version, e.UserUUID, e.UpdateErr, e.DeleteErr)
	}
	return fmt.Sprintf("session: writing profile version %d into the sessions of user %s failed (%v); deleted %d of their sessions to fail closed", e.Version, e.UserUUID, e.UpdateErr, e.Deleted)
}

// Unwrap returns the update error and, when set, the delete error.
func (e *PropagationError) Unwrap() []error {
	if e.DeleteErr != nil {
		return []error{e.UpdateErr, e.DeleteErr}
	}
	return []error{e.UpdateErr}
}

// Config is set by the service.
type Config struct {
	// KeyPrefix starts every Valkey key, for example "myapp:session:". Letters, digits and : _ . - only.
	KeyPrefix string
	// CookieName must start with "__Host-". Default "__Host-session".
	CookieName string
	// IdleTTL ends a session not used for this long. Default 15 minutes.
	IdleTTL time.Duration
	// AbsoluteTTL ends a session this long after login, used or not. Default 12 hours.
	AbsoluteTTL time.Duration
	// OperationTimeout bounds propagation and its fail-closed delete, which run even when the caller's request
	// context has ended (the database change is already committed). Default 5 seconds.
	OperationTimeout time.Duration
}

const (
	DefaultCookieName       = "__Host-session"
	DefaultIdleTTL          = 15 * time.Minute
	DefaultAbsoluteTTL      = 12 * time.Hour
	DefaultOperationTimeout = 5 * time.Second
)

var (
	keyPrefixPattern  = regexp.MustCompile(`^[A-Za-z0-9:_.\-]{1,64}$`)
	authMethodPattern = regexp.MustCompile(`^[a-z0-9_\-]{1,32}$`)
	sessionIDPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cookieNamePattern = regexp.MustCompile(`^__Host-[A-Za-z0-9_\-]{1,64}$`)
)

func (c Config) withDefaults() (Config, error) {
	if !keyPrefixPattern.MatchString(c.KeyPrefix) {
		return c, fmt.Errorf("session: Config.KeyPrefix %q must be 1 to 64 letters, digits or : _ . -", c.KeyPrefix)
	}
	if c.CookieName == "" {
		c.CookieName = DefaultCookieName
	}
	if !cookieNamePattern.MatchString(c.CookieName) {
		return c, fmt.Errorf("session: Config.CookieName %q must start with __Host- followed by letters, digits, _ or -", c.CookieName)
	}
	if c.IdleTTL == 0 {
		c.IdleTTL = DefaultIdleTTL
	}
	if c.AbsoluteTTL == 0 {
		c.AbsoluteTTL = DefaultAbsoluteTTL
	}
	if c.OperationTimeout == 0 {
		c.OperationTimeout = DefaultOperationTimeout
	}
	if c.IdleTTL < time.Millisecond || c.AbsoluteTTL < time.Millisecond {
		return c, fmt.Errorf("session: IdleTTL (%s) and AbsoluteTTL (%s) must be at least 1ms", c.IdleTTL, c.AbsoluteTTL)
	}
	if c.IdleTTL > c.AbsoluteTTL {
		return c, fmt.Errorf("session: IdleTTL (%s) is longer than AbsoluteTTL (%s)", c.IdleTTL, c.AbsoluteTTL)
	}
	return c, nil
}

// Cookie returns the cookie that carries a session token until the session's absolute expiry.
func (c Config) Cookie(token string, absoluteExpiry time.Time) *http.Cookie {
	name := c.CookieName
	if name == "" {
		name = DefaultCookieName
	}
	return &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/", // a __Host- cookie needs Path=/, Secure and no Domain, or the browser drops it
		Expires:  absoluteExpiry,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// ClearCookie returns the cookie that removes the session cookie from the browser.
func (c Config) ClearCookie() *http.Cookie {
	ck := c.Cookie("", time.Unix(0, 0))
	ck.MaxAge = -1
	return ck
}

// TokenFromRequest returns the session token in a request's cookie, or ErrNotFound when there is none.
// It does not check the token's form; Get does.
func (c Config) TokenFromRequest(r *http.Request) (string, error) {
	name := c.CookieName
	if name == "" {
		name = DefaultCookieName
	}
	ck, err := r.Cookie(name)
	if err != nil || ck.Value == "" {
		return "", ErrNotFound
	}
	return ck.Value, nil
}

const tokenBytes = 32 // 256 bits

// NewToken returns a new random 256-bit token, base64url without padding (43 characters).
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: reading %d random bytes for a token: %w", tokenBytes, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SessionID returns the public id of a token (the hex SHA-256 of its text), after checking the token's form.
func SessionID(token string) (string, error) {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return "", ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != tokenBytes {
		return "", ErrInvalidToken
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}

// canonicalUUID parses a user uuid and returns its canonical lower-case form, so nothing but a uuid reaches a
// Valkey key name.
func canonicalUUID(s string) (string, error) {
	u, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil || u == uuid.Nil || len(strings.TrimSpace(s)) != 36 {
		return "", fmt.Errorf("session: %q is not a user uuid", truncate(s, 64))
	}
	return u.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
