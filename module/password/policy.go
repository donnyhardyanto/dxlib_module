package password

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normalize returns the form a password is hashed and checked in: Unicode NFKC, so the same password typed on two
// keyboards (a composed or a decomposed é, a full-width digit) hashes the same.
func normalize(pw string) string { return norm.NFKC.String(pw) }

// checkShape checks what needs no outside answer: valid UTF-8 and the length in characters after normalisation.
func (m *Manager) checkShape(pw string) (string, error) {
	if !utf8.ValidString(pw) {
		return "", fmt.Errorf("%w: %w", ErrPolicy, ErrNotUTF8)
	}
	n := normalize(pw)
	count := utf8.RuneCountInString(n)
	if count < m.cfg.MinLength {
		return "", fmt.Errorf("%w: %w (%d characters, at least %d needed)", ErrPolicy, ErrTooShort, count, m.cfg.MinLength)
	}
	if count > m.cfg.MaxLength {
		return "", fmt.Errorf("%w: %w (%d characters, at most %d allowed)", ErrPolicy, ErrTooLong, count, m.cfg.MaxLength)
	}
	return n, nil
}

// CheckPolicy tells whether pw may become the user's new password, without storing anything: a page can call it
// before the form is sent. It returns an error wrapping ErrPolicy (with the reason) when the policy refuses it, or
// another error when the user's context words or the breached list could not be read.
func (m *Manager) CheckPolicy(ctx context.Context, userUUID, pw string) error {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return err
	}
	_, err = m.checkPolicy(ctx, u, pw)
	return err
}

// checkPolicy returns the normalised password when the policy allows it.
func (m *Manager) checkPolicy(ctx context.Context, userUUID, pw string) (string, error) {
	n, err := m.checkShape(pw)
	if err != nil {
		return "", err
	}
	words, err := m.accounts.ContextWords(ctx, userUUID)
	if err != nil {
		return "", fmt.Errorf("password: reading the context words of user %s: %w", userUUID, err)
	}
	folded := strings.ToLower(n)
	for _, w := range words {
		w = strings.ToLower(normalize(strings.TrimSpace(w)))
		if utf8.RuneCountInString(w) >= MinContextWordLength && strings.Contains(folded, w) {
			return "", fmt.Errorf("%w: %w", ErrPolicy, ErrContextWord)
		}
	}
	// The lists hold passwords as typed; check the normalised form as well when it differs.
	for _, candidate := range uniq(pw, n) {
		breached, err := m.breached.Breached(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("password: the breached-password check could not be made, so the password was not set: %w", err)
		}
		if breached {
			return "", fmt.Errorf("%w: %w", ErrPolicy, ErrBreached)
		}
	}
	return n, nil
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}
