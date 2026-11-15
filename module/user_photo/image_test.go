package user_photo

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"
)

func mustManager(t testing.TB, cfg Config) *Manager {
	t.Helper()
	c, err := cfg.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{cfg: c, decodeSlots: make(chan struct{}, c.MaxConcurrentDecodes)}
}

// checkOutput decodes a stored photo and checks it is a bare 256x256 lossless WebP: RIFF, WEBP, one VP8L chunk.
func checkOutput(t *testing.T, out []byte) {
	t.Helper()
	img, err := webp.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not a WebP: %v", err)
	}
	if b := img.Bounds(); b.Dx() != Size || b.Dy() != Size {
		t.Fatalf("output is %dx%d, want %dx%d", b.Dx(), b.Dy(), Size, Size)
	}
	if string(out[12:16]) != "VP8L" {
		t.Fatalf("output's first chunk is %q, want VP8L", out[12:16])
	}
	n := int(out[16]) | int(out[17])<<8 | int(out[18])<<16 | int(out[19])<<24
	if 20+n+n%2 != len(out) {
		t.Fatalf("output holds more than its one VP8L chunk: chunk %d bytes, file %d bytes", n, len(out))
	}
}

func TestPNGAndJPEGAndWebPAreReencodedTo256Square(t *testing.T) {
	m := mustManager(t, Config{})
	var lossless bytes.Buffer
	if err := nativewebp.Encode(&lossless, decodePNG(t, solidPNG(t, 300, 200, color.NRGBA{0, 200, 0, 255})), nil); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{
		"png":         solidPNG(t, 640, 480, color.NRGBA{200, 10, 10, 255}),
		"tiny png":    solidPNG(t, 1, 1, color.NRGBA{0, 0, 255, 255}),
		"jpeg":        jpegWithExif(t, 400, 600, "nothing"),
		"webp (VP8L)": lossless.Bytes(),
	} {
		out, err := m.reencode(bg, in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkOutput(t, out)
	}
}

func TestMetadataDoesNotSurvive(t *testing.T) {
	m := mustManager(t, Config{})
	const marker = "GPS-48.8584N-2.2945E-SECRET-CAMERA-SERIAL"
	inputs := map[string][]byte{
		"jpeg exif": jpegWithExif(t, 320, 240, marker),
		"png text":  withChunkBeforeIEND(solidPNG(t, 64, 64, color.NRGBA{1, 2, 3, 255}), pngChunk("tEXt", []byte("Comment\x00"+marker))),
		"png exif":  withChunkBeforeIEND(solidPNG(t, 64, 64, color.NRGBA{1, 2, 3, 255}), pngChunk("eXIf", []byte(marker))),
	}
	for name, in := range inputs {
		if !bytes.Contains(in, []byte(marker)) {
			t.Fatalf("%s: test input lacks its marker", name)
		}
		out, err := m.reencode(bg, in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.Contains(out, []byte(marker)) || bytes.Contains(out, []byte("Exif")) {
			t.Fatalf("%s: the stored photo still holds the input's metadata", name)
		}
		checkOutput(t, out)
	}
}

func TestCentreSquareIsKeptAndAlphaSurvives(t *testing.T) {
	m := mustManager(t, Config{})
	// 600x200: red | green | blue thirds. The centred 200x200 square is all green.
	img := decodePNG(t, solidPNG(t, 600, 200, color.NRGBA{255, 0, 0, 255}))
	for y := 0; y < 200; y++ {
		for x := 200; x < 400; x++ {
			img.Set(x, y, color.NRGBA{0, 255, 0, 255})
		}
		for x := 400; x < 600; x++ {
			img.Set(x, y, color.NRGBA{0, 0, 255, 255})
		}
	}
	out, err := m.reencode(bg, encodePNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := webp.Decode(bytes.NewReader(out))
	for _, x := range []int{2, 128, 253} {
		r, g, b, _ := got.At(x, 128).RGBA()
		if r>>8 > 10 || g>>8 < 245 || b>>8 > 10 {
			t.Fatalf("pixel (%d,128) is %d,%d,%d; want green (the centre square only)", x, r>>8, g>>8, b>>8)
		}
	}
	out, err = m.reencode(bg, solidPNG(t, 50, 50, color.NRGBA{0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ = webp.Decode(bytes.NewReader(out))
	if _, _, _, a := got.At(100, 100).RGBA(); a != 0 {
		t.Fatalf("a transparent image came out with alpha %d", a>>8)
	}
}

func TestOnlyPNGJPEGAndWebPBytesAreAccepted(t *testing.T) {
	m := mustManager(t, Config{})
	for name, in := range map[string][]byte{
		"gif":            []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\xff\xff\xff\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"),
		"bmp":            append([]byte("BM"), make([]byte, 60)...),
		"svg":            []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><rect width="10" height="10"/></svg>`),
		"html":           []byte("<!doctype html><script>alert(1)</script>"),
		"empty":          nil,
		"png name, no":   []byte("not really a png.png"),
		"riff, not webp": append([]byte("RIFF\x10\x00\x00\x00WAVE"), make([]byte, 8)...),
	} {
		if _, err := m.reencode(bg, in); !errors.Is(err, ErrFormat) || !errors.Is(err, ErrRejected) {
			t.Fatalf("%s: got %v, want ErrFormat", name, err)
		}
	}
}

func TestPixelBombsAreRefusedBeforeDecoding(t *testing.T) {
	m := mustManager(t, Config{})
	bombs := map[string][]byte{
		"png 50000x50000":              pngHeaderOnly(50000, 50000),
		"png 9000x10 (side)":           pngHeaderOnly(9000, 10),
		"jpeg 65000x65000":             jpegHeaderOnly(65000, 65000),
		"webp VP8L 16384x16384":        riff(webpChunk("VP8L", vp8lHeader(16384, 16384))),
		"webp VP8 16383x16383":         riff(webpChunk("VP8 ", vp8Header(16383, 16383))),
		"webp small canvas, big frame": riff(webpChunk("VP8X", vp8xHeader(16, 16)), webpChunk("VP8L", vp8lHeader(8000, 8000))),
		"webp big canvas":              riff(webpChunk("VP8X", vp8xHeader(100000, 100000)), webpChunk("VP8L", vp8lHeader(16, 16))),
	}
	for name, in := range bombs {
		start := time.Now()
		_, err := m.reencode(bg, in)
		if !errors.Is(err, ErrTooManyPixels) {
			t.Fatalf("%s: got %v, want ErrTooManyPixels", name, err)
		}
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("%s: refusal took %s; it must come from the header, before decoding", name, d)
		}
	}
	// The limit is the configured one.
	small := mustManager(t, Config{MaxPixels: 100 * 100})
	if _, err := small.reencode(bg, solidPNG(t, 101, 100, color.White)); !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("101x100 with MaxPixels 10000: got %v", err)
	}
	if _, err := small.reencode(bg, solidPNG(t, 100, 100, color.White)); err != nil {
		t.Fatalf("100x100 with MaxPixels 10000: %v", err)
	}
}

func TestAnimatedAndDamagedFilesAreRefused(t *testing.T) {
	m := mustManager(t, Config{})
	good := solidPNG(t, 40, 40, color.White)
	for name, in := range map[string][]byte{
		"animated webp":       riff(webpChunk("VP8X", vp8xHeader(16, 16)), webpChunk("ANIM", make([]byte, 6)), webpChunk("ANMF", make([]byte, 16))),
		"two webp frames":     riff(webpChunk("VP8L", vp8lHeader(16, 16)), webpChunk("VP8L", vp8lHeader(16, 16))),
		"webp no frame":       riff(webpChunk("VP8X", vp8xHeader(16, 16))),
		"webp chunk past end": append([]byte("RIFF\xff\x00\x00\x00WEBPVP8L\xff\xff\x00\x00"), 0x2f),
		"truncated png":       good[:len(good)/2],
		"png header only":     pngHeaderOnly(64, 64),
		"jpeg header only":    jpegHeaderOnly(64, 64),
	} {
		if _, err := m.reencode(bg, in); !errors.Is(err, ErrDecode) {
			t.Fatalf("%s: got %v, want ErrDecode", name, err)
		}
	}
}

func TestReadLimitedTakesExactlyMaxBytes(t *testing.T) {
	const max = DefaultMaxBytes
	if b, err := readLimited(bytes.NewReader(make([]byte, max)), max); err != nil || len(b) != max {
		t.Fatalf("exactly %d bytes: %d bytes, %v", max, len(b), err)
	}
	if _, err := readLimited(bytes.NewReader(make([]byte, max+1)), max); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("%d bytes: got %v, want ErrTooLarge", max+1, err)
	}
	if !strings.Contains(ErrTooLarge.Error(), "larger") {
		t.Fatal("ErrTooLarge text")
	}
}

func TestDecodeWaitsForASlotUntilItsContextEnds(t *testing.T) {
	m := mustManager(t, Config{MaxConcurrentDecodes: 1})
	m.decodeSlots <- struct{}{} // the only slot is taken
	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	_, err := m.reencode(ctx, solidPNG(t, 10, 10, color.White))
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "decode slot") {
		t.Fatalf("got %v, want a decode-slot wait that ended with the context", err)
	}
	<-m.decodeSlots
	if _, err := m.reencode(bg, solidPNG(t, 10, 10, color.White)); err != nil {
		t.Fatalf("with the slot free: %v", err)
	}
}

func TestConfigRefusesBadValues(t *testing.T) {
	for name, c := range map[string]Config{
		"max bytes over 8 MiB": {MaxBytes: HighestMaxBytes + 1},
		"prefix with ..":       {KeyPrefix: "a/../b/"},
		"prefix leading /":     {KeyPrefix: "/abs/"},
		"prefix odd chars":     {KeyPrefix: "a b"},
		"path without uuid":    {PhotoPath: "/users/photo"},
		"path with query":      {PhotoPath: "/users/{uuid}/photo?x=1"},
		"negative max age":     {CacheMaxAge: -time.Second},
	} {
		if _, err := c.withDefaults(); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	c, err := Config{}.withDefaults()
	if err != nil || c.MaxBytes != 2<<20 || c.PhotoPath != DefaultPhotoPath {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if got := c.Ref("0b8f4a1e-6a3e-4f6e-9b9c-0c4a4f1e2d3c", 3); got != "/users/0b8f4a1e-6a3e-4f6e-9b9c-0c4a4f1e2d3c/photo?v=3" {
		t.Fatalf("Ref: %s", got)
	}
	if c.Ref("x", 0) != "" {
		t.Fatal("Ref of version 0 must be empty")
	}
}

// FuzzReencode feeds arbitrary bytes, with each accepted format's leading bytes as seeds: nothing may panic, every
// refusal must wrap ErrRejected, and anything accepted must come out as a bare 256x256 WebP.
func FuzzReencode(f *testing.F) {
	f.Add(solidPNG(f, 16, 16, color.White))
	f.Add(jpegWithExif(f, 16, 16, "x"))
	f.Add(riff(webpChunk("VP8L", vp8lHeader(16, 16))))
	f.Add(riff(webpChunk("VP8 ", vp8Header(16, 16))))
	f.Add(riff(webpChunk("VP8X", vp8xHeader(16, 16)), webpChunk("VP8L", vp8lHeader(16, 16))))
	f.Add(pngHeaderOnly(30, 30))
	f.Add(jpegHeaderOnly(30, 30))
	m := mustManager(f, Config{MaxPixels: 512 * 512, MaxSide: 1024})
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := m.reencode(bg, data)
		if err != nil {
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("refusal not wrapping ErrRejected: %v", err)
			}
			return
		}
		checkOutput(t, out)
	})
}

// manyScans returns a progressive JPEG header for w x h followed by n near-empty scans: each scan costs the decoder
// a pass over every block while costing the file a dozen bytes.
func manyScans(w, h uint16, n int) []byte {
	out := []byte{0xFF, 0xD8, 0xFF, 0xC2, 0x00, 0x0B, 0x08, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 0x01, 0x01, 0x11, 0x00}
	for i := 0; i < n; i++ {
		out = append(out, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x01, 0x3F, 0x00, 0x00)
	}
	return append(out, 0xFF, 0xD9)
}

func TestAJPEGWithTooManyScansIsRefusedBeforeDecoding(t *testing.T) {
	m := mustManager(t, Config{})
	start := time.Now()
	_, err := m.reencode(bg, manyScans(4000, 4000, 150000))
	if !errors.Is(err, ErrDecode) || !strings.Contains(err.Error(), "150000 scan markers") {
		t.Fatalf("got %v, want ErrDecode naming the scan count", err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("refusal took %s; it must come before decoding", d)
	}
	// Fill bytes before a marker do not hide it.
	filled := bytes.ReplaceAll(manyScans(64, 64, maxJPEGScans+1), []byte{0xFF, 0xDA}, []byte{0xFF, 0xFF, 0xFF, 0xDA})
	if _, err := m.reencode(bg, filled); !errors.Is(err, ErrDecode) || !strings.Contains(err.Error(), "scan markers") {
		t.Fatalf("scans behind fill bytes: %v", err)
	}
	// Up to the limit, the file reaches the decoder (which refuses these empty scans for its own reason).
	_, err = m.reencode(bg, manyScans(64, 64, maxJPEGScans))
	if err == nil || strings.Contains(err.Error(), "scan markers") {
		t.Fatalf("%d scans: %v", maxJPEGScans, err)
	}
}
