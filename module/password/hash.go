package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The Argon2id parameters of every new hash: RFC 9106 section 4, the second recommended option (64 MiB of memory,
// 3 passes, 4 lanes), with a 128-bit salt and a 256-bit tag.
const (
	ArgonMemoryKiB = 64 * 1024
	ArgonPasses    = 3
	ArgonLanes     = 4
	ArgonSaltBytes = 16
	ArgonTagBytes  = 32
)

// Limits on the parameters read back from a stored hash, so a damaged or forged row cannot make a login take
// gigabytes or minutes.
const (
	maxStoredMemoryKiB = 256 * 1024
	maxStoredPasses    = 10
	maxStoredLanes     = 16
)

// argonParams are the parameters of one hash.
type argonParams struct {
	memory  uint32
	passes  uint32
	lanes   uint8
	salt    []byte
	tag     []byte
	version int
}

func (p argonParams) current() bool {
	return p.version == argon2.Version && p.memory == ArgonMemoryKiB && p.passes == ArgonPasses && p.lanes == ArgonLanes &&
		len(p.salt) == ArgonSaltBytes && len(p.tag) == ArgonTagBytes
}

// hasher runs Argon2id with at most cap(slots) computations at once.
type hasher struct {
	slots chan struct{}
}

func newHasher(n int) *hasher { return &hasher{slots: make(chan struct{}, n)} }

func (h *hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("password: waiting for a free Argon2id slot (%d in use, each 64 MiB): %w", cap(h.slots), ctx.Err())
	}
}

func (h *hasher) release() { <-h.slots }

// hash returns the PHC string of a new Argon2id hash of pw:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<tag>, salt and tag in base64 without padding.
func (h *hasher) hash(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, ArgonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: reading %d random bytes for the salt: %w", ArgonSaltBytes, err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	tag := argon2.IDKey([]byte(pw), salt, ArgonPasses, ArgonMemoryKiB, ArgonLanes, ArgonTagBytes)
	h.release()
	return encodePHC(argonParams{memory: ArgonMemoryKiB, passes: ArgonPasses, lanes: ArgonLanes, salt: salt, tag: tag, version: argon2.Version}), nil
}

// verify reports whether pw matches the PHC string, and whether the string uses other parameters than a new hash
// would (so the caller may store a fresh hash after a successful login).
func (h *hasher) verify(ctx context.Context, phc, pw string) (ok, outdated bool, err error) {
	p, err := decodePHC(phc)
	if err != nil {
		return false, false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	tag := argon2.IDKey([]byte(pw), p.salt, p.passes, p.memory, p.lanes, uint32(len(p.tag)))
	h.release()
	return subtle.ConstantTimeCompare(tag, p.tag) == 1, !p.current(), nil
}

var b64 = base64.RawStdEncoding

func encodePHC(p argonParams) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", p.version, p.memory, p.passes, p.lanes, b64.EncodeToString(p.salt), b64.EncodeToString(p.tag))
}

var errPHC = errors.New("password: the stored hash is not an Argon2id PHC string")

func decodePHC(s string) (argonParams, error) {
	var p argonParams
	parts := strings.Split(s, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, tag
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, errPHC
	}
	v, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return p, errPHC
	}
	version, err := strconv.Atoi(v)
	if err != nil || version != argon2.Version {
		return p, fmt.Errorf("%w (version %q; only %d is supported)", errPHC, v, argon2.Version)
	}
	p.version = version
	var m, t, l uint64
	for _, kv := range strings.Split(parts[3], ",") {
		k, val, ok := strings.Cut(kv, "=")
		if !ok {
			return p, errPHC
		}
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return p, fmt.Errorf("%w (parameter %q)", errPHC, kv)
		}
		switch k {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			l = n
		default:
			return p, fmt.Errorf("%w (unknown parameter %q)", errPHC, k)
		}
	}
	if m < 8 || m > maxStoredMemoryKiB || t < 1 || t > maxStoredPasses || l < 1 || l > maxStoredLanes || m < 8*l {
		return p, fmt.Errorf("%w (parameters m=%d,t=%d,p=%d are out of the accepted range m<=%d, t<=%d, p<=%d)", errPHC, m, t, l, maxStoredMemoryKiB, maxStoredPasses, maxStoredLanes)
	}
	p.memory, p.passes, p.lanes = uint32(m), uint32(t), uint8(l)
	if p.salt, err = b64.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return p, fmt.Errorf("%w (salt)", errPHC)
	}
	if p.tag, err = b64.DecodeString(parts[5]); err != nil || len(p.tag) < 16 || len(p.tag) > 64 {
		return p, fmt.Errorf("%w (tag)", errPHC)
	}
	return p, nil
}
