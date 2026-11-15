package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	dxVault "github.com/donnyhardyanto/dxlib/vault"
	baoapi "github.com/hashicorp/vault/api"
)

// SourceKind says how an OpenBao source hands out its credential.
type SourceKind int

const (
	// SourceKV reads a username and a password from a KV version 2 secret. Version is the secret's KV version.
	SourceKV SourceKind = iota + 1
	// SourceDatabase issues a dynamic credential from a database secrets engine role. Version is the lease id and
	// ExpiresAt the lease's end; the lease is renewed while it can be and replaced when it cannot.
	SourceDatabase
)

// OpenBaoSource says where one named credential lives in OpenBao.
type OpenBaoSource struct {
	Kind SourceKind
	// Mount is the secrets engine's mount path, for example "secret" (KV) or "database".
	Mount string
	// Path is the KV secret's path under the mount ("valkey/sessions"), or the database role's name ("app").
	Path string
	// UsernameKey and PasswordKey name the KV secret's fields. Defaults "username" and "password". KV only.
	UsernameKey, PasswordKey string
}

// KV returns a source reading a KV version 2 secret at <mount>/data/<path>, with the fields "username" and
// "password". Its policy needs read on <mount>/data/<path>.
func KV(mount, path string) OpenBaoSource {
	return OpenBaoSource{Kind: SourceKV, Mount: mount, Path: path}
}

// DatabaseRole returns a source issuing dynamic credentials from <mount>/creds/<role>. Its policy needs read on
// <mount>/creds/<role>, update on sys/leases/renew and sys/leases/revoke, and the token's own update on
// auth/token/renew-self and read on auth/token/lookup-self.
func DatabaseRole(mount, role string) OpenBaoSource {
	return OpenBaoSource{Kind: SourceDatabase, Mount: mount, Path: role}
}

// OpenBaoOptions tunes an OpenBao provider. The zero value is usable.
type OpenBaoOptions struct {
	// RenewBefore is how long before a lease or the token ends it is renewed (or a lease replaced). Default one
	// minute; for a lease or token shorter than twice that, half its duration.
	RenewBefore time.Duration
	// Namespace is sent as X-Vault-Namespace. It replaces a VAULT_NAMESPACE the client read from the environment, so
	// the namespace is always the one the service set.
	Namespace string
	// TokenSource returns a new OpenBao token for this provider (a fresh AppRole or Kubernetes login) when the
	// current one is refused (HTTP 403) or can no longer be renewed. Optional: without it the token handed in with
	// the client is the only one, and the provider stops working when it ends.
	TokenSource func(ctx context.Context) (string, error)
	// AllowHTTP permits an http:// address. For tests against a local dev server only: the token and the
	// credentials would cross the network in clear.
	AllowHTTP bool
}

// OpenBao is a Provider backed by OpenBao (or Vault), reusing dxlib's vault client.
//
// KV credentials are read again on every call, so a holder always gets the newest version. Dynamic database
// credentials are cached per name: one lease serves every call until it is near its end, when it is renewed; once
// it cannot be renewed past RenewBefore (the role's max_ttl, a revoked lease) or a holder reported it refused
// (Invalidate), a new one is issued. The superseded lease is left to run out, so connections still using it finish.
//
// Every lease is a child of the provider's token, and OpenBao revokes them all when the token ends. The provider
// renews its token (auth/token/renew-self) when it is near its end, caps every ExpiresAt at the token's end, and
// asks TokenSource for a new token when the current one is refused or cannot be renewed. Use a periodic token (or
// an auth method that issues one) so renewal can go on for the life of the service.
type OpenBao struct {
	vault *dxVault.DXHashicorpVault
	opts  OpenBaoOptions

	sources map[string]OpenBaoSource

	tokenMu      sync.Mutex
	tokenKnown   bool
	tokenExpires time.Time // zero: the token does not expire
	tokenTTL     time.Duration
	tokenRenew   bool

	leaseMu sync.Mutex
	leases  map[string]*leaseEntry
	retired []retiredLease // superseded leases not yet ended, revoked by Close

}

type retiredLease struct {
	id  string
	end time.Time
}

type leaseEntry struct {
	mu       sync.Mutex // held across the network call, so one name issues one lease at a time
	cred     Credential
	leaseID  string
	duration time.Duration // the lease's duration as issued: the renewal asks for this again
	renew    bool
	end      time.Time // the lease's own end, before the cap at the token's end
}

var baoSegment = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)

