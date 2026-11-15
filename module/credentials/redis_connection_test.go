package credentials

import (
	"context"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

func refusedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// With Valkey unreachable, a failed attempt is not repeated before the back-off has passed, the error keeps the
// network's own reason, and no failed attempt leaves a client running.
func TestRedisConnectionBacksOffAndLeavesNothingRunning(t *testing.T) {
	addr := refusedAddress(t)
	c := NewRedisConnection("x", addr, 0, "v", NewFixed(map[string]Credential{"v": {Username: "u", Password: "p", Version: "1"}}))
	c.RetryMin, c.RetryMax = 50*time.Millisecond, 200*time.Millisecond
	ctx := context.Background()

	before := runtime.NumGoroutine()
	_, err := c.Command(ctx, "probe", "PING")
	if err == nil || !strings.Contains(err.Error(), "connection refused") || !strings.Contains(err.Error(), addr) {
		t.Fatalf("first attempt: %v", err)
	}
	_, err = c.Command(ctx, "probe", "PING")
	if err == nil || !strings.Contains(err.Error(), "no new connection attempt") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("second call within the back-off: %v", err)
	}
	if c.attempts != 1 {
		t.Fatalf("%d connection attempts within the back-off, want 1", c.attempts)
	}
	time.Sleep(60 * time.Millisecond)
	_, _ = c.Command(ctx, "probe", "PING")
	_, _ = c.Command(ctx, "probe", "PING") // the wait has doubled to 100 ms
	if c.attempts != 2 {
		t.Fatalf("%d connection attempts, want 2", c.attempts)
	}

	// Many failed attempts in a row leave no goroutine behind (a failed dxlib Connect would leave its ring's
	// health check running, one per attempt).
	c.RetryMin, c.RetryMax = time.Millisecond, time.Millisecond
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Millisecond)
		_, _ = c.Command(ctx, "probe", "PING")
	}
	if c.attempts < 10 {
		t.Fatalf("only %d attempts made", c.attempts)
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutines grew from %d to %d over %d failed attempts", before, after, c.attempts)
	}
}

// The tests below need a real Valkey, as tools/test-valkey.sh starts one; without it they are skipped.
func valkeyUser(t *testing.T) (addr string, admin *goredis.Client, user, password string) {
	t.Helper()
	addr = os.Getenv("DXLIB_MODULE_TEST_VALKEY")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_VALKEY is not set; run tools/test-valkey.sh")
	}
	admin = goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD")})
	t.Cleanup(func() { _ = admin.Close() })
	ctx := context.Background()
	user, password = "cred"+uuid.NewString()[:8], "pw-"+uuid.NewString()
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "on", ">"+password, "resetkeys", "~"+user+":*", "resetchannels", "-@all", "+ping").Err(); err != nil {
		t.Fatalf("ACL SETUSER on Valkey at %s: %v", addr, err)
	}
	t.Cleanup(func() { admin.Do(context.Background(), "ACL", "DELUSER", user) })
	return addr, admin, user, password
}

func rotateOnServer(t *testing.T, admin *goredis.Client, user string) string {
	t.Helper()
	ctx := context.Background()
	pw := "pw-" + uuid.NewString()
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "resetpass", ">"+pw).Err(); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "CLIENT", "KILL", "USER", user).Err(); err != nil {
		t.Fatal(err)
	}
	return pw
}

// After a rotation, calls refused at the same time reconnect once between them; while the provider still returns
// the refused credential, no new connection is made with it.
func TestValkeyRefusedCredentialReconnectsOnce(t *testing.T) {
	addr, admin, user, pw := valkeyUser(t)
	provider := NewFixed(map[string]Credential{"v": {Username: user, Password: pw, Version: "1"}})
	c := NewRedisConnection("x", addr, 0, "v", provider)
	c.CloseGrace, c.RetryMin = 10*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if _, err := c.Command(ctx, "ping", "PING"); err != nil {
		t.Fatal(err)
	}

	pw2 := rotateOnServer(t, admin, user)
	provider.Set("v", Credential{Username: user, Password: pw2, Version: "2"})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Command(ctx, "ping", "PING"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("call after the rotation: %v", err)
	}
	if c.attempts != 2 || c.CredentialVersion() != "2" {
		t.Fatalf("%d connection attempts (want 2), credential version %q (want 2)", c.attempts, c.CredentialVersion())
	}

	// The server takes a new password the provider does not have yet.
	pw3 := rotateOnServer(t, admin, user)
	_, err := c.Command(ctx, "ping", "PING")
	if err == nil || !strings.Contains(err.Error(), "WRONGPASS") || !strings.Contains(err.Error(), "still returns") {
		t.Fatalf("call with the provider behind the server: %v", err)
	}
	_, err = c.Command(ctx, "ping", "PING")
	if err == nil || !strings.Contains(err.Error(), "no new connection attempt") {
		t.Fatalf("second call within the back-off: %v", err)
	}
	if err := c.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "no new connection attempt") {
		t.Fatalf("Refresh within the back-off: %v, want the last failure", err)
	}
	if c.attempts != 2 {
		t.Fatalf("%d connection attempts with the refused credential, want none beyond the first 2", c.attempts)
	}

	provider.Set("v", Credential{Username: user, Password: pw3, Version: "3"})
	time.Sleep(110 * time.Millisecond)
	if _, err := c.Command(ctx, "ping", "PING"); err != nil {
		t.Fatalf("call once the provider has the new credential: %v", err)
	}
	if c.attempts != 3 || c.CredentialVersion() != "3" {
		t.Fatalf("%d connection attempts (want 3), credential version %q (want 3)", c.attempts, c.CredentialVersion())
	}
}
