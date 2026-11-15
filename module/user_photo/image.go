package user_photo

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// format is an accepted input format, told by the file's own bytes.
type format string

const (
	formatPNG  format = "png"
	formatJPEG format = "jpeg"
	formatWebP format = "webp"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// sniff names the format of data by its leading bytes; the request's Content-Type and file name are never read.
func sniff(data []byte) (format, bool) {
	switch {
	case bytes.HasPrefix(data, pngSignature):
		return formatPNG, true
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return formatJPEG, true
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return formatWebP, true
	}
	return "", false
}

// readLimited reads r up to max bytes and refuses one more, without reading the rest.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: the upload has no body", ErrDecode)
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("user_photo: reading the upload failed after %d bytes: %w", len(data), err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w (%d bytes at most)", ErrTooLarge, max)
	}
	return data, nil
}

// checkSize refuses dimensions over the pixel-bomb limits.
func (c Config) checkSize(what string, w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("%w: %s declares %dx%d pixels", ErrDecode, what, w, h)
	}
	if w > c.MaxSide || h > c.MaxSide {
		return fmt.Errorf("%w: %s is %dx%d, over %d pixels on a side", ErrTooManyPixels, what, w, h, c.MaxSide)
	}
	if int64(w)*int64(h) > c.MaxPixels {
		return fmt.Errorf("%w: %s is %dx%d, %d pixels, over %d", ErrTooManyPixels, what, w, h, int64(w)*int64(h), c.MaxPixels)
	}
	return nil
}

// webpFrames walks a WebP file's RIFF chunks and checks the size of every image it declares: the VP8X canvas and
// each VP8 or VP8L frame header. The decoder allocates by the frame header, which may differ from the canvas that
// DecodeConfig reports, so both are checked before decoding. An animation is refused.
func (c Config) webpFrames(data []byte) error {
	if len(data) < 12 {
		return fmt.Errorf("%w: WebP file shorter than its header", ErrDecode)
	}
	riffLen := int64(binary.LittleEndian.Uint32(data[4:8])) + 8
	end := int64(len(data))
	if riffLen < end {
		end = riffLen
	}
	frames := 0
	for off := int64(12); off+8 <= end; {
		id := string(data[off : off+4])
		n := int64(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if n > end-body {
			return fmt.Errorf("%w: WebP chunk %q at byte %d runs past the end of the file", ErrDecode, id, off)
		}
		chunk := data[body : body+n]
		switch id {
		case "ANIM", "ANMF":
			return fmt.Errorf("%w: animated WebP is not accepted", ErrDecode)
		case "VP8X":
			if len(chunk) < 10 {
				return fmt.Errorf("%w: WebP VP8X chunk of %d bytes", ErrDecode, len(chunk))
			}
			w := int(uint32(chunk[4])|uint32(chunk[5])<<8|uint32(chunk[6])<<16) + 1
			h := int(uint32(chunk[7])|uint32(chunk[8])<<8|uint32(chunk[9])<<16) + 1
			if err := c.checkSize("the WebP canvas", w, h); err != nil {
				return err
			}
		case "VP8 ":
			if len(chunk) < 10 || chunk[3] != 0x9d || chunk[4] != 0x01 || chunk[5] != 0x2a {
				return fmt.Errorf("%w: WebP VP8 frame header missing or damaged", ErrDecode)
			}
			w := int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
			h := int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			if err := c.checkSize("the WebP VP8 frame", w, h); err != nil {
				return err
			}
			frames++
		case "VP8L":
			if len(chunk) < 5 || chunk[0] != 0x2f {
				return fmt.Errorf("%w: WebP VP8L frame header missing or damaged", ErrDecode)
			}
			bits := binary.LittleEndian.Uint32(chunk[1:5])
			w := int(bits&0x3fff) + 1
			h := int((bits>>14)&0x3fff) + 1
			if err := c.checkSize("the WebP VP8L frame", w, h); err != nil {
				return err
			}
			frames++
		}
		off = body + n + n&1
	}
	if frames != 1 {
		return fmt.Errorf("%w: WebP file holds %d image frames, not 1", ErrDecode, frames)
	}
	return nil
}

// sosMarker starts a JPEG scan. Go's decoder reads it as these two adjacent bytes (fill bytes before it are 0xFF
// too), and inside entropy-coded data 0xFF is always followed by 0x00 or a restart marker, so counting the pair
// over the whole file counts every scan the decoder can see, and at worst a few more found inside APPn or COM data.
var sosMarker = []byte{0xFF, 0xDA}

// maxJPEGScans bounds the scans of a JPEG. Each progressive scan walks every block of its components, and a scan
// that only says "nothing here" costs a few bytes: a 2 MiB, 4000x4000 file holds 64 555 of them at about 6 ms each,
// six minutes of one core. 64 scans cost about 0.4 s. Encoders write about ten scans for a progressive JPEG and one
// to three for a baseline one; an embedded preview adds its own.
const maxJPEGScans = 64

// decodeConfig reads the declared dimensions with the decoder of the sniffed format only (no other registered
// decoder, such as GIF or BMP, is ever reached) and checks them.
func (c Config) decodeConfig(f format, data []byte) (image.Config, error) {
	var cfg image.Config
	var err error
	switch f {
	case formatPNG:
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	case formatJPEG:
		if n := bytes.Count(data, sosMarker); n > maxJPEGScans {
			return cfg, fmt.Errorf("%w: the JPEG holds %d scan markers, more than %d", ErrDecode, n, maxJPEGScans)
		}
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case formatWebP:
		if err = c.webpFrames(data); err != nil {
			return cfg, err
		}
		cfg, err = webp.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil {
		return cfg, fmt.Errorf("%w: reading the %s header: %v", ErrDecode, f, err)
	}
	return cfg, c.checkSize("the "+string(f)+" image", cfg.Width, cfg.Height)
}

func decode(f format, data []byte) (img image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("%w: the %s decoder stopped on the file: %v", ErrDecode, f, r)
		}
	}()
	switch f {
	case formatPNG:
		img, err = png.Decode(bytes.NewReader(data))
	case formatJPEG:
		img, err = jpeg.Decode(bytes.NewReader(data))
	case formatWebP:
		img, err = webp.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: decoding the %s image: %v", ErrDecode, f, err)
	}
	return img, nil
}