func validBaoPath(p string, maxSegments int) bool {
	parts := strings.Split(p, "/")
	if len(parts) > maxSegments {
		return false
	}
	for _, s := range parts {
		if !baoSegment.MatchString(s) || s == "." || s == ".." {
			return false
		}
	}
	return true
}

// NewOpenBao returns a provider for the named sources, speaking through v's client (started here if it was not).
// It makes no request; call Check at start-up to verify the token and every source.
func NewOpenBao(v *dxVault.DXHashicorpVault, sources map[string]OpenBaoSource, opts OpenBaoOptions) (*OpenBao, error) {
	if v == nil {
		return nil, errors.New("credentials: NewOpenBao needs a dxlib vault client (vault.NewHashiCorpVault)")
	}
	u, err := url.Parse(v.Address)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("credentials: OpenBao address %q must be an https URL with a host", v.Address)
	}
	if u.Scheme == "http" && !opts.AllowHTTP {
		return nil, fmt.Errorf("credentials: OpenBao address %q is http: the token and every credential would cross the network in clear; use https (OpenBaoOptions.AllowHTTP is for a local test server only)", v.Address)
	}
	if len(sources) == 0 {
		return nil, errors.New("credentials: NewOpenBao has no sources")
	}
	src := map[string]OpenBaoSource{}
	for name, s := range sources {
		if name == "" {
			return nil, errors.New("credentials: an OpenBao source has an empty name")
		}
		if !validBaoPath(s.Mount, 4) {
			return nil, fmt.Errorf("credentials: source %q: mount %q must be up to four segments of letters, digits, _ . -", name, truncateText(s.Mount, 140))
		}
		switch s.Kind {
		case SourceKV:
			if !validBaoPath(s.Path, 8) {
				return nil, fmt.Errorf("credentials: source %q: KV path %q must be up to eight segments of letters, digits, _ . -", name, truncateText(s.Path, 140))
			}
			if s.UsernameKey == "" {
				s.UsernameKey = "username"
			}
			if s.PasswordKey == "" {
				s.PasswordKey = "password"
			}
		case SourceDatabase:
			if !validBaoPath(s.Path, 1) {
				return nil, fmt.Errorf("credentials: source %q: database role %q must be 1 to 128 letters, digits, _ . -", name, truncateText(s.Path, 140))
			}
		default:
			return nil, fmt.Errorf("credentials: source %q has no kind (use credentials.KV or credentials.DatabaseRole)", name)
		}
		src[name] = s
	}
	if v.Client == nil {
		if err := v.Start(); err != nil {
			return nil, fmt.Errorf("credentials: starting the vault client for %s: %w", v.Address, err)
		}
	}
	// The vault client reads VAULT_SKIP_VERIFY from the environment; never accept a client that does not check the
	// server's certificate.
	if cfg := v.Client.CloneConfig(); cfg != nil && cfg.HttpClient != nil {
		if tr, ok := cfg.HttpClient.Transport.(*http.Transport); ok && tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
			return nil, errors.New("credentials: the vault client does not verify OpenBao's TLS certificate (VAULT_SKIP_VERIFY is set in the environment); unset it and give the CA with VAULT_CACERT")
		}
	}
	if opts.Namespace != "" {
		v.Client.SetNamespace(opts.Namespace)
	} else {
		v.Client.ClearNamespace()
	}
	return &OpenBao{vault: v, opts: opts, sources: src, leases: map[string]*leaseEntry{}}, nil
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// threshold is how long before end something lasting d is renewed: RenewBefore, or half of d when d is short.
func (o *OpenBao) threshold(d time.Duration) time.Duration {
	t := orDefault(o.opts.RenewBefore, time.Minute)
	if d > 0 && t > d/2 {
		t = d / 2
	}
	return t
}

func (o *OpenBao) describe() string { return "OpenBao at " + o.vault.Address }

func statusOf(err error) int {
	var re *baoapi.ResponseError
	if errors.As(err, &re) {
		return re.StatusCode
	}
	return 0
}

// call runs fn; when OpenBao refuses the token (HTTP 403) and a TokenSource is set, it takes a new token and runs fn
// once more.
func (o *OpenBao) call(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil || statusOf(err) != http.StatusForbidden || o.opts.TokenSource == nil {
		return err
	}
	if terr := o.replaceToken(ctx); terr != nil {
		return fmt.Errorf("%w; taking a new token from the TokenSource after HTTP 403 failed too: %v", err, terr)
	}
	return fn()
}

