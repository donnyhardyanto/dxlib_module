package credentials

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/tls/tlstest"
)

// The config handed to the trial connection and to dxlib is raised to TLS 1.2 on a copy, and used as given when
// it is already at or above it.
func TestRedisConnectionTLSFloorOnACopy(t *testing.T) {
	c := NewRedisConnection("x", "127.0.0.1:1", 0, "v", NewFixed(nil))
	if c.effectiveTLSConfig() != nil || c.tlsSummary() != "TLS off" {
		t.Fatalf("no TLSConfig must mean plain TCP")
	}
	given := &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11, ServerName: "valkey.test"}
	c.TLSConfig = given
	got := c.effectiveTLSConfig()
	if got == given || got.MinVersion != tls.VersionTLS12 || got.MaxVersion != tls.VersionTLS12 || got.ServerName != "valkey.test" {
		t.Fatalf("effective config: min %s max %s server name %q (same pointer: %t)",
			tls.VersionName(got.MinVersion), tls.VersionName(got.MaxVersion), got.ServerName, got == given)
	}
	if given.MinVersion != tls.VersionTLS10 {
		t.Fatalf("the caller's config was changed in place")
	}
	c.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	if c.effectiveTLSConfig() != c.TLSConfig {
		t.Fatalf("a config at or above TLS 1.2 must be used as given")
	}
}

func TestRedisConnectionSetTLSFromBlock(t *testing.T) {
	root := tlstest.NewRootCA(t, "redis-connection-test-root")
	dir := t.TempDir()
	caFile := tlstest.WriteCA(t, dir, "ca", root)
	crt, key := tlstest.WriteLeaf(t, dir, "client", root.Issue(t, tlstest.LeafOptions{CommonName: "session-store", ClientOnly: true}))

	c := NewRedisConnection("sessions", "127.0.0.1:1", 0, "v", NewFixed(nil))
	if err := c.SetTLSFromBlock(utils.JSON{"tls-policy": "intermediate", "ca-trust": "custom", "ca-files": []any{caFile},
		"cert-file": crt, "key-file": key, "server-name": "valkey.test"}); err != nil {
		t.Fatal(err)
	}
	if c.TLSConfig == nil || c.TLS == nil || c.TLSConfig.ServerName != "valkey.test" || c.TLSConfig.GetClientCertificate == nil ||
		c.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("TLS not set from the block: %+v", c.TLSConfig)
	}
	if !strings.HasPrefix(c.tlsSummary(), "TLS on (") {
		t.Fatalf("summary %q", c.tlsSummary())
	}

	if err := c.SetTLSFromBlock(utils.JSON{"enabled": false, "tls-policy": "intermediate", "ca-trust": "system"}); err != nil {
		t.Fatal(err)
	}
	if c.TLSConfig != nil || c.TLS != nil {
		t.Fatalf("enabled=false must leave TLS off")
	}

	err := c.SetTLSFromBlock(utils.JSON{"tls-policy": "intermediate", "ca-trust": "custom", "ca-files": []any{filepath.Join(dir, "missing.pem")}})
	if err == nil || !strings.Contains(err.Error(), "missing.pem") || !strings.Contains(err.Error(), "Valkey sessions at 127.0.0.1:1/0") {
		t.Fatalf("a missing CA file must be reported with the connection and the file: %v", err)
	}
}

// The tests below need the TLS listener of tools/test-valkey.sh: TLS only, client certificates required, a
// server certificate for valkey.test signed by the CA in DXLIB_MODULE_TEST_VALKEY_TLS_DIR.
type valkeyTLSEnv struct {
	addr                         string
	caFile, clientCrt, clientKey string
}

func valkeyTLS(t *testing.T) valkeyTLSEnv {
	t.Helper()
	addr, dir := os.Getenv("DXLIB_MODULE_TEST_VALKEY_TLS"), os.Getenv("DXLIB_MODULE_TEST_VALKEY_TLS_DIR")
	if addr == "" || dir == "" {
		t.Skip("DXLIB_MODULE_TEST_VALKEY_TLS and DXLIB_MODULE_TEST_VALKEY_TLS_DIR are not set; run tools/test-valkey.sh")
	}
	return valkeyTLSEnv{addr: addr, caFile: filepath.Join(dir, "ca.crt"), clientCrt: filepath.Join(dir, "client.crt"), clientKey: filepath.Join(dir, "client.key")}
}

func (e valkeyTLSEnv) block() utils.JSON {
	return utils.JSON{"tls-policy": "intermediate", "ca-trust": "custom", "ca-files": []any{e.caFile},
		"cert-file": e.clientCrt, "key-file": e.clientKey, "server-name": "valkey.test"}
}

