package base

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	dxerrors "github.com/donnyhardyanto/dxlib/errors"
)

func TestNotFoundError(t *testing.T) {
	err := NewNotFoundError("USER_NOT_FOUND", "user", map[string]any{"id": int64(42)})

	if got, want := err.Error(), "USER_NOT_FOUND: no user with id=42"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if err.StatusCode() != http.StatusNotFound {
		t.Errorf("StatusCode() = %d, want 404", err.StatusCode())
	}

	// Callers see it through the wraps this module and dxlib put around it.
	for name, wrapped := range map[string]error{
		"bare":           err,
		"fmt %w":         fmt.Errorf("delete user: %w", err),
		"dxlib Wrap":     dxerrors.Wrap(err, "delete user"),
		"dxlib Wrapf x2": dxerrors.Wrapf(dxerrors.Wrap(err, "inner"), "outer %d", 1),
	} {
		if !errors.Is(wrapped, ErrNotFound) {
			t.Errorf("%s: errors.Is(ErrNotFound) = false", name)
		}
		if !errors.Is(wrapped, &NotFoundError{Code: "USER_NOT_FOUND"}) {
			t.Errorf("%s: errors.Is(same code) = false", name)
		}
		if errors.Is(wrapped, &NotFoundError{Code: "ROLE_NOT_FOUND"}) {
			t.Errorf("%s: errors.Is(other code) = true", name)
		}
		var nf *NotFoundError
		if !errors.As(wrapped, &nf) || nf.Lookup["id"] != int64(42) || nf.Entity != "user" {
			t.Errorf("%s: errors.As gave %+v", name, nf)
		}
		if !strings.Contains(wrapped.Error(), "USER_NOT_FOUND") {
			t.Errorf("%s: text %q lost the legacy code", name, wrapped.Error())
		}
	}

	if errors.Is(errors.New("USER_NOT_FOUND"), ErrNotFound) {
		t.Error("a plain error matched ErrNotFound")
	}
	if got := NewNotFoundError("X_NOT_FOUND", "", nil).Error(); got != "X_NOT_FOUND" {
		t.Errorf("no lookup: Error() = %q", got)
	}
	keys := NewNotFoundError("C", "thing", map[string]any{"b": 2, "a": 1}).Error()
	if keys != "C: no thing with a=1, b=2" {
		t.Errorf("lookup keys not sorted: %q", keys)
	}
}

func TestFingerprint(t *testing.T) {
	const secret = "a-registration-token-that-must-not-leak"
	fp := Fingerprint(secret)
	if !strings.HasPrefix(fp, "sha256:") || len(fp) != len("sha256:")+12 {
		t.Errorf("Fingerprint = %q, want sha256: and 12 hex digits", fp)
	}
	if strings.Contains(fp, secret) || fp != Fingerprint(secret) || fp == Fingerprint(secret+"x") {
		t.Errorf("Fingerprint not stable, distinct and opaque: %q", fp)
	}
}
