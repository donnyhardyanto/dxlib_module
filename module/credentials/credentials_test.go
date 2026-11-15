package credentials

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestCredentialNeverPrintsThePassword(t *testing.T) {
	c := Credential{Username: "svc", Password: "s3cret-value", Version: "7"}
	for _, s := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprint(c)} {
		if strings.Contains(s, "s3cret-value") {
			t.Fatalf("formatted credential shows the password: %s", s)
		}
	}
}

func TestFixedProvider(t *testing.T) {
	f := NewFixed(map[string]Credential{"a": {Username: "u", Version: "1"}})
	if c, err := f.Credential(context.Background(), "a"); err != nil || c.Version != "1" {
		t.Fatalf("got %v, %v", c, err)
	}
	f.Set("a", Credential{Username: "u", Version: "2"})
	if c, _ := f.Credential(context.Background(), "a"); c.Version != "2" {
		t.Fatal("Set did not replace the credential")
	}
	if _, err := f.Credential(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing credential: %v", err)
	}
}

func TestIsAuthError(t *testing.T) {
	for s, want := range map[string]bool{
		"WRONGPASS invalid username-password pair or user is disabled.": true,
		"NOAUTH Authentication required.":                               true,
		"NOPERM this user has no permissions":                           false,
		"dial tcp: connection refused":                                  false,
	} {
		if got := IsAuthError(errors.New(s)); got != want {
			t.Errorf("IsAuthError(%q) = %v", s, got)
		}
	}
	if IsAuthError(nil) {
		t.Error("nil is not an auth error")
	}
}

func TestExplainUnreachableAddsTheDialError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	err := ExplainUnreachable(errors.New("redis: all ring shards are down"), addr)
	if !strings.Contains(err.Error(), "connection refused") || !strings.Contains(err.Error(), addr) {
		t.Fatalf("got %v", err)
	}
	other := errors.New("NOPERM")
	if ExplainUnreachable(other, addr) != other {
		t.Fatal("an unrelated error was changed")
	}
}

func TestRedisConnectionReportsAProviderFailure(t *testing.T) {
	c := NewRedisConnection("x", "127.0.0.1:1", 0, "absent", NewFixed(nil))
	_, err := c.Command(context.Background(), "probe", "PING")
	if err == nil || !strings.Contains(err.Error(), `credential "absent"`) || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("got %v", err)
	}
}
