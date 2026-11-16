package user_photo

// These tests run against a real S3 API, as tools/test-seaweedfs.sh starts one (SeaweedFS):
//
//	DXLIB_MODULE_TEST_S3=127.0.0.1:18333 DXLIB_MODULE_TEST_S3_BUCKET=... DXLIB_MODULE_TEST_S3_KEY=... go test ./module/user_photo
//
// The store signs with a photo identity allowed only Read, Write and List on the test bucket, handed over by the
// credential provider; an admin identity creates the buckets. Without the variables they are skipped.

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/donnyhardyanto/dxlib_module/v2/module/credentials"
)

type s3Env struct {
	addr, bucket string
	admin        *minio.Client
	provider     *credentials.Fixed
}

func newS3Env(t *testing.T) *s3Env {
	t.Helper()
	addr := os.Getenv("DXLIB_MODULE_TEST_S3")
	if addr == "" {
		t.Skip("DXLIB_MODULE_TEST_S3 is not set; run tools/test-seaweedfs.sh")
	}
	e := &s3Env{addr: addr, bucket: os.Getenv("DXLIB_MODULE_TEST_S3_BUCKET"), provider: credentials.NewFixed(nil)}
	e.provider.Set("s3/photos", credentials.Credential{Username: os.Getenv("DXLIB_MODULE_TEST_S3_KEY"), Password: os.Getenv("DXLIB_MODULE_TEST_S3_SECRET"), Version: "1"})
	e.provider.Set("s3/other", credentials.Credential{Username: os.Getenv("DXLIB_MODULE_TEST_S3_OTHER_KEY"), Password: os.Getenv("DXLIB_MODULE_TEST_S3_OTHER_SECRET"), Version: "1"})
	e.provider.Set("s3/admin", credentials.Credential{Username: os.Getenv("DXLIB_MODULE_TEST_S3_ADMIN_KEY"), Password: os.Getenv("DXLIB_MODULE_TEST_S3_ADMIN_SECRET"), Version: "1"})
	admin, err := minio.New(addr, &minio.Options{Creds: miniocreds.NewStaticV4(os.Getenv("DXLIB_MODULE_TEST_S3_ADMIN_KEY"), os.Getenv("DXLIB_MODULE_TEST_S3_ADMIN_SECRET"), ""),
		BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatal(err)
	}
	e.admin = admin
	e.ensureBucket(t, e.bucket)
	return e
}

// ensureBucket creates a bucket as the admin and writes a first object, retrying while SeaweedFS registers its
// volume server.
func (e *s3Env) ensureBucket(t *testing.T, bucket string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := e.admin.MakeBucket(bg, bucket, minio.MakeBucketOptions{})
		if err != nil {
			if code := minio.ToErrorResponse(err).Code; code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
				err = nil
			}
		}
		if err == nil {
			_, err = e.admin.PutObject(bg, bucket, "warm-up", strings.NewReader("x"), 1, minio.PutObjectOptions{})
		}
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("creating bucket %s at %s as the admin: %v", bucket, e.addr, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (e *s3Env) store(t *testing.T, bucket, credName string, provider credentials.Provider) *S3Store {
	t.Helper()
	if provider == nil {
		provider = e.provider
	}
	s, err := NewS3Store(S3Config{Endpoint: e.addr, Bucket: bucket, CredentialName: credName, Timeout: 10 * time.Second}, provider)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *s3Env) adminHas(t *testing.T, key string) bool {
	t.Helper()
	_, err := e.admin.StatObject(bg, e.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false
	}
	t.Fatalf("admin stat of %s: %v", key, err)
	return false
}

func (e *s3Env) unsignedGet(t *testing.T, bucket, key string) int {
	t.Helper()
	resp, err := http.Get("http://" + e.addr + "/" + bucket + "/" + key)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestS3CheckAcceptsOnlyAnExistingPrivateBucket(t *testing.T) {
	e := newS3Env(t)
	if err := e.store(t, e.bucket, "s3/photos", nil).Check(bg); err != nil {
		t.Fatalf("private bucket: %v", err)
	}
	// A missing bucket is refused and not created, even by an identity allowed to create buckets.
	missing := "user-photos-missing-" + strings.ToLower(newUser()[:8])
	err := e.store(t, missing, "s3/admin", nil).Check(bg)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing bucket: %v", err)
	}
	if ok, _ := e.admin.BucketExists(bg, missing); ok {
		t.Fatal("Check created the bucket")
	}
	// A bucket anyone may read is refused.
	e.ensureBucket(t, "public-test")
	if code := e.unsignedGet(t, "public-test", "warm-up"); code != 200 {
		t.Fatalf("the public test bucket answers an unsigned read with %d; the test setup is wrong", code)
	}
	err = e.store(t, "public-test", "s3/admin", nil).Check(bg)
	if err == nil || !strings.Contains(err.Error(), "not private") || !strings.Contains(err.Error(), "HTTP 200") {
		t.Fatalf("public bucket: %v", err)
	}
}

func TestS3PutGetDeleteAndAnUnsignedReadIsRefused(t *testing.T) {
	e := newS3Env(t)
	s := e.store(t, e.bucket, "s3/photos", nil)
	key, _ := newObjectKey("user-photo/")
	data := bytes.Repeat([]byte("webp"), 1000)
	if err := s.Put(bg, key, data, ContentType); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(bg, key, HighestMaxBytes)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("get: %d bytes, %v", len(got), err)
	}
	if code := e.unsignedGet(t, e.bucket, key); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Fatalf("an unsigned GET of a stored photo answered %d", code)
	}
	if _, err := s.Get(bg, key, 100); err == nil || !strings.Contains(err.Error(), "larger than 100") {
		t.Fatalf("over-long object: %v", err)
	}
	if err := s.Delete(bg, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(bg, key, HighestMaxBytes); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := s.Delete(bg, key); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	// An identity limited to another bucket is refused, with the server's answer in the error.
	other := e.store(t, e.bucket, "s3/other", nil)
	if err := other.Put(bg, key, data, ContentType); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("identity of another bucket: %v", err)
	}
}

