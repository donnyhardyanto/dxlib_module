package accountlockout

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/donnyhardyanto/dxlib_module/module/account_lockout"
	"github.com/donnyhardyanto/dxlib_module/module/password"
)

func TestNewRefusesAnUninitialisedLockout(t *testing.T) {
	resolve := func(context.Context, string) (int64, string, error) { return 1, "a", nil }
	if _, err := New(nil, resolve); err == nil {
		t.Fatal("nil lockout accepted")
	}
	if _, err := New(&account_lockout.DXMAccountLockout{}, resolve); err == nil {
		t.Fatal("a lockout without Config accepted")
	}
	if _, err := New(&account_lockout.DXMAccountLockout{Config: &account_lockout.AccountLockoutConfig{}}, nil); err == nil {
		t.Fatal("no resolver accepted")
	}
}

func TestADisabledLockoutPassesAndAResolverErrorIsNamed(t *testing.T) {
	ctx := context.Background()
	var asked []string
	resolve := func(_ context.Context, u string) (int64, string, error) {
		asked = append(asked, u)
		if u == "gone" {
			return 0, "", errors.New("no user row")
		}
		return 42, "alice", nil
	}
	a, err := New(&account_lockout.DXMAccountLockout{Config: &account_lockout.AccountLockoutConfig{Enabled: false}}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if locked, err := a.Locked(ctx, "u1"); locked || err != nil {
		t.Fatalf("Locked: %v %v", locked, err)
	}
	if err := a.Failed(ctx, "u1", password.Client{Address: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Succeeded(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Unlock(ctx, "u1", "test"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 4 {
		t.Fatalf("resolver asked %d times, want 4", len(asked))
	}
	_, err = a.Locked(ctx, "gone")
	if err == nil || !strings.Contains(err.Error(), "no user row") || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("got %v, want the resolver's error and the user", err)
	}
}
