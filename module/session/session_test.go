package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewTokenIs256BitsAndValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 {
			t.Fatalf("token length %d, want 43 (256 bits base64url)", len(tok))
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
		id, err := SessionID(tok)
		if err != nil {
			t.Fatalf("SessionID(%q): %v", tok, err)
		}
		sum := sha256.Sum256([]byte(tok))
		if id != hex.EncodeToString(sum[:]) {
			t.Fatal("session id is not the SHA-256 of the token")
		}
	}
}

func TestSessionIDRejectsMalformedTokens(t *testing.T) {
	good, _ := NewToken()
	bad := []string{
		"",
		"short",
		good + "A",
		good[:42],
		strings.Repeat("*", 43),
		good[:40] + "\r\n",
		"' OR 1=1 --" + strings.Repeat("A", 32),
		strings.Repeat("A", 42) + "B", // non-canonical last character: trailing bits set
		"]]..redis.call('FLUSHALL')..[[" + strings.Repeat("A", 13),
	}
	for _, b := range bad {
		if _, err := SessionID(b); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("SessionID(%q) = %v, want ErrInvalidToken", b, err)
		}
	}
}

func TestCanonicalUUIDRejectsPayloads(t *testing.T) {
	ok, err := canonicalUUID("6F9619FF-8B86-D011-B42D-00C04FC964FF")
	if err != nil || ok != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Fatalf("canonicalUUID upper case = %q, %v", ok, err)
	}
	for _, b := range []string{
		"", "*", "00000000-0000-0000-0000-000000000000",
		"6f9619ff8b86d011b42d00c04fc964ff", // no dashes: refused, one spelling per user
		"urn:uuid:6f9619ff-8b86-d011-b42d-00c04fc964ff",
		"6f9619ff-8b86-d011-b42d-00c04fc964ff*",
		"6f9619ff-8b86-d011-b42d-00c04fc964ff\r\nFLUSHALL",
		"x' OR '1'='1", "../../etc/passwd",
	} {
		if _, err := canonicalUUID(b); err == nil {
			t.Errorf("canonicalUUID(%q) accepted", b)
		}
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	c, err := Config{KeyPrefix: "app:session:"}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if c.CookieName != "__Host-session" || c.IdleTTL != 15*time.Minute || c.AbsoluteTTL != 12*time.Hour {
		t.Fatalf("defaults: %+v", c)
	}
	for _, bad := range []Config{
		{},
		{KeyPrefix: "app session"},
		{KeyPrefix: "app:*"},
		{KeyPrefix: "app:", CookieName: "session"},
		{KeyPrefix: "app:", CookieName: "__Secure-session"},
		{KeyPrefix: "app:", IdleTTL: time.Hour, AbsoluteTTL: time.Minute},
	} {
		if _, err := bad.withDefaults(); err == nil {
			t.Errorf("config %+v accepted", bad)
		}
	}
}

func TestCookieIsHostPrefixedSecureStrict(t *testing.T) {
	c, _ := Config{KeyPrefix: "app:"}.withDefaults()
	exp := time.Now().Add(time.Hour)
	ck := c.Cookie("tok", exp)
	if ck.Name != "__Host-session" || !ck.Secure || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("cookie attributes: %+v", ck)
	}
	if !ck.Expires.Equal(exp) {
		t.Fatalf("cookie expiry %v, want %v", ck.Expires, exp)
	}
	line := ck.String()
	for _, want := range []string{"__Host-session=tok", "Path=/", "Secure", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(line, want) {
			t.Errorf("Set-Cookie %q lacks %q", line, want)
		}
	}
	clr := c.ClearCookie()
	if clr.MaxAge >= 0 || clr.Value != "" {
		t.Fatalf("clear cookie: %+v", clr)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := c.TokenFromRequest(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no cookie: %v", err)
	}
	r.AddCookie(&http.Cookie{Name: "__Host-session", Value: "abc"})
	if tok, err := c.TokenFromRequest(r); err != nil || tok != "abc" {
		t.Fatalf("TokenFromRequest = %q, %v", tok, err)
	}
}

func TestCheckCSRF(t *testing.T) {
	s := &Session{CSRFToken: "abc"}
	if !s.CheckCSRF("abc") || s.CheckCSRF("abd") || s.CheckCSRF("") {
		t.Fatal("CheckCSRF compares wrongly")
	}
	if (&Session{}).CheckCSRF("") {
		t.Fatal("an empty CSRF token must never match")
	}
}

func TestPropagationErrorNamesBothCauses(t *testing.T) {
	e := &PropagationError{UserUUID: "u", Version: 3, UpdateErr: errors.New("NOPERM hset"), DeleteErr: errors.New("connection refused")}
	if !strings.Contains(e.Error(), "NOPERM hset") || !strings.Contains(e.Error(), "connection refused") {
		t.Fatal(e.Error())
	}
	if !errors.Is(e, e.UpdateErr) || !errors.Is(e, e.DeleteErr) {
		t.Fatal("PropagationError does not unwrap to both causes")
	}
}