// rotating hands out a revoked credential first, then the current one, as a secret store does once the key it
// had cached is rotated.
type rotating struct {
	inner   credentials.Provider
	calls   atomic.Int32
	revoked credentials.Credential
}

func (r *rotating) Credential(ctx context.Context, name string) (credentials.Credential, error) {
	if r.calls.Add(1) == 1 {
		return r.revoked, nil
	}
	return r.inner.Credential(ctx, name)
}

func TestS3ARotatedCredentialIsAskedForAgain(t *testing.T) {
	e := newS3Env(t)
	cur, _ := e.provider.Credential(bg, "s3/photos")
	p := &rotating{inner: e.provider, revoked: credentials.Credential{Username: cur.Username, Password: "revoked-" + cur.Password, Version: "0"}}
	s := e.store(t, e.bucket, "s3/photos", p)
	key, _ := newObjectKey("user-photo/")
	if err := s.Put(bg, key, []byte("x"), ContentType); err != nil {
		t.Fatalf("put with a revoked first credential: %v", err)
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("provider asked %d times, want 2 (once refused, once more)", n)
	}
	if _, err := s.Get(bg, key, 10); err != nil {
		t.Fatal(err)
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("provider asked %d times after a working request, want still 2", n)
	}
	_ = s.Delete(bg, key)
}

func TestS3PhotoLifecycleThroughTheManager(t *testing.T) {
	e := newS3Env(t)
	r := newRig(t, Config{}, nil)
	objects := e.store(t, e.bucket, "s3/photos", nil)
	r.m.objects = objects
	alice := newUser()
	token, caller := r.sessions.login(t, alice)

	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(jpegWithExif(t, 800, 600, "GPS-SECRET")))
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := r.store.Get(bg, alice)
	first := rec.ObjectKey
	stored, err := objects.Get(bg, first, HighestMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, stored)
	if bytes.Contains(stored, []byte("GPS-SECRET")) {
		t.Fatal("metadata reached the bucket")
	}
	if code := e.unsignedGet(t, e.bucket, first); code != http.StatusForbidden {
		t.Fatalf("unsigned GET of the stored photo: %d", code)
	}
	w := r.serve(t, http.MethodGet, ch.Photo.Ref, token, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), stored) || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("served %d, %d bytes", w.Code, w.Body.Len())
	}
	bobToken, _ := r.sessions.login(t, newUser())
	if w := r.serve(t, http.MethodGet, ch.Photo.Ref, bobToken, nil); w.Code != 404 {
		t.Fatalf("another user: %d", w.Code)
	}

	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 300, 300, color.NRGBA{1, 2, 3, 255}))); err != nil {
		t.Fatal(err)
	}
	if e.adminHas(t, first) {
		t.Fatal("the replaced object is still in the bucket")
	}
	rec, _ = r.store.Get(bg, alice)
	if err := r.m.DeleteUser(bg, alice, ""); err != nil {
		t.Fatal(err)
	}
	if e.adminHas(t, rec.ObjectKey) {
		t.Fatal("the photo outlived its user")
	}
}
