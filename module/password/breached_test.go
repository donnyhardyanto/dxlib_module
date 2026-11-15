package password

import (
	"crypto/sha1" //nolint:gosec // the list format under test
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/donnyhardyanto/dxlib_module/module/credentials"
)

func argonTag(pw string, salt []byte, passes, memory uint32, lanes uint8) []byte {
	return argon2.IDKey([]byte(pw), salt, passes, memory, lanes, 32)
}

func sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s)) //nolint:gosec // list format
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestHashListReadsThePwnedPasswordsFormat(t *testing.T) {
	in := "# comment\n" + sha1Hex("password1") + ":24230577\n\n" + strings.ToLower(sha1Hex("hunter2")) + "\n"
	l, err := NewHashList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if l.Len() != 2 {
		t.Fatalf("len %d", l.Len())
	}
	for pw, want := range map[string]bool{"password1": true, "hunter2": true, "not in the list": false} {
		if got, _ := l.Breached(bg, pw); got != want {
			t.Errorf("%q: %v, want %v", pw, got, want)
		}
	}
	if _, err := NewHashList(strings.NewReader(sha1Hex("a") + "\nnot-a-hash\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("a bad line: %v, want an error naming line 2", err)
	}
}

func TestRangeAPISendsOnlyThePrefixAndReadsPaddedAnswers(t *testing.T) {
	full := sha1Hex("password1")
	var mu sync.Mutex
	var paths, padding []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		padding = append(padding, r.Header.Get("Add-Padding"))
		mu.Unlock()
		if r.URL.Path == "/range/"+full[:5] {
			// a real suffix, then a padding line with count 0
			_, _ = fmt.Fprintf(w, "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n%s:42\r\n%s:0\r\n", full[5:], strings.Repeat("F", 35))
			return
		}
		_, _ = fmt.Fprintf(w, "%s:0\r\n", strings.Repeat("A", 35))
	}))
	defer srv.Close()
	api, err := NewRangeAPI(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := api.Breached(bg, "password1"); !got || err != nil {
		t.Fatalf("password1: %v %v", got, err)
	}
	if got, err := api.Breached(bg, "a passphrase nobody has used"); got || err != nil {
		t.Fatalf("unknown: %v %v", got, err)
	}
	api.MinCount = 100
	if got, _ := api.Breached(bg, "password1"); got {
		t.Fatal("seen 42 times counted as breached with MinCount 100")
	}
	for i, p := range paths {
		if len(p) != len("/range/")+5 || padding[i] != "true" {
			t.Fatalf("request %d: path %q padding %q; want only a 5-digit prefix and Add-Padding", i, p, padding[i])
		}
	}
}

func TestRangeAPIErrorsNameTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	api, _ := NewRangeAPI(srv.URL, nil)
	_, err := api.Breached(bg, goodPassword)
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("got %v", err)
	}
	if _, err := NewRangeAPI("ftp://example.test", nil); err == nil {
		t.Fatal("an ftp URL was accepted")
	}
}

// fakeBao is a transit server speaking the OpenBao HTTP API over TestTransit.
type fakeBao struct {
	mu       sync.Mutex
	tokens   map[string]bool
	transit  *TestTransit
	key      KeyInfo
	requests int
}

func (f *fakeBao) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	ok := f.tokens[r.Header.Get("X-Vault-Token")]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
		return
	}
	var in map[string]string
	_ = json.NewDecoder(r.Body).Decode(&in)
	reply := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []string{msg}})
	}
	switch {
	case r.URL.Path == "/v1/transit/keys/pw":
		k := f.key
		k.LatestVersion, _ = f.transit.LatestVersion(bg)
		reply(k)
	case r.URL.Path == "/v1/transit/encrypt/pw":
		pt, err := decodeB64(in["plaintext"])
		if err != nil {
			fail(400, "bad plaintext")
			return
		}
		ct, _ := f.transit.Encrypt(bg, pt)
		reply(map[string]any{"ciphertext": "vault" + strings.TrimPrefix(ct.Text, "test"), "key_version": ct.KeyVersion})
	case r.URL.Path == "/v1/transit/decrypt/pw":
		pt, err := f.transit.Decrypt(bg, "test"+strings.TrimPrefix(in["ciphertext"], "vault"))
		if err != nil {
			fail(400, err.Error())
			return
		}
		reply(map[string]string{"plaintext": encodeB64(pt)})
	case r.URL.Path == "/v1/transit/rewrap/pw":
		ct, err := f.transit.Rewrap(bg, "test"+strings.TrimPrefix(in["ciphertext"], "vault"))
		if err != nil {
			fail(400, err.Error())
			return
		}
		reply(map[string]any{"ciphertext": "vault" + strings.TrimPrefix(ct.Text, "test"), "key_version": ct.KeyVersion})
	default:
		fail(404, "no handler for route "+r.URL.Path)
	}
}