// A user limited to its key prefix works over TLS: the listener takes nothing but TLS, so the trial connection and
// the real one both made the handshake (with the client certificate the server requires).
func TestValkeyTLSWithAPrefixLimitedUser(t *testing.T) {
	e := valkeyTLS(t)
	_, admin, user, pw := valkeyUser(t)
	ctx := context.Background()
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "+set", "+get").Err(); err != nil {
		t.Fatal(err)
	}
	c := NewRedisConnection("tls", e.addr, 0, "v", NewFixed(map[string]Credential{"v": {Username: user, Password: pw, Version: "1"}}))
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetTLSFromBlock(e.block()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Command(ctx, "set", "SET", user+":k", "v1"); err != nil {
		t.Fatalf("SET under the user's prefix over TLS: %v", err)
	}
	if v, err := c.Command(ctx, "get", "GET", user+":k"); err != nil || v != "v1" {
		t.Fatalf("GET over TLS: %v, %v", v, err)
	}
	if _, err := c.Command(ctx, "get", "GET", "other:k"); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("a key outside the prefix must be refused by the ACL: %v", err)
	}
	if c.attempts != 1 {
		t.Fatalf("%d connection attempts, want 1", c.attempts)
	}
	if v, err := admin.Get(ctx, user+":k").Result(); err != nil || v != "v1" {
		t.Fatalf("the value written over TLS, read on the plain port: %q, %v", v, err)
	}
}

// Each way the TLS settings can be wrong fails in the trial connection, says so, and leaves no connection made.
func TestValkeyTLSFailuresStopAtTheTrialConnection(t *testing.T) {
	e := valkeyTLS(t)
	_, _, user, pw := valkeyUser(t)
	other := tlstest.NewRootCA(t, "unrelated-root")
	otherCA := tlstest.WriteCA(t, t.TempDir(), "other", other)
	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(e.caFile)
	if err != nil || !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("reading the test CA %s: %v", e.caFile, err)
	}
	withServerName := func(kv utils.JSON) utils.JSON { kv["server-name"] = "valkey.test"; return kv }

	cases := map[string]struct {
		block  utils.JSON
		config *tls.Config
		want   []string
	}{
		"plain TCP on the TLS port": {want: []string{"TLS off"}},
		"no client certificate": {block: withServerName(utils.JSON{"tls-policy": "intermediate", "ca-trust": "custom", "ca-files": []any{e.caFile}}),
			want: []string{"TLS on"}},
		"another CA": {block: withServerName(utils.JSON{"tls-policy": "intermediate", "ca-trust": "custom", "ca-files": []any{otherCA},
			"cert-file": e.clientCrt, "key-file": e.clientKey}), want: []string{"TLS handshake", "TRUST"}},
		"no server name for an IP address": {config: &tls.Config{RootCAs: pool}, want: []string{"TLS handshake", "NAME", "server name the host of " + e.addr}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewRedisConnection("tls", e.addr, 0, "v", NewFixed(map[string]Credential{"v": {Username: user, Password: pw, Version: "1"}}))
			c.ProbeTimeout, c.RetryMin = 2*time.Second, time.Hour
			t.Cleanup(func() { _ = c.Close() })
			if tc.block != nil {
				if err := c.SetTLSFromBlock(tc.block); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != nil {
				c.TLSConfig = tc.config
			}
			before := runtime.NumGoroutine()
			_, err := c.Command(context.Background(), "ping", "PING")
			if err == nil {
				t.Fatalf("connected")
			}
			msg := err.Error()
			t.Logf("reported: %v", err)
			if !strings.Contains(msg, "connecting as") || strings.Contains(msg, "after a trial connection succeeded") {
				t.Fatalf("the failure must come from the trial connection: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error lacks %q: %v", w, err)
				}
			}
			c.mu.Lock()
			current, attempts := c.current, c.attempts
			c.mu.Unlock()
			if current != nil || attempts != 1 {
				t.Fatalf("connection %v after %d attempts; want none after 1", current, attempts)
			}
			time.Sleep(50 * time.Millisecond)
			if after := runtime.NumGoroutine(); after > before+1 {
				t.Fatalf("goroutines grew from %d to %d", before, after)
			}
		})
	}
}

// A new credential after a rotation is tried and used over TLS too.
func TestValkeyTLSReconnectsAfterARotation(t *testing.T) {
	e := valkeyTLS(t)
	_, admin, user, pw := valkeyUser(t)
	provider := NewFixed(map[string]Credential{"v": {Username: user, Password: pw, Version: "1"}})
	c := NewRedisConnection("tls", e.addr, 0, "v", provider)
	c.CloseGrace = 10 * time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetTLSFromBlock(e.block()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Command(ctx, "ping", "PING"); err != nil {
		t.Fatal(err)
	}
	pw2 := rotateOnServer(t, admin, user)
	provider.Set("v", Credential{Username: user, Password: pw2, Version: "2"})
	if _, err := c.Command(ctx, "ping", "PING"); err != nil {
		t.Fatalf("after the rotation: %v", err)
	}
	if c.attempts != 2 || c.CredentialVersion() != "2" {
		t.Fatalf("%d attempts (want 2), credential version %q (want 2)", c.attempts, c.CredentialVersion())
	}
}
