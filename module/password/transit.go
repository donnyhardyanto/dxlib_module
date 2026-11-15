package password

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Transit encrypts and decrypts with a key that never leaves the secret store. OpenBaoTransit is the
// implementation for a service; InsecureTestTransit is for tests only.
type Transit interface {
	// Encrypt encrypts plaintext with the newest key version.
	Encrypt(ctx context.Context, plaintext []byte) (Ciphertext, error)
	// Decrypt decrypts a ciphertext made by Encrypt or Rewrap with any key version still allowed to decrypt.
	Decrypt(ctx context.Context, ciphertext string) ([]byte, error)
	// Rewrap re-encrypts a ciphertext with the newest key version, without handing out the plaintext.
	Rewrap(ctx context.Context, ciphertext string) (Ciphertext, error)
	// LatestVersion is the newest key version.
	LatestVersion(ctx context.Context) (int, error)
}

// Ciphertext is an encrypted value and the key version that encrypted it.
type Ciphertext struct {
	Text       string
	KeyVersion int
}

// keyVersionOf reads the version from a transit ciphertext ("vault:v3:..." or "test:v3:...").
func keyVersionOf(ct string) (int, error) {
	parts := strings.SplitN(ct, ":", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "v") {
		return 0, fmt.Errorf("password: %q is not a transit ciphertext (<prefix>:v<version>:<data>)", truncate(ct, 24))
	}
	v, err := strconv.Atoi(parts[1][1:])
	if err != nil || v < 1 {
		return 0, fmt.Errorf("password: transit ciphertext %q has no valid key version", truncate(ct, 24))
	}
	return v, nil
}

// The plaintext given to Transit: a format tag, the owner's uuid and the PHC string, NUL separated. Decrypting checks
// the uuid, so a value copied into another user's row is refused.
const envelopeTag = "dxpw1"

var errOtherUser = errors.New("the stored password belongs to another user (the row was copied or altered)")

func seal(userUUID, phc string) []byte {
	return []byte(envelopeTag + "\x00" + userUUID + "\x00" + phc)
}

func open(userUUID string, plaintext []byte) (string, error) {
	parts := bytes.SplitN(plaintext, []byte{0}, 3)
	if len(parts) != 3 || string(parts[0]) != envelopeTag {
		return "", errors.New("the decrypted value is not a stored password of this package")
	}
	if string(parts[1]) != userUUID {
		return "", errOtherUser
	}
	return string(parts[2]), nil
}

// InsecureTestTransit is a Transit holding AES-256-GCM keys in memory. It is for tests only: its keys live in the
// process and die with it, so every password stored with it is unreadable after a restart. New refuses it outside
// `go test`.
type InsecureTestTransit struct {
	mu   sync.RWMutex
	keys []cipher.AEAD // keys[v-1] is version v
	// MinDecryptionVersion, when set, refuses to decrypt ciphertexts of older versions, as a trimmed transit key does.
	MinDecryptionVersion int
	// Fail, when set, is returned by every call.
	Fail error
}

// NewInsecureTestTransit returns an InsecureTestTransit with one key version.
func NewInsecureTestTransit() *InsecureTestTransit {
	t := &InsecureTestTransit{}
	t.Rotate()
	return t
}

// Rotate adds a new key version, as rotating a transit key does.
func (t *InsecureTestTransit) Rotate() {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	t.mu.Lock()
	t.keys = append(t.keys, aead)
	t.mu.Unlock()
}

func (t *InsecureTestTransit) encrypt(plaintext []byte) (Ciphertext, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v := len(t.keys)
	k := t.keys[v-1]
	nonce := make([]byte, k.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Ciphertext{}, err
	}
	out := k.Seal(nonce, nonce, plaintext, nil)
	return Ciphertext{Text: "test:v" + strconv.Itoa(v) + ":" + base64.StdEncoding.EncodeToString(out), KeyVersion: v}, nil
}

func (t *InsecureTestTransit) decrypt(ct string) ([]byte, error) {
	v, err := keyVersionOf(ct)
	if err != nil {
		return nil, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !strings.HasPrefix(ct, "test:") || v > len(t.keys) {
		return nil, fmt.Errorf("test transit: no key version %d", v)
	}
	if v < t.MinDecryptionVersion {
		return nil, fmt.Errorf("test transit: key version %d is below the minimum decryption version %d", v, t.MinDecryptionVersion)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.SplitN(ct, ":", 3)[2])
	if err != nil {
		return nil, fmt.Errorf("test transit: ciphertext is not base64: %w", err)
	}
	k := t.keys[v-1]
	if len(raw) < k.NonceSize() {
		return nil, errors.New("test transit: ciphertext too short")
	}
	pt, err := k.Open(nil, raw[:k.NonceSize()], raw[k.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("test transit: decrypting with key version %d: %w", v, err)
	}
	return pt, nil
}

// Encrypt encrypts with the newest key version.
func (t *InsecureTestTransit) Encrypt(_ context.Context, plaintext []byte) (Ciphertext, error) {
	if t.Fail != nil {
		return Ciphertext{}, t.Fail
	}
	return t.encrypt(plaintext)
}

// Decrypt decrypts with the ciphertext's key version.
func (t *InsecureTestTransit) Decrypt(_ context.Context, ct string) ([]byte, error) {
	if t.Fail != nil {
		return nil, t.Fail
	}
	return t.decrypt(ct)
}

// Rewrap re-encrypts with the newest key version.
func (t *InsecureTestTransit) Rewrap(_ context.Context, ct string) (Ciphertext, error) {
	if t.Fail != nil {
		return Ciphertext{}, t.Fail
	}
	pt, err := t.decrypt(ct)
	if err != nil {
		return Ciphertext{}, err
	}
	return t.encrypt(pt)
}

// LatestVersion is the newest key version.
func (t *InsecureTestTransit) LatestVersion(context.Context) (int, error) {
	if t.Fail != nil {
		return 0, t.Fail
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.keys), nil
}

var _ Transit = (*InsecureTestTransit)(nil)
