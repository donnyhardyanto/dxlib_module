package user_photo

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

// Handler serves GET and HEAD of the photo route. Mount it on a pattern with {uuid}, matching Config.PhotoPath:
//
//	mux.Handle("GET /users/{uuid}/photo", m.Handler(sessions, sessionConfig))
//
// In order: the session from the cookie (401 without one), Rights.MayView (404 when refused), then the row and
// the object (404 when there is none), so nothing about another user's photo is read or told before the right is
// checked. A served photo carries Content-Type image/webp, X-Content-Type-Options: nosniff, Cache-Control: private
// with Config.CacheMaxAge, Vary: Cookie, an ETag of its version (If-None-Match gives 304), and a
// Content-Security-Policy that lets nothing run if it is opened as a page.
func (m *Manager) Handler(sessions session.Store, cookies session.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Vary", "Cookie")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			plain(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		token, err := cookies.TokenFromRequest(r)
		if err != nil {
			plain(w, http.StatusUnauthorized, "not logged in")
			return
		}
		caller, err := sessions.Get(r.Context(), token)
		if err != nil {
			if errors.Is(err, session.ErrNotFound) || errors.Is(err, session.ErrInvalidToken) {
				plain(w, http.StatusUnauthorized, "not logged in")
				return
			}
			m.Log.Errorf(err, "user_photo: %s %s: reading the caller's session failed", r.Method, r.URL.Path)
			plain(w, http.StatusServiceUnavailable, "reading the session failed; try again")
			return
		}
		img, err := m.Get(r.Context(), caller, r.PathValue("uuid"))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				plain(w, http.StatusNotFound, "not found")
				return
			}
			m.Log.Errorf(err, "user_photo: %s %s for user %s failed", r.Method, r.URL.Path, caller.UserUUID)
			plain(w, http.StatusServiceUnavailable, "reading the photo failed; try again")
			return
		}
		etag := `"` + strconv.FormatInt(img.Version, 10) + `"`
		h.Set("ETag", etag)
		h.Set("Cache-Control", "private, max-age="+strconv.FormatInt(int64(m.cfg.CacheMaxAge.Seconds()), 10))
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Type", ContentType)
		h.Set("Content-Length", strconv.Itoa(len(img.Data)))
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Disposition", `inline; filename="photo.webp"`)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(img.Data)
		}
	})
}

// plain answers a short text that no cache keeps.
func plain(w http.ResponseWriter, status int, text string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text + "\n"))
}

func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == etag || t == "*" {
			return true
		}
	}
	return false
}
