package password

// These tests run against a real OpenBao dev server, as tools/test-openbao.sh starts one:
//
//	DXLIB_MODULE_TEST_OPENBAO=http://127.0.0.1:18200 DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN=... go test ./module/password
//
// Each test mounts its own transit engine, makes the key, a policy with only the paths the package's doc names, and a
// token holding that policy; OpenBaoTransit uses that token. Without the variables they are skipped.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/credentials"
)

type baoAdmin struct {
	t     *testing.T
	addr  string
	token string
}

func (a baoAdmin) call(method, path string, body any) map[string]any {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(bg, method, a.addr+"/v1/"+path, rd)
	req.Header.Set("X-Vault-Token", a.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode > 299 {
		a.t.Fatalf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// newBao mounts a transit engine with the key "pw" (aes256-gcm96, not exportable) and an exportable key "bad", and
// returns the mount and a token whose policy allows only what OpenBaoConfig.CredentialName's doc lists for "pw".
func newBao(t *testing.T) (baoAdmin, string, string) {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_OPENBAO")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_OPENBAO is not set; run tools/test-openbao.sh")
	}
	a := baoAdmin{t: t, addr: addr, token: os.Getenv("DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN")}
	mount := "transit-" + uuid.NewString()[:8]
	a.call("POST", "sys/mounts/"+mount, map[string]any{"type": "transit"})
	t.Cleanup(func() { a.call("DELETE", "sys/mounts/"+mount, nil) })
	a.call("POST", mount+"/keys/pw", map[string]any{"type": "aes256-gcm96", "exportable": false})
	a.call("POST", mount+"/keys/bad", map[string]any{"type": "aes256-gcm96", "exportable": true})
	policy := fmt.Sprintf(`path "%[1]s/encrypt/pw" { capabilities = ["update"] }
path "%[1]s/decrypt/pw" { capabilities = ["update"] }
path "%[1]s/rewrap/pw" { capabilities = ["update"] }
path "%[1]s/keys/pw" { capabilities = ["read"] }`, mount)
	a.call("PUT", "sys/policies/acl/"+mount, map[string]any{"policy": policy})
	return a, mount, a.newToken(mount)
}

func (a baoAdmin) newToken(policy string) string {
	out := a.call("POST", "auth/token/create", map[string]any{"policies": []string{policy}, "ttl": "10m", "no_default_policy": true})
	return out["auth"].(map[string]any)["client_token"].(string)
}

func TestOpenBaoTransitAgainstARealServer(t *testing.T) {
	admin, mount, token := newBao(t)
	creds := credentials.NewFixed(map[string]credentials.Credential{"openbao/password-transit": {Password: token}})
	tr, err := NewOpenBaoTransit(OpenBaoConfig{Address: admin.addr, Mount: mount, Key: "pw", CredentialName: "openbao/password-transit", AllowHTTP: true}, creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Check(bg); err != nil {
		t.Fatalf("Check with the documented policy: %v", err)
	}

	// The server's answer names the key version itself, and it matches the ciphertext's prefix.
	raw := admin.call("POST", mount+"/encrypt/pw", map[string]any{"plaintext": encodeB64([]byte("x"))})
	if kv, _ := raw["data"].(map[string]any)["key_version"].(float64); kv != 1 {
		t.Fatalf("encrypt answer %v: want data.key_version 1", raw["data"])
	}

	r := newRig(t, nil)
	r.m.transit = tr
	alice, bob := r.accounts.add("alice"), r.accounts.add("bob")
	if err := r.m.Set(bg, "", alice, goodPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	before, _ := r.store.Get(bg, alice)
	if before.KeyVersion != 1 || !strings.HasPrefix(before.Ciphertext, "vault:v1:") {
		t.Fatalf("stored %+v", before)
	}

	admin.call("POST", mount+"/keys/pw/rotate", nil)
	if err := r.m.Set(bg, "", bob, otherPassword, false, Client{}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := r.store.Get(bg, bob); rec.KeyVersion != 2 {
		t.Fatalf("after the rotation a new password got key version %d", rec.KeyVersion)
	}
	res, err := r.m.RewrapAll(bg, 10)
	if err != nil || res.Rewrapped != 1 || res.LatestVersion != 2 {
		t.Fatalf("rewrap %+v %v", res, err)
	}
	admin.call("POST", mount+"/keys/pw/config", map[string]any{"min_decryption_version": 2})
	for _, u := range []string{"alice", "bob"} {
		p := goodPassword
		if u == "bob" {
			p = otherPassword
		}
		if _, _, err := r.m.Login(bg, u, p, Client{}); err != nil {
			t.Fatalf("%s after rewrap and trimming version 1: %v", u, err)
		}
	}
	_, err = tr.Decrypt(bg, before.Ciphertext)
	var be *baoError
	if !errors.As(err, &be) || be.status != http.StatusBadRequest {
		t.Fatalf("decrypting a version-1 ciphertext after trimming: %v, want HTTP 400 from the server", err)
	}

	// The token is rotated: the old one revoked, the provider hands out the new one.
	fresh := admin.newToken(mount)
	admin.call("POST", "auth/token/revoke", map[string]any{"token": token})
	creds.Set("openbao/password-transit", credentials.Credential{Password: fresh})
	if _, _, err := r.m.Login(bg, "alice", goodPassword, Client{}); err != nil {
		t.Fatalf("after the token rotation: %v", err)
	}
}

func TestOpenBaoPolicyAndKeyChecksAgainstARealServer(t *testing.T) {
	admin, mount, token := newBao(t)
	limited, _ := NewOpenBaoTransit(OpenBaoConfig{Address: admin.addr, Mount: mount, Key: "bad", CredentialName: "c", AllowHTTP: true},
		credentials.NewFixed(map[string]credentials.Credential{"c": {Password: token}}))
	_, err := limited.Encrypt(bg, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("a key outside the policy: %v, want HTTP 403 permission denied", err)
	}
	root, _ := NewOpenBaoTransit(OpenBaoConfig{Address: admin.addr, Mount: mount, Key: "bad", CredentialName: "c", AllowHTTP: true},
		credentials.NewFixed(map[string]credentials.Credential{"c": {Password: admin.token}}))
	if err := root.Check(bg); err == nil || !strings.Contains(err.Error(), "exportable") {
		t.Fatalf("Check of an exportable key: %v", err)
	}
}
