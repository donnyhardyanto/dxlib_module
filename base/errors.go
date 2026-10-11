package base

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ErrNotFound is matched by every NotFoundError, so callers can test any
// lookup failure with errors.Is(err, base.ErrNotFound) and answer 404.
var ErrNotFound = &NotFoundError{Code: "NOT_FOUND"}

// NotFoundError reports a lookup that found no row. Code is the error code the
// module returned before this type existed (such as "USER_NOT_FOUND"), so text
// that starts with it still does. Entity names what was looked up and Lookup
// holds the keys used; secrets (tokens, keys) go in as a fingerprint, never in
// full.
type NotFoundError struct {
	Code   string
	Entity string
	Lookup map[string]any
}

func NewNotFoundError(code string, entity string, lookup map[string]any) *NotFoundError {
	return &NotFoundError{Code: code, Entity: entity, Lookup: lookup}
}

func (e *NotFoundError) Error() string {
	if e.Entity == "" && len(e.Lookup) == 0 {
		return e.Code
	}
	keys := make([]string, 0, len(e.Lookup))
	for k := range e.Lookup {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, e.Lookup[k]))
	}
	return fmt.Sprintf("%s: no %s with %s", e.Code, e.Entity, strings.Join(parts, ", "))
}

// Is makes every NotFoundError match ErrNotFound, and two NotFoundErrors match
// when their codes are the same.
func (e *NotFoundError) Is(target error) bool {
	t, ok := target.(*NotFoundError)
	if !ok {
		return false
	}
	return t == ErrNotFound || t.Code == e.Code
}

// StatusCode is the HTTP status an API should answer with.
func (e *NotFoundError) StatusCode() int {
	return http.StatusNotFound
}

// Fingerprint names a secret (a token, a key handle) in errors and logs
// without giving it away: the first 12 hex digits of its SHA-256.
func Fingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}