// reencode checks data and returns it as a Size x Size lossless WebP built from its pixels alone: a centred square
// crop, scaled with Catmull-Rom. Nothing of the file but its pixels reaches the result, so its metadata is gone.
func (m *Manager) reencode(ctx context.Context, data []byte) ([]byte, error) {
	f, ok := sniff(data)
	if !ok {
		return nil, ErrFormat
	}
	cfg, err := m.cfg.decodeConfig(f, data)
	if err != nil {
		return nil, err
	}
	select {
	case m.decodeSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("user_photo: waiting for a decode slot (%d in use): %w", cap(m.decodeSlots), ctx.Err())
	}
	dst, err := decodeAndScale(f, data, cfg)
	<-m.decodeSlots // the full-size image is garbage from here; only the 256x256 copy is kept
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := nativewebp.Encode(&out, dst, nil); err != nil {
		return nil, fmt.Errorf("user_photo: encoding the %dx%d WebP failed: %w", Size, Size, err)
	}
	return out.Bytes(), nil
}

// decodeAndScale decodes the image and returns its centred square scaled to Size x Size. It runs while the caller
// holds a decode slot, so the full-size image never outlives the slot.
func decodeAndScale(f format, data []byte, cfg image.Config) (*image.RGBA, error) {
	img, err := decode(f, data)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return nil, fmt.Errorf("%w: the %s header declares %dx%d but the image decoded to %dx%d", ErrDecode, f, cfg.Width, cfg.Height, b.Dx(), b.Dy())
	}
	side := min(b.Dx(), b.Dy())
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, image.Rect(x0, y0, x0+side, y0+side), draw.Src, nil)
	return dst, nil
}
