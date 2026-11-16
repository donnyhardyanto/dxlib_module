package user_photo

import (
	"bytes"
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

// serve runs one request through a mux holding the handler, as a service mounts it.
func (r *rig) serve(t *testing.T, method, path, token string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	cookies := session.Config{}
	mux.Handle("/users/{uuid}/photo", r.m.Handler(r.sessions, cookies))
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.AddCookie(cookies.Cookie(token, timeFar))
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestHandlerServesOwnPhotoWithPrivateNosniffHeaders(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	token, caller := r.sessions.login(t, alice)
	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 30, 30, color.White)))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := r.objects.Get(bg, r.objects.Keys()[0], HighestMaxBytes)
	w := r.serve(t, http.MethodGet, ch.Photo.Ref, token, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), stored) {
		t.Fatalf("status %d, %d bytes", w.Code, w.Body.Len())
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/webp",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, max-age=86400",
		"Vary":                    "Cookie",
		"ETag":                    `"1"`,
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := w.Header().Get(k); got != want {
			t.Fatalf("%s: %q, want %q", k, got, want)
		}
	}
	// HEAD: the headers, no body.
	if w := r.serve(t, http.MethodHead, ch.Photo.Ref, token, nil); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "0" {
		t.Fatalf("HEAD: %d, %d bytes, length %s", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
	// A matching If-None-Match: 304, still private and nosniff.
	w = r.serve(t, http.MethodGet, ch.Photo.Ref, token, map[string]string{"If-None-Match": `W/"1"`})
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("304: %d %v", w.Code, w.Header())
	}
	if w := r.serve(t, http.MethodGet, ch.Photo.Ref, token, map[string]string{"If-None-Match": `"2"`}); w.Code != 200 {
		t.Fatalf("stale If-None-Match: %d", w.Code)
	}
}

func TestAnotherUsersUUIDIs404BeforeAnythingIsRead(t *testing.T) {
	r := newRig(t, Config{}, SelfOrPermission{ViewAny: "user.photo.view_any"})
	alice, bob := newUser(), newUser()
	aliceToken, aliceSession := r.sessions.login(t, alice)
	if _, err := r.m.Set(bg, aliceSession, alice, bytes.NewReader(solidPNG(t, 30, 30, color.White))); err != nil {
		t.Fatal(err)
	}
	bobToken, _ := r.sessions.login(t, bob)
	storeCalls, objectCalls := r.store.Calls, r.objects.Calls
	w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", bobToken, nil)
	if w.Code != 404 {
		t.Fatalf("bob asking for alice's photo: %d", w.Code)
	}
	if r.store.Calls != storeCalls || r.objects.Calls != objectCalls {
		t.Fatalf("the stores were read before the right was refused: store %d->%d, objects %d->%d", storeCalls, r.store.Calls, objectCalls, r.objects.Calls)
	}
	notFound := w.Body.String()
	// Every not-found answer is the same: a user without a photo, a malformed or injected uuid, a removed photo.
	for name, path := range map[string]string{
		"own, no photo": "/users/" + bob + "/photo",
		"not a uuid":    "/users/not-a-uuid/photo",
		"injection":     "/users/1%27%20OR%20%271%27=%271/photo",
		"nil uuid":      "/users/00000000-0000-0000-0000-000000000000/photo",
	} {
		w := r.serve(t, http.MethodGet, path, bobToken, nil)
		if w.Code != 404 || w.Body.String() != notFound || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s: %d %q", name, w.Code, w.Body.String())
		}
	}
	if _, err := r.m.Remove(bg, aliceSession, alice); err != nil {
		t.Fatal(err)
	}
	if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", aliceToken, nil); w.Code != 404 || w.Body.String() != notFound {
		t.Fatalf("removed photo: %d", w.Code)
	}
	// A viewer holding the permission sees anyone's photo.
	_, _ = r.m.Set(bg, aliceSession, alice, bytes.NewReader(solidPNG(t, 30, 30, color.White)))
	adminToken, _ := r.sessions.login(t, newUser(), "user.photo.view_any")
	if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", adminToken, nil); w.Code != 200 {
		t.Fatalf("viewer with the permission: %d", w.Code)
	}
}

func TestHandlerWithoutASessionIs401(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	for name, token := range map[string]string{"no cookie": "", "unknown token": mustToken(t), "malformed": "abc"} {
		if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", token, nil); w.Code != 401 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	if r.store.Calls != 0 || r.objects.Calls != 0 {
		t.Fatal("stores read without a session")
	}
	token, _ := r.sessions.login(t, alice)
	if w := r.serve(t, http.MethodPost, "/users/"+alice+"/photo", token, nil); w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: %d", w.Code)
	}
	r.sessions.getErr = errors.New("valkey: i/o timeout")
	if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", token, nil); w.Code != 503 {
		t.Fatalf("session store down: %d", w.Code)
	}
}

func TestHandlerAnswers503WhenTheBucketFails(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	token, caller := r.sessions.login(t, alice)
	_, _ = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 30, 30, color.White)))
	r.objects.FailGet = errors.New("HTTP 500 InternalError")
	if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", token, nil); w.Code != 503 {
		t.Fatalf("bucket failing: %d", w.Code)
	}
	r.objects.FailGet = nil
	_ = r.objects.Delete(bg, r.objects.Keys()[0])
	if w := r.serve(t, http.MethodGet, "/users/"+alice+"/photo", token, nil); w.Code != 404 {
		t.Fatalf("object missing behind its row: %d", w.Code)
	}
}
