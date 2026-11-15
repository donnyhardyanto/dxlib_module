package user_photo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/donnyhardyanto/dxlib_module/module/credentials"
)

// S3Config names the bucket. The credentials come from a credentials.Provider, never from here.
type S3Config struct {
	// Endpoint is host:port of the S3 API, for example "seaweedfs-s3:8333".
	Endpoint string
	// Secure uses HTTPS. Leave it off only on a private network.
	Secure bool
	// Region is sent in signatures. Default "us-east-1".
	Region string
	// Bucket must exist and refuse anonymous requests (Check); this package never creates it.
	Bucket string
	// CredentialName is asked of the provider, for example "s3/user-photos": Username is the access key id,
	// Password the secret key, ExpiresAt when they stop working.
	CredentialName string
	// Transport carries the requests, for TLS settings; default http.DefaultTransport.
	Transport http.RoundTripper
	// Timeout bounds one request. Default 30 seconds.
	Timeout time.Duration
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`)

// S3Store is an ObjectStore on a bucket of an S3 API (tested against SeaweedFS), through minio-go. Every request
// is signed with the provider's current credential; a request refused for its credential asks the provider again
// and is tried once more, so a rotated key is picked up without a restart.
type S3Store struct {
	cfg    S3Config
	client *minio.Client
	creds  *miniocreds.Credentials
	http   *http.Client
}

var _ ObjectStore = (*S3Store)(nil)

// NewS3Store returns a store on the bucket. It sends nothing; call Check at start-up.
func NewS3Store(cfg S3Config, provider credentials.Provider) (*S3Store, error) {
	if provider == nil {
		return nil, errors.New("user_photo: NewS3Store needs a credentials.Provider")
	}
	if cfg.Endpoint == "" || cfg.CredentialName == "" {
		return nil, errors.New("user_photo: S3Config.Endpoint and S3Config.CredentialName must be set")
	}
	if !bucketPattern.MatchString(cfg.Bucket) {
		return nil, fmt.Errorf("user_photo: S3Config.Bucket %q is not a bucket name (3 to 63 lower-case letters, digits, . or -)", truncate(cfg.Bucket, 70))
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	creds := miniocreds.New(&providerCreds{provider: provider, name: cfg.CredentialName})
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.Secure, Region: cfg.Region,
		Transport: cfg.Transport, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, fmt.Errorf("user_photo: S3 client for %s: %w", cfg.Endpoint, err)
	}
	return &S3Store{cfg: cfg, client: client, creds: creds, http: &http.Client{Transport: cfg.Transport, Timeout: cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// providerCreds hands minio-go the provider's credential, and asks again once it is near its expiry or after
// Expire (a refused request).
type providerCreds struct {
	provider credentials.Provider
	name     string
	mu       sync.Mutex
	expires  time.Time
}

func (p *providerCreds) RetrieveWithCredContext(cc *miniocreds.CredContext) (miniocreds.Value, error) {
	ctx := context.Background()
	if cc != nil && cc.Context != nil {
		ctx = cc.Context
	}
	c, err := p.provider.Credential(ctx, p.name)
	if err != nil {
		return miniocreds.Value{}, fmt.Errorf("user_photo: credential %q from the provider: %w", p.name, err)
	}
	if c.Username == "" || c.Password == "" {
		return miniocreds.Value{}, fmt.Errorf("user_photo: credential %q from the provider has no access key or no secret key (%s)", p.name, c)
	}
	p.mu.Lock()
	p.expires = c.ExpiresAt
	p.mu.Unlock()
	return miniocreds.Value{AccessKeyID: c.Username, SecretAccessKey: c.Password, Expiration: c.ExpiresAt, SignerType: miniocreds.SignatureV4}, nil
}

func (p *providerCreds) Retrieve() (miniocreds.Value, error) { return p.RetrieveWithCredContext(nil) }

// IsExpired is true from 30 seconds before the credential's expiry, so no request is signed with a dying key.
func (p *providerCreds) IsExpired() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.expires.IsZero() && time.Now().Add(30*time.Second).After(p.expires)
}

// credentialRefused tells an answer that a fresh credential could fix.
func credentialRefused(err error) bool {
	switch minio.ToErrorResponse(err).Code {
	case "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied", "ExpiredToken", "InvalidToken", "InvalidAccessKeyID":
		return true
	}
	return false
}

// do runs op, and once more with a credential asked for again when the first answer refused the credential.
func (s *S3Store) do(ctx context.Context, op func(ctx context.Context) error) error {
	cctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	err := op(cctx)
	cancel()
	if err != nil && credentialRefused(err) && ctx.Err() == nil {
		s.creds.Expire()
		cctx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
		err = op(cctx)
		cancel()
	}
	return err
}

func (s *S3Store) describe(op, key string, err error) error {
	r := minio.ToErrorResponse(err)
	if r.Code != "" {
		return fmt.Errorf("user_photo: S3 %s of %s/%s at %s refused: HTTP %d %s: %s: %w", op, s.cfg.Bucket, key, s.cfg.Endpoint, r.StatusCode, r.Code, r.Message, err)
	}
	return fmt.Errorf("user_photo: S3 %s of %s/%s at %s failed: %w", op, s.cfg.Bucket, key, s.cfg.Endpoint, err)
}

// Put stores data under key, in one request.
func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	err := s.do(ctx, func(ctx context.Context) error {
		_, err := s.client.PutObject(ctx, s.cfg.Bucket, key, bytes.NewReader(data), int64(len(data)),
			minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true})
		return err
	})
	if err != nil {
		return s.describe("put", key, err)
	}
	return nil
}

// Get returns the object's bytes. ErrNotFound when there is no such object.
func (s *S3Store) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	var data []byte
	err := s.do(ctx, func(ctx context.Context) error {
		obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
		if err != nil {
			return err
		}
		defer obj.Close()
		data, err = io.ReadAll(io.LimitReader(obj, maxBytes+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > maxBytes {
			return fmt.Errorf("the object is larger than %d bytes, more than a stored photo can be", maxBytes)
		}
		return nil
	})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, fmt.Errorf("%w: object %s/%s is missing: %v", ErrNotFound, s.cfg.Bucket, key, err)
		}
		return nil, s.describe("get", key, err)
	}
	return data, nil
}

// Delete removes the object; S3 answers success when it is already gone.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	err := s.do(ctx, func(ctx context.Context) error {
		return s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{})
	})
	if err != nil {
		return s.describe("delete", key, err)
	}
	return nil
}

// Check is run at start-up: the bucket must exist (it is never created here) and must refuse a request that
// carries no signature, both a listing and an object read; otherwise the photos would not be private.
func (s *S3Store) Check(ctx context.Context) error {
	var exists bool
	err := s.do(ctx, func(ctx context.Context) error {
		var err error
		exists, err = s.client.BucketExists(ctx, s.cfg.Bucket)
		return err
	})
	if err != nil {
		return s.describe("bucket check", "", err)
	}
	if !exists {
		return fmt.Errorf("user_photo: bucket %s does not exist at %s; create it (private, no anonymous access) before starting", s.cfg.Bucket, s.cfg.Endpoint)
	}
	scheme := "http"
	if s.cfg.Secure {
		scheme = "https"
	}
	base := scheme + "://" + s.cfg.Endpoint + "/" + url.PathEscape(s.cfg.Bucket)
	for _, probe := range []struct{ what, url string }{
		{"an unsigned listing of the bucket", base + "/?list-type=2&max-keys=1"},
		{"an unsigned read of an object", base + "/user-photo-anonymous-probe"},
	} {
		if err := s.refusesAnonymous(ctx, probe.what, probe.url); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3Store) refusesAnonymous(ctx context.Context, what, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("user_photo: building %s (%s): %w", what, u, err)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("user_photo: sending %s to %s failed: %w", what, u, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	return fmt.Errorf("user_photo: bucket %s at %s is not private: %s (%s) answered HTTP %d instead of 401 or 403; remove its anonymous access", s.cfg.Bucket, s.cfg.Endpoint, what, u, resp.StatusCode)
}