// replaceToken takes a new token from the TokenSource and reads its lifetime. It does not retry: a new token that is
// refused too is reported.
func (o *OpenBao) replaceToken(ctx context.Context) error {
	tok, err := o.opts.TokenSource(ctx)
	if err != nil {
		return err
	}
	if tok == "" {
		return errors.New("the TokenSource returned an empty token")
	}
	o.vault.Client.SetToken(tok)
	o.vault.Token = tok
	o.tokenMu.Lock()
	defer o.tokenMu.Unlock()
	o.tokenKnown = false
	if err := o.lookupToken(ctx); err != nil {
		return fmt.Errorf("the new token from the TokenSource: %w", err)
	}
	return nil
}

// lookupToken reads the token's lifetime (auth/token/lookup-self). Called with tokenMu held.
func (o *OpenBao) lookupToken(ctx context.Context) error {
	sec, err := o.vault.Client.Auth().Token().LookupSelfWithContext(ctx)
	if err != nil {
		return fmt.Errorf("reading the provider's own token (auth/token/lookup-self): %w", err)
	}
	if err := o.learnToken(sec); err != nil {
		return fmt.Errorf("auth/token/lookup-self: %w", err)
	}
	return nil
}

// ensureToken learns the token's lifetime once and renews the token when it is near its end; a token that is
// refused, or cannot be renewed far enough, is replaced from the TokenSource when one is set.
func (o *OpenBao) ensureToken(ctx context.Context) error {
	o.tokenMu.Lock()
	if !o.tokenKnown {
		if err := o.lookupToken(ctx); err != nil {
			o.tokenMu.Unlock()
			if statusOf(err) == http.StatusForbidden && o.opts.TokenSource != nil {
				if terr := o.replaceToken(ctx); terr != nil {
					return fmt.Errorf("%s: %v; taking a new token from the TokenSource failed: %w", o.describe(), err, terr)
				}
				return nil
			}
			return fmt.Errorf("%s: %w", o.describe(), err)
		}
	}
	if o.tokenExpires.IsZero() || time.Until(o.tokenExpires) >= o.threshold(o.tokenTTL) {
		o.tokenMu.Unlock()
		return nil
	}
	var renewErr error
	if o.tokenRenew {
		inc := int(o.tokenTTL / time.Second)
		sec, err := o.vault.Client.Auth().Token().RenewSelfWithContext(ctx, inc)
		if err == nil {
			ttl, terr := sec.TokenTTL()
			if terr == nil && ttl > 0 {
				o.tokenExpires = time.Now().Add(ttl)
			}
			renewErr = terr
		} else {
			renewErr = err
		}
		if renewErr == nil && time.Until(o.tokenExpires) >= o.threshold(o.tokenTTL) {
			o.tokenMu.Unlock()
			return nil
		}
	} else {
		renewErr = errors.New("the token is not renewable")
	}
	expires := o.tokenExpires
	o.tokenMu.Unlock()
	if o.opts.TokenSource != nil {
		if err := o.replaceToken(ctx); err != nil {
			return fmt.Errorf("%s: the provider's token ends at %s and could not be renewed (%v); taking a new one from the TokenSource failed: %w",
				o.describe(), expires.Format(time.RFC3339), renewErr, err)
		}
		return nil
	}
	if time.Now().After(expires) {
		return fmt.Errorf("%s: the provider's token ended at %s and could not be renewed (%v), and no TokenSource is set to take a new one",
			o.describe(), expires.Format(time.RFC3339), renewErr)
	}
	// Still valid for a while: go on; every ExpiresAt is capped at the token's end.
	return nil
}

// learnToken records a lookup-self answer. Called with tokenMu held.
func (o *OpenBao) learnToken(sec *baoapi.Secret) error {
	if sec == nil || sec.Data == nil {
		return errors.New("empty answer")
	}
	ttl, err := sec.TokenTTL()
	if err != nil {
		return err
	}
	renewable, _ := sec.TokenIsRenewable()
	o.tokenRenew = renewable
	o.tokenTTL = 0
	if v, ok := sec.Data["creation_ttl"].(json.Number); ok {
		if n, err := v.Int64(); err == nil {
			o.tokenTTL = time.Duration(n) * time.Second
		}
	}
	if p, ok := sec.Data["period"].(json.Number); ok {
		if n, err := p.Int64(); err == nil && n > 0 {
			o.tokenTTL = time.Duration(n) * time.Second
		}
	}
	if ttl > 0 {
		o.tokenExpires = time.Now().Add(ttl)
		if o.tokenTTL == 0 {
			o.tokenTTL = ttl
		}
	} else {
		o.tokenExpires = time.Time{}
	}
	o.tokenKnown = true
	return nil
}

