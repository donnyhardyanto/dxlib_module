// Package user_photo keeps one profile photo per user: checked, re-encoded and stored in a private bucket.
//
// An upload is read up to Config.MaxBytes (2 MiB by default) and accepted only when its bytes are PNG, JPEG or
// WebP, whatever the request claims. Its dimensions are read from the header and checked against a pixel limit
// before any pixel is decoded (a pixel bomb is refused without its memory being allocated). The image is then
// decoded, cropped to a centred square and scaled to 256x256, and encoded again as lossless WebP from the pixels
// alone, so no EXIF, GPS, ICC, XMP or comment survives. The result is stored under a new random object key in a
// private bucket (an S3 API such as SeaweedFS; S3Store), and the user's row in the database (Store) names it.
//
// Only the route reads it back: Handler serves /users/{uuid}/photo after the caller's session is checked and
// Rights agrees; a photo the caller may not see answers 404, as a missing one does. A change or removal updates
// the photo reference in every session of the user (session.Store.PropagateProfile) and is audited as
// auth_audit.PhotoChanged or PhotoRemoved. DeleteUser removes the row and the object with the user.
package user_photo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

// ContentType is the type of every stored photo.
const ContentType = "image/webp"

// Size is the width and height of every stored photo, in pixels.
const Size = 256

const (
	DefaultMaxBytes             = 2 << 20 // 2 MiB
	HighestMaxBytes             = 8 << 20
	DefaultMaxPixels            = 16 << 20 // 16 777 216 pixels, 4096x4096
	DefaultMaxSide              = 8192
	DefaultMaxConcurrentDecodes = 1
	DefaultPhotoPath            = "/users/{uuid}/photo"
	DefaultCacheMaxAge          = 24 * time.Hour
	DefaultOperationTimeout     = 10 * time.Second
)

// Config is set by the service.
type Config struct {
	// MaxBytes is the largest upload accepted, counted while reading. Default 2 MiB, at most 8 MiB.
	MaxBytes int64
	// MaxPixels is the pixel-bomb limit: an image whose header declares more pixels (width x height) is refused
	// before it is decoded. Default 16 777 216 (4096x4096).
	MaxPixels int64
	// MaxSide refuses an image wider or taller than this. Default 8192.
	MaxSide int
	// MaxConcurrentDecodes bounds the decodes running at once. Each holds its slot until the image is scaled, and
	// can need up to about MaxPixels x 20 bytes meanwhile (a progressive four-component JPEG keeps 16 bytes of
	// coefficients per pixel beside its 4-byte pixels; a 16-bit PNG needs 8): 320 MiB at the default MaxPixels.
	// An upload waits for a slot until its context ends. Default 1.
	MaxConcurrentDecodes int
	// KeyPrefix starts every object key, for example "user-photo/". Letters, digits and / _ . - only, no "..".
	KeyPrefix string
	// PhotoPath is where the service mounts Handler, with {uuid} for the user; the session's photo reference is
	// this path with ?v=<version>. Default "/users/{uuid}/photo".
	PhotoPath string
	// CacheMaxAge is the max-age of a served photo's Cache-Control: private (never a shared cache). The reference
	// carries the version, so a change is fetched at once. Default 24 hours.
	CacheMaxAge time.Duration
	// OperationTimeout bounds the steps that must finish after the caller's context has ended: deleting an object
	// after a failed database write, the audit record. Default 10 seconds.
	OperationTimeout time.Duration
}

