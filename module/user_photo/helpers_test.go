package user_photo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

var bg = context.Background()

// fakeSessions is a session.Store holding sessions by token, recording what the Manager asks of it.
type fakeSessions struct {
	mu           sync.Mutex
	byToken      map[string]*session.Session
	propagated   []session.Profile
	propagateErr error
	invalidated  []string
	invalidErr   error
	revoked      []string
	getErr       error
}

func newFakeSessions() *fakeSessions { return &fakeSessions{byToken: map[string]*session.Session{}} }

// login adds a session for the user and returns its token.
func (f *fakeSessions) login(t *testing.T, userUUID string, permissions ...string) (string, *session.Session) {
	t.Helper()
	token, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := session.SessionID(token)
	s := &session.Session{ID: id, UserUUID: userUUID, Profile: session.Profile{Permissions: permissions}}
	f.mu.Lock()
	f.byToken[token] = s
	f.mu.Unlock()
	return token, s
}

func (f *fakeSessions) Create(context.Context, session.LoginInfo) (string, *session.Session, error) {
	return "", nil, errors.New("not used")
}
func (f *fakeSessions) Get(_ context.Context, token string) (*session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	if _, err := session.SessionID(token); err != nil {
		return nil, err
	}
	s, ok := f.byToken[token]
	if !ok {
		return nil, session.ErrNotFound
	}
	return s, nil
}
func (f *fakeSessions) Rotate(context.Context, string, *session.Profile) (string, *session.Session, error) {
	return "", nil, errors.New("not used")
}
func (f *fakeSessions) Logout(context.Context, string) error                    { return errors.New("not used") }
func (f *fakeSessions) List(context.Context, string) ([]session.Session, error) { return nil, nil }
func (f *fakeSessions) RevokeSession(context.Context, string, string) error {
	return errors.New("not used")
}
func (f *fakeSessions) RevokeAll(_ context.Context, userUUID, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, userUUID)
	return 2, nil
}
func (f *fakeSessions) PropagateProfile(_ context.Context, _ string, p session.Profile) (session.PropagationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.propagateErr != nil {
		return session.PropagationResult{}, f.propagateErr
	}
	f.propagated = append(f.propagated, p)
	return session.PropagationResult{Updated: 2}, nil
}
func (f *fakeSessions) InvalidateProfiles(_ context.Context, userUUID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated = append(f.invalidated, userUUID)
	return f.invalidErr
}

var _ session.Store = (*fakeSessions)(nil)

// rig is a Manager on memory stores and fake sessions.
type rig struct {
	m        *Manager
	store    *MemoryStore
	objects  *MemoryObjectStore
	sessions *fakeSessions
	audit    *auth_audit.Memory
	loadErr  error
	cfg      Config
}

func newRig(t *testing.T, cfg Config, rights Rights) *rig {
	t.Helper()
	r := &rig{store: NewMemoryStore(), objects: NewMemoryObjectStore(), sessions: newFakeSessions(), audit: &auth_audit.Memory{}}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "user-photo/"
	}
	inner := session.ProfileLoaderFunc(func(_ context.Context, u string) (session.Profile, error) {
		if r.loadErr != nil {
			return session.Profile{}, r.loadErr
		}
		return session.Profile{Username: "u-" + u[:8], DisplayName: "User", Status: "active", Version: 7}, nil
	})
	profiles, err := NewProfiles(inner, r.store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(cfg, Dependencies{Store: r.store, Objects: r.objects, Sessions: r.sessions, Profiles: profiles, Rights: rights, Audit: r.audit})
	if err != nil {
		t.Fatal(err)
	}
	r.m, r.cfg = m, m.Config()
	return r
}

func newUser() string { return uuid.NewString() }

// solidPNG returns a w x h PNG filled with c.
func solidPNG(t testing.TB, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngChunk returns one PNG chunk with its CRC.
func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

// withChunkBeforeIEND inserts a chunk before a PNG's IEND (the last 12 bytes).
func withChunkBeforeIEND(p []byte, chunk []byte) []byte {
	out := append([]byte(nil), p[:len(p)-12]...)
	out = append(out, chunk...)
	return append(out, p[len(p)-12:]...)
}

// pngHeaderOnly returns a PNG whose IHDR declares w x h, with no pixel data: a pixel bomb's header.
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	out := append([]byte(nil), pngSignature...)
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", []byte{0x78, 0x9c, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01})...)
	return append(out, pngChunk("IEND", nil)...)
}

// jpegWithExif returns a JPEG carrying an APP1 Exif segment that holds marker.
func jpegWithExif(t testing.TB, w, h int, marker string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	j := b.Bytes()
	payload := append([]byte("Exif\x00\x00"), []byte(marker)...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	out := append([]byte{0xFF, 0xD8}, seg...)
	return append(out, j[2:]...)
}

// jpegHeaderOnly returns a JPEG whose SOF0 declares w x h, then an SOS with no scan data.
func jpegHeaderOnly(w, h uint16) []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x0B, 0x08, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 0x01, 0x01, 0x11, 0x00,
		0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, 0xFF, 0xD9}
}

// riff wraps WebP chunks in a RIFF container.
func riff(chunks ...[]byte) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		body.Write(c)
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func webpChunk(id string, data []byte) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	if len(data)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// vp8lHeader returns a VP8L chunk body declaring w x h (and no real image data).
func vp8lHeader(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	b := []byte{0x2f, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:5], bits)
	return b
}

// vp8Header returns a VP8 (lossy) chunk body declaring w x h (and no real image data).
func vp8Header(w, h int) []byte {
	b := []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(b[6:8], uint16(w))
	binary.LittleEndian.PutUint16(b[8:10], uint16(h))
	return b
}

func vp8xHeader(w, h int) []byte {
	b := make([]byte, 10)
	w--
	h--
	b[4], b[5], b[6] = byte(w), byte(w>>8), byte(w>>16)
	b[7], b[8], b[9] = byte(h), byte(h>>8), byte(h>>16)
	return b
}

func decodePNG(t testing.TB, p []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
	return n
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var timeFar = time.Now().Add(time.Hour)

func mustToken(t *testing.T) string {
	t.Helper()
	tok, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
