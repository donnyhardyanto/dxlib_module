// Package credentials hands the login framework's parts the credentials they connect with.
//
// Every part (session store, password store, photo store, audit store) takes its Valkey, PostgreSQL and
// object-storage credentials through one Provider, never from configuration values or the environment.
// A service ships a Provider backed by its secret store (OpenBao or Vault); Fixed is for tests only.
package credentials

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Credential is one set of connection credentials.
type Credential struct {
	Username string
	Password string
	// ExpiresAt is when the credential stops working (a dynamic credential's lease end). Zero: no expiry known.
	ExpiresAt time.Time
	// Version names this issue of the credential (a lease id, a KV version), so a holder can tell a rotation.
	Version string
}

// String never prints the password.
func (c Credential) String() string {
	return fmt.Sprintf("credential{user=%q version=%q expires=%s}", c.Username, c.Version, c.ExpiresAt.Format(time.RFC3339))
}

// GoString keeps %#v from printing the password too.
func (c Credential) GoString() string { return c.String() }

// Provider returns the current credential for a name (for example "valkey/sessions").
// It must return the newest credential each time it is called, so a holder that sees an authentication error can
// call it again and reconnect.
type Provider interface {
	Credential(ctx context.Context, name string) (Credential, error)
}

// Fixed is a Provider holding credentials in memory. It is for tests only: a service never puts credentials in
// configuration or the environment.
type Fixed struct {
	mu    sync.RWMutex
	creds map[string]Credential
}

// NewFixed returns a Fixed provider holding the given credentials.
func NewFixed(creds map[string]Credential) *Fixed {
	f := &Fixed{creds: map[string]Credential{}}
	for k, v := range creds {
		f.creds[k] = v
	}
	return f
}

// Set replaces one credential, as a rotation in the secret store would.
func (f *Fixed) Set(name string, c Credential) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creds[name] = c
}

// Credential returns the credential stored under name.
func (f *Fixed) Credential(_ context.Context, name string) (Credential, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c, ok := f.creds[name]
	if !ok {
		return Credential{}, fmt.Errorf("credentials: no credential named %q in the fixed test provider", name)
	}
	return c, nil
}