func (o *OpenBao) tokenEnd() time.Time {
	o.tokenMu.Lock()
	defer o.tokenMu.Unlock()
	return o.tokenExpires
}

// Credential returns the current credential for name.
func (o *OpenBao) Credential(ctx context.Context, name string) (Credential, error) {
	s, ok := o.sources[name]
	if !ok {
		return Credential{}, fmt.Errorf("credentials: no OpenBao source named %q", name)
	}
	if err := o.ensureToken(ctx); err != nil {
		return Credential{}, err
	}
	if s.Kind == SourceKV {
		return o.readKV(ctx, name, s)
	}
	return o.databaseCredential(ctx, name, s)
}

func (o *OpenBao) readKV(ctx context.Context, name string, s OpenBaoSource) (Credential, error) {
	path := s.Mount + "/data/" + s.Path
	var sec *baoapi.Secret
	err := o.call(ctx, func() (err error) {
		sec, err = o.vault.Client.Logical().ReadWithContext(ctx, path)
		return err
	})
	if err != nil {
		return Credential{}, fmt.Errorf("%s: reading credential %q from KV %s: %w", o.describe(), name, path, err)
	}
	if sec == nil || sec.Data == nil {
		return Credential{}, fmt.Errorf("%s: credential %q: KV %s does not exist (or its latest version is deleted)", o.describe(), name, path)
	}
	data, _ := sec.Data["data"].(map[string]interface{})
	meta, _ := sec.Data["metadata"].(map[string]interface{})
	if data == nil {
		return Credential{}, fmt.Errorf("%s: credential %q: KV %s has no data in its latest version (deleted or destroyed); write a new version", o.describe(), name, path)
	}
	user, uok := data[s.UsernameKey].(string)
	pass, pok := data[s.PasswordKey].(string)
	if !uok || !pok || pass == "" {
		return Credential{}, fmt.Errorf("%s: credential %q: KV %s needs string fields %q and %q (a non-empty password)", o.describe(), name, path, s.UsernameKey, s.PasswordKey)
	}
	version := ""
	if meta != nil {
		if v, ok := meta["version"].(json.Number); ok {
			version = v.String()
		}
	}
	if version == "" {
		return Credential{}, fmt.Errorf("%s: credential %q: KV %s has no metadata.version; is the mount KV version 1? Only version 2 is supported", o.describe(), name, path)
	}
	return Credential{Username: user, Password: pass, Version: version}, nil
}

func (o *OpenBao) entry(name string) *leaseEntry {
	o.leaseMu.Lock()
	defer o.leaseMu.Unlock()
	e, ok := o.leases[name]
	if !ok {
		e = &leaseEntry{}
		o.leases[name] = e
	}
	return e
}

// capped is the lease's credential with ExpiresAt no later than the token's end.
func (o *OpenBao) capped(e *leaseEntry) Credential {
	c := e.cred
	c.ExpiresAt = e.end
	if te := o.tokenEnd(); !te.IsZero() && te.Before(c.ExpiresAt) {
		c.ExpiresAt = te
	}
	return c
}

