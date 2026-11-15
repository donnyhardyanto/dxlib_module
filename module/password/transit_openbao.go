package password

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/donnyhardyanto/dxlib_module/module/credentials"
)

// OpenBaoConfig names the transit key. The token comes from the credential provider, never from here.
type OpenBaoConfig struct {
	// Address is the server's base URL, for example "https://bao.example.test:8200".
	Address string
	// Mount is the transit engine's mount path. Default "transit".
	Mount string
	// Key is the transit key's name. Create it with type=aes256-gcm96 and exportable=false.
	Key string
	// Namespace is sent as X-Vault-Namespace when set.
	Namespace string
	// CredentialName is the name the token is asked for from the credentials.Provider; the token is the
	// credential's Password. Its policy needs update on <mount>/encrypt/<key>, <mount>/decrypt/<key> and
	// <mount>/rewrap/<key>, and read on <mount>/keys/<key>.
	CredentialName string
	// HTTPClient carries the TLS settings. Default: a client with a 10-second timeout.
	HTTPClient *http.Client
	// AllowHTTP permits an http:// Address. For tests against a local dev server only: the token and the hashes
	// would cross the network in clear.
	AllowHTTP bool
}

// OpenBaoTransit is a Transit on an OpenBao (or Vault) transit key, spoken to over its HTTP API.
type OpenBaoTransit struct {
	cfg      OpenBaoConfig
	base     *url.URL
	client   *http.Client
	provider credentials.Provider

	mu    sync.Mutex
	token string
}