func newFakeBao(t *testing.T, tokens ...string) (*fakeBao, *httptest.Server) {
	f := &fakeBao{tokens: map[string]bool{}, transit: NewTestTransit(), key: KeyInfo{Type: "aes256-gcm96"}}
	for _, tk := range tokens {
		f.tokens[tk] = true
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func TestOpenBaoTransitRoundTripAndRotation(t *testing.T) {
	f, srv := newFakeBao(t, "t1")
	tr, err := NewOpenBaoTransit(OpenBaoConfig{Address: srv.URL, Key: "pw", CredentialName: "openbao/transit", AllowHTTP: true},
		credentials.NewFixed(map[string]credentials.Credential{"openbao/transit": {Password: "t1"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Check(bg); err != nil {
		t.Fatal(err)
	}
	ct, err := tr.Encrypt(bg, []byte("hello"))
	if err != nil || ct.KeyVersion != 1 || !strings.HasPrefix(ct.Text, "vault:v1:") {
		t.Fatalf("encrypt: %+v %v", ct, err)
	}
	f.transit.Rotate()
	nw, err := tr.Rewrap(bg, ct.Text)
	if err != nil || nw.KeyVersion != 2 {
		t.Fatalf("rewrap: %+v %v", nw, err)
	}
	pt, err := tr.Decrypt(bg, nw.Text)
	if err != nil || string(pt) != "hello" {
		t.Fatalf("decrypt: %q %v", pt, err)
	}
	if v, _ := tr.LatestVersion(bg); v != 2 {
		t.Fatalf("latest %d", v)
	}
	_, err = tr.Decrypt(bg, "vault:v9:AAAA")
	var be *baoError
	if !errors.As(err, &be) || be.status != 400 || !strings.Contains(err.Error(), "no key version 9") {
		t.Fatalf("decrypting an unknown version: %v, want HTTP 400 with the server's message", err)
	}
}

func TestOpenBaoTransitAsksForANewTokenOn403(t *testing.T) {
	f, srv := newFakeBao(t, "old")
	creds := credentials.NewFixed(map[string]credentials.Credential{"bao": {Password: "old"}})
	tr, _ := NewOpenBaoTransit(OpenBaoConfig{Address: srv.URL, Key: "pw", CredentialName: "bao", AllowHTTP: true}, creds)
	if _, err := tr.Encrypt(bg, []byte("x")); err != nil {
		t.Fatal(err)
	}
	// The token is rotated in the secret store and the old one revoked.
	f.mu.Lock()
	f.tokens = map[string]bool{"new": true}
	f.mu.Unlock()
	creds.Set("bao", credentials.Credential{Password: "new"})
	if _, err := tr.Encrypt(bg, []byte("x")); err != nil {
		t.Fatalf("after the rotation: %v", err)
	}
	// A token the provider cannot replace: one retry at most, then the 403 with the server's words.
	f.mu.Lock()
	f.tokens = map[string]bool{}
	before := f.requests
	f.mu.Unlock()
	_, err := tr.Encrypt(bg, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("got %v", err)
	}
	if n := f.requests - before; n != 1 {
		t.Fatalf("%d requests with an unchanged refused token, want 1", n)
	}
}

func TestOpenBaoTransitCheckNamesAWrongKey(t *testing.T) {
	f, srv := newFakeBao(t, "t")
	f.key = KeyInfo{Type: "aes128-gcm96", Exportable: true, AllowPlaintextBackup: true}
	tr, _ := NewOpenBaoTransit(OpenBaoConfig{Address: srv.URL, Key: "pw", CredentialName: "c", AllowHTTP: true},
		credentials.NewFixed(map[string]credentials.Credential{"c": {Password: "t"}}))
	err := tr.Check(bg)
	for _, want := range []string{"aes128-gcm96", "exportable", "plaintext backup"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want it to name %q", err, want)
		}
	}
}

func TestOpenBaoConfigIsChecked(t *testing.T) {
	p := credentials.NewFixed(nil)
	for _, c := range []OpenBaoConfig{
		{Address: "http://bao.test:8200", Key: "pw", CredentialName: "c"},          // http without AllowHTTP
		{Address: "https://bao.test:8200", Key: "../sys/raw", CredentialName: "c"}, // a path in the key name
		{Address: "https://bao.test:8200", Key: "pw", Mount: "a/../b", CredentialName: "c"},
		{Address: "https://bao.test:8200", Key: "pw"}, // no credential name
		{Address: "https://bao.test:8200/?x=1", Key: "pw", CredentialName: "c"},
	} {
		if _, err := NewOpenBaoTransit(c, p); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if _, err := NewOpenBaoTransit(OpenBaoConfig{Address: "https://bao.test:8200", Key: "pw", CredentialName: "c"}, nil); err == nil {
		t.Error("no provider accepted")
	}
}

func TestAManagerOnOpenBaoTransitLogsIn(t *testing.T) {
	_, srv := newFakeBao(t, "t")
	tr, _ := NewOpenBaoTransit(OpenBaoConfig{Address: srv.URL, Key: "pw", CredentialName: "c", AllowHTTP: true},
		credentials.NewFixed(map[string]credentials.Credential{"c": {Password: "t"}}))
	r := newRig(t, nil)
	r.m.transit = tr
	alice := r.accounts.add("alice")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); err != nil {
		t.Fatal(err)
	}
}

func decodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
func encodeB64(b []byte) string          { return base64.StdEncoding.EncodeToString(b) }