func (o *OpenBao) databaseCredential(ctx context.Context, name string, s OpenBaoSource) (Credential, error) {
	e := o.entry(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	var renewErr error
	if e.leaseID != "" && now.Before(e.end) {
		if e.end.Sub(now) >= o.threshold(e.duration) {
			return o.capped(e), nil
		}
		if e.renew {
			var sec *baoapi.Secret
			err := o.call(ctx, func() (err error) {
				sec, err = o.vault.Client.Sys().RenewWithContext(ctx, e.leaseID, int(e.duration/time.Second))
				return err
			})
			if err == nil && sec != nil && sec.LeaseDuration > 0 {
				e.end = time.Now().Add(time.Duration(sec.LeaseDuration) * time.Second)
				if e.end.Sub(time.Now()) >= o.threshold(e.duration) {
					return o.capped(e), nil
				}
				renewErr = fmt.Errorf("renewing lease %s reached its max_ttl (it ends %s)", e.leaseID, e.end.Format(time.RFC3339))
			} else if err != nil {
				renewErr = fmt.Errorf("renewing lease %s: %w", e.leaseID, err)
			}
			// Renewal failed or reached max_ttl: issue a new lease below, leaving this one to run out.
		}
	}
	path := s.Mount + "/creds/" + s.Path
	var sec *baoapi.Secret
	err := o.call(ctx, func() (err error) {
		sec, err = o.vault.Client.Logical().ReadWithContext(ctx, path)
		return err
	})
	if err != nil {
		if renewErr != nil {
			return Credential{}, fmt.Errorf("%s: issuing credential %q from %s after %v: %w", o.describe(), name, path, renewErr, err)
		}
		return Credential{}, fmt.Errorf("%s: issuing credential %q from %s: %w", o.describe(), name, path, err)
	}
	if sec == nil || sec.LeaseID == "" || sec.LeaseDuration <= 0 {
		return Credential{}, fmt.Errorf("%s: credential %q: %s answered without a lease; is %s a database secrets engine role?", o.describe(), name, path, path)
	}
	user, _ := sec.Data["username"].(string)
	pass, _ := sec.Data["password"].(string)
	if user == "" || pass == "" {
		return Credential{}, fmt.Errorf("%s: credential %q: %s answered without a username and password (lease %s)", o.describe(), name, path, sec.LeaseID)
	}
	o.retire(e)
	got := time.Now()
	e.leaseID = sec.LeaseID
	e.duration = time.Duration(sec.LeaseDuration) * time.Second
	e.renew = sec.Renewable
	e.end = got.Add(e.duration)
	e.cred = Credential{Username: user, Password: pass, Version: sec.LeaseID}
	return o.capped(e), nil
}

// retire keeps a superseded lease for Close, and forgets the ones that have ended. Called with e.mu held.
func (o *OpenBao) retire(e *leaseEntry) {
	if e.leaseID == "" {
		return
	}
	o.leaseMu.Lock()
	defer o.leaseMu.Unlock()
	now := time.Now()
	kept := o.retired[:0]
	for _, r := range o.retired {
		if r.end.After(now) {
			kept = append(kept, r)
		}
	}
	o.retired = append(kept, retiredLease{id: e.leaseID, end: e.end})
}

// Invalidate tells the provider the server refused version of name's credential, so the next Credential call issues
// a new database lease instead of returning the cached one. KV credentials are read fresh anyway.
func (o *OpenBao) Invalidate(name, version string) {
	o.leaseMu.Lock()
	e, ok := o.leases[name]
	o.leaseMu.Unlock()
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.leaseID != "" && e.leaseID == version {
		o.retire(e)
		e.leaseID = ""
		e.end = time.Time{}
	}
}

// Check verifies the token and every source at start-up: it reads each KV secret and issues each database
// credential (kept as the first lease). The error names every source that failed.
func (o *OpenBao) Check(ctx context.Context) error {
	var problems []string
	for name := range o.sources {
		if _, err := o.Credential(ctx, name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("credentials: OpenBao check failed for %d of %d sources: %s", len(problems), len(o.sources), strings.Join(problems, "; "))
	}
	return nil
}

// Close revokes every database lease the provider issued (the current ones and the superseded ones still running),
// so no dynamic user outlives the service. Call it after closing the connections that use them. It returns the
// failures, naming each lease.
func (o *OpenBao) Close(ctx context.Context) error {
	// Lock order is entry, then leaseMu: take the entries first, then each one's lock on its own.
	o.leaseMu.Lock()
	entries := make([]*leaseEntry, 0, len(o.leases))
	for _, e := range o.leases {
		entries = append(entries, e)
	}
	o.leaseMu.Unlock()
	var ids []string
	for _, e := range entries {
		e.mu.Lock()
		if e.leaseID != "" {
			ids = append(ids, e.leaseID)
			e.leaseID = ""
			e.end = time.Time{}
		}
		e.mu.Unlock()
	}
	o.leaseMu.Lock()
	for _, r := range o.retired {
		ids = append(ids, r.id)
	}
	o.retired = nil
	o.leaseMu.Unlock()
	var problems []string
	for _, id := range ids {
		err := o.call(ctx, func() error { return o.vault.Client.Sys().RevokeWithContext(ctx, id) })
		if err != nil && statusOf(err) != http.StatusBadRequest { // 400: the lease has already ended
			problems = append(problems, fmt.Sprintf("lease %s: %v", id, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: revoking %d of %d leases failed: %s", o.describe(), len(problems), len(ids), strings.Join(problems, "; "))
	}
	return nil
}

var _ Provider = (*OpenBao)(nil)
var _ Invalidator = (*OpenBao)(nil)