var (
	keyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9/_.\-]{0,128}$`)
	objectKeyPattern = regexp.MustCompile(`^[A-Za-z0-9/_.\-]{1,256}$`)
)

func (c Config) withDefaults() (Config, error) {
	if c.MaxBytes == 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.MaxBytes < 1 || c.MaxBytes > HighestMaxBytes {
		return c, fmt.Errorf("user_photo: Config.MaxBytes %d must be between 1 and %d", c.MaxBytes, HighestMaxBytes)
	}
	if c.MaxPixels == 0 {
		c.MaxPixels = DefaultMaxPixels
	}
	if c.MaxSide == 0 {
		c.MaxSide = DefaultMaxSide
	}
	if c.MaxPixels < 1 || c.MaxSide < 1 {
		return c, fmt.Errorf("user_photo: Config.MaxPixels (%d) and Config.MaxSide (%d) must be positive", c.MaxPixels, c.MaxSide)
	}
	if c.MaxConcurrentDecodes == 0 {
		c.MaxConcurrentDecodes = DefaultMaxConcurrentDecodes
	}
	if c.MaxConcurrentDecodes < 1 {
		return c, fmt.Errorf("user_photo: Config.MaxConcurrentDecodes %d must be positive", c.MaxConcurrentDecodes)
	}
	if !keyPrefixPattern.MatchString(c.KeyPrefix) || strings.Contains(c.KeyPrefix, "..") || strings.HasPrefix(c.KeyPrefix, "/") {
		return c, fmt.Errorf("user_photo: Config.KeyPrefix %q must be up to 128 letters, digits or / _ . -, without \"..\" or a leading /", truncate(c.KeyPrefix, 140))
	}
	if c.PhotoPath == "" {
		c.PhotoPath = DefaultPhotoPath
	}
	if !strings.HasPrefix(c.PhotoPath, "/") || strings.Count(c.PhotoPath, "{uuid}") != 1 || strings.ContainsAny(c.PhotoPath, "?#") {
		return c, fmt.Errorf("user_photo: Config.PhotoPath %q must start with / and hold {uuid} once, without ? or #", truncate(c.PhotoPath, 140))
	}
	if c.CacheMaxAge == 0 {
		c.CacheMaxAge = DefaultCacheMaxAge
	}
	if c.CacheMaxAge < 0 {
		return c, fmt.Errorf("user_photo: Config.CacheMaxAge %s must not be negative", c.CacheMaxAge)
	}
	if c.OperationTimeout == 0 {
		c.OperationTimeout = DefaultOperationTimeout
	}
	return c, nil
}

// Ref returns the photo reference a session carries for a user's photo version: PhotoPath with the uuid and
// ?v=<version>. Empty when version is 0 (no photo).
func (c Config) Ref(userUUID string, version int64) string {
	if version <= 0 {
		return ""
	}
	p := c.PhotoPath
	if p == "" {
		p = DefaultPhotoPath
	}
	return strings.Replace(p, "{uuid}", userUUID, 1) + "?v=" + strconv.FormatInt(version, 10)
}

var (
	// ErrNotFound: no photo, or a photo the caller may not see or change, or not a user uuid. Answer it as 404,
	// never with more detail.
	ErrNotFound = errors.New("user_photo: not found")
	// ErrRejected is wrapped by every refusal of an uploaded image (too large, not PNG/JPEG/WebP, too many pixels,
	// undecodable). Its text is safe to show the user; answer 413 for ErrTooLarge and 422 for the rest.
	ErrRejected = errors.New("user_photo: image refused")
	// ErrTooLarge: the upload is over Config.MaxBytes. Wraps ErrRejected.
	ErrTooLarge = fmt.Errorf("%w: the upload is larger than the limit", ErrRejected)
	// ErrFormat: the bytes are not PNG, JPEG or WebP. Wraps ErrRejected.
	ErrFormat = fmt.Errorf("%w: the file is not a PNG, JPEG or WebP image", ErrRejected)
	// ErrTooManyPixels: the declared dimensions are over Config.MaxPixels or Config.MaxSide. Wraps ErrRejected.
	ErrTooManyPixels = fmt.Errorf("%w: the image has more pixels than allowed", ErrRejected)
	// ErrDecode: the image is damaged or uses a feature the decoders refuse (an animated WebP). Wraps ErrRejected.
	ErrDecode = fmt.Errorf("%w: the image could not be decoded", ErrRejected)
	// ErrNoCaller: Set or Remove was called without the caller's session.
	ErrNoCaller = errors.New("user_photo: no caller session")
	// ErrConflict: another change of the same user's first photo was stored at the same moment; try again.
	ErrConflict = errors.New("user_photo: changed concurrently")
)

// Rights decides who may see and change a user's photo. Both are asked before anything is read or written.
type Rights interface {
	MayView(ctx context.Context, caller *session.Session, userUUID string) (bool, error)
	MayChange(ctx context.Context, caller *session.Session, userUUID string) (bool, error)
}

// SelfOrPermission lets a user see and change their own photo, and a caller whose session holds ViewAny (or
// ChangeAny) see (or change) anyone's. An empty permission name grants nothing.
type SelfOrPermission struct {
	ViewAny   string
	ChangeAny string
}

// MayView reports whether caller may see the photo of userUUID.
func (r SelfOrPermission) MayView(_ context.Context, caller *session.Session, userUUID string) (bool, error) {
	return allowed(caller, userUUID, r.ViewAny), nil
}

// MayChange reports whether caller may change or remove the photo of userUUID.
func (r SelfOrPermission) MayChange(_ context.Context, caller *session.Session, userUUID string) (bool, error) {
	return allowed(caller, userUUID, r.ChangeAny), nil
}

func allowed(caller *session.Session, userUUID, permission string) bool {
	if caller == nil || caller.UserUUID == "" {
		return false
	}
	if strings.EqualFold(caller.UserUUID, userUUID) {
		return true
	}
	if permission == "" {
		return false
	}
	for _, p := range caller.Profile.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// Photo is a stored photo.
type Photo struct {
	UserUUID string
	// Version rises with every change of the user's photo; 0 means none.
	Version int64
	// Ref is the reference the sessions carry (Config.Ref).
	Ref string
	// Bytes is the size of the stored WebP.
	Bytes int
}

// Change is what Set or Remove did once the photo was stored or removed.
type Change struct {
	Photo Photo
	// Propagation counts the sessions that got the new reference.
	Propagation session.PropagationResult
	// SessionsErr is set when the user's sessions could not be given the new reference. The session store has then
	// failed closed (their cached profiles dropped, or the sessions ended), and the error says which; the photo
	// change itself is stored.
	SessionsErr error
	// OldObjectErr is set when the previous photo's object could not be deleted. The error names its key, as the
	// audit record does, so it can be removed by hand; it is no longer referenced or served.
	OldObjectErr error
}

// canonicalUUID parses a user uuid and returns its canonical lower-case form.
func canonicalUUID(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if len(t) != 36 {
		return "", false
	}
	u, err := uuid.Parse(t)
	if err != nil || u == uuid.Nil {
		return "", false
	}
	return u.String(), true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