var pathSegment = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)
var mountPath = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}(/[A-Za-z0-9_.\-]{1,128}){0,3}$`)

// maxBaoResponseBytes bounds one answer of the server.
const maxBaoResponseBytes = 1 << 20

// NewOpenBaoTransit returns a Transit on the key. It makes no request; call Check at start-up to verify the key.
func NewOpenBaoTransit(cfg OpenBaoConfig, provider credentials.Provider) (*OpenBaoTransit, error) {
	if provider == nil {
		return nil, errors.New("password: NewOpenBaoTransit needs a credentials.Provider for the token")
	}
	if cfg.Mount == "" {
		cfg.Mount = "transit"
	}
	if !mountPath.MatchString(cfg.Mount) || hasDotSegment(cfg.Mount) {
		return nil, fmt.Errorf("password: OpenBaoConfig.Mount %q must be up to four segments of letters, digits, _ . -", truncate(cfg.Mount, 140))
	}
	if !pathSegment.MatchString(cfg.Key) || cfg.Key == "." || cfg.Key == ".." {
		return nil, fmt.Errorf("password: OpenBaoConfig.Key %q must be 1 to 128 letters, digits, _ . -", truncate(cfg.Key, 140))
	}
	if cfg.CredentialName == "" {
		return nil, errors.New("password: OpenBaoConfig.CredentialName is empty")
	}
	u, err := url.Parse(cfg.Address)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("password: OpenBaoConfig.Address %q must be an https URL with a host and no query", truncate(cfg.Address, 200))
	}
	if u.Scheme == "http" && !cfg.AllowHTTP {
		return nil, fmt.Errorf("password: OpenBaoConfig.Address %q is http; use https (AllowHTTP is for a local test server only)", truncate(cfg.Address, 200))
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OpenBaoTransit{cfg: cfg, base: u, client: client, provider: provider}, nil
}

// baoError is a failed request: the operation, the HTTP status and the server's own messages.
type baoError struct {
	op       string
	status   int
	messages []string
	err      error
}

func (e *baoError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("openbao transit %s: %v", e.op, e.err)
	}
	msg := strings.Join(e.messages, "; ")
	if msg == "" {
		msg = http.StatusText(e.status)
	}
	return fmt.Sprintf("openbao transit %s: HTTP %d: %s", e.op, e.status, msg)
}

func (e *baoError) Unwrap() error { return e.err }

func (t *OpenBaoTransit) currentToken(ctx context.Context, refresh bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && !refresh {
		return t.token, nil
	}
	c, err := t.provider.Credential(ctx, t.cfg.CredentialName)
	if err != nil {
		return "", fmt.Errorf("asking the credential provider for %q: %w", t.cfg.CredentialName, err)
	}
	if c.Password == "" {
		return "", fmt.Errorf("the credential provider returned an empty token for %q", t.cfg.CredentialName)
	}
	t.token = c.Password
	return t.token, nil
}

// do sends one request and decodes the answer's "data" into out. On HTTP 403 it asks the provider again and, when
// the token changed (rotated or renewed), retries once.
func (t *OpenBaoTransit) do(ctx context.Context, method, op, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return &baoError{op: op, err: err}
		}
	}
	token, err := t.currentToken(ctx, false)
	if err != nil {
		return &baoError{op: op, err: err}
	}
	for attempt := 0; ; attempt++ {
		status, raw, err := t.send(ctx, method, path, token, payload)
		if err != nil {
			return &baoError{op: op, err: err}
		}
		if status == http.StatusForbidden && attempt == 0 {
			fresh, ferr := t.currentToken(ctx, true)
			if ferr != nil {
				return &baoError{op: op, err: fmt.Errorf("HTTP 403, then %w", ferr)}
			}
			if fresh != token {
				token = fresh
				continue
			}
		}
		if status < 200 || status > 299 {
			var e struct {
				Errors []string `json:"errors"`
			}
			_ = json.Unmarshal(raw, &e)
			return &baoError{op: op, status: status, messages: e.Errors}
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
			return &baoError{op: op, err: fmt.Errorf("HTTP %d with an answer that has no data object", status)}
		}
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return &baoError{op: op, err: fmt.Errorf("decoding the answer's data: %w", err)}
		}
		return nil
	}
}

func (t *OpenBaoTransit) send(ctx context.Context, method, path, token string, payload []byte) (int, []byte, error) {
	endpoint := t.base.JoinPath("v1", t.cfg.Mount, path, t.cfg.Key).String()
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("X-Vault-Request", "true")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.cfg.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", t.cfg.Namespace)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBaoResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s %s: reading the answer (HTTP %d): %w", method, endpoint, resp.StatusCode, err)
	}
	if len(raw) > maxBaoResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("%s %s: the answer is larger than %d bytes", method, endpoint, maxBaoResponseBytes)
	}
	return resp.StatusCode, raw, nil
}

type baoCiphertext struct {
	Ciphertext string `json:"ciphertext"`
	KeyVersion int    `json:"key_version"`
}

func (t *OpenBaoTransit) ciphertext(op string, r baoCiphertext) (Ciphertext, error) {
	v, err := keyVersionOf(r.Ciphertext)
	if err != nil {
		return Ciphertext{}, &baoError{op: op, err: err}
	}
	if r.KeyVersion != 0 && r.KeyVersion != v {
		return Ciphertext{}, &baoError{op: op, err: fmt.Errorf("the answer names key version %d but its ciphertext version %d", r.KeyVersion, v)}
	}
	return Ciphertext{Text: r.Ciphertext, KeyVersion: v}, nil
}

// Encrypt encrypts with the key's newest version.
func (t *OpenBaoTransit) Encrypt(ctx context.Context, plaintext []byte) (Ciphertext, error) {
	var r baoCiphertext
	if err := t.do(ctx, http.MethodPost, "encrypt key "+t.cfg.Key, "encrypt", map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)}, &r); err != nil {
		return Ciphertext{}, err
	}
	return t.ciphertext("encrypt key "+t.cfg.Key, r)
}

// Decrypt decrypts a ciphertext of this key.
func (t *OpenBaoTransit) Decrypt(ctx context.Context, ciphertext string) ([]byte, error) {
	var r struct {
		Plaintext string `json:"plaintext"`
	}
	if err := t.do(ctx, http.MethodPost, "decrypt key "+t.cfg.Key, "decrypt", map[string]string{"ciphertext": ciphertext}, &r); err != nil {
		return nil, err
	}
	pt, err := base64.StdEncoding.DecodeString(r.Plaintext)
	if err != nil {
		return nil, &baoError{op: "decrypt key " + t.cfg.Key, err: fmt.Errorf("the plaintext in the answer is not base64: %w", err)}
	}
	return pt, nil
}

// Rewrap re-encrypts with the key's newest version.
func (t *OpenBaoTransit) Rewrap(ctx context.Context, ciphertext string) (Ciphertext, error) {
	var r baoCiphertext
	if err := t.do(ctx, http.MethodPost, "rewrap key "+t.cfg.Key, "rewrap", map[string]string{"ciphertext": ciphertext}, &r); err != nil {
		return Ciphertext{}, err
	}
	return t.ciphertext("rewrap key "+t.cfg.Key, r)
}

// KeyInfo is what the server says about the key.
type KeyInfo struct {
	Type                 string `json:"type"`
	Exportable           bool   `json:"exportable"`
	AllowPlaintextBackup bool   `json:"allow_plaintext_backup"`
	Derived              bool   `json:"derived"`
	LatestVersion        int    `json:"latest_version"`
	MinDecryptionVersion int    `json:"min_decryption_version"`
	MinEncryptionVersion int    `json:"min_encryption_version"`
}

// Key reads the key's settings.
func (t *OpenBaoTransit) Key(ctx context.Context) (KeyInfo, error) {
	var k KeyInfo
	err := t.do(ctx, http.MethodGet, "read key "+t.cfg.Key, "keys", nil, &k)
	return k, err
}

// LatestVersion is the key's newest version.
func (t *OpenBaoTransit) LatestVersion(ctx context.Context) (int, error) {
	k, err := t.Key(ctx)
	if err != nil {
		return 0, err
	}
	return k.LatestVersion, nil
}

// Check verifies the key is what this package needs: type aes256-gcm96, not exportable, no plaintext backup, not
// derived. Call it at start-up; it names every setting that is wrong.
func (t *OpenBaoTransit) Check(ctx context.Context) error {
	k, err := t.Key(ctx)
	if err != nil {
		return err
	}
	var wrong []string
	if k.Type != "aes256-gcm96" {
		wrong = append(wrong, fmt.Sprintf("type is %q, not aes256-gcm96", k.Type))
	}
	if k.Exportable {
		wrong = append(wrong, "it is exportable")
	}
	if k.AllowPlaintextBackup {
		wrong = append(wrong, "it allows a plaintext backup")
	}
	if k.Derived {
		wrong = append(wrong, "it is a derived key (this package sends no context)")
	}
	if k.LatestVersion < 1 {
		wrong = append(wrong, fmt.Sprintf("its latest version is %d", k.LatestVersion))
	}
	if len(wrong) > 0 {
		return fmt.Errorf("password: transit key %s at %s (mount %s) is not usable: %s", t.cfg.Key, t.base.Host, t.cfg.Mount, strings.Join(wrong, "; "))
	}
	return nil
}

var _ Transit = (*OpenBaoTransit)(nil)

func hasDotSegment(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return true
		}
	}
	return false
}
