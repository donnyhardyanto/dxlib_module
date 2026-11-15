package passkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Connection runs the challenge commands on Valkey. credentials.RedisConnection implements it: a dxlib redis
// connection made with credentials from the credential provider, reconnecting when they rotate. A nil reply is
// (nil, nil); op names the operation in errors.
type Connection interface {
	Eval(ctx context.Context, op, script string, keys []string, args ...interface{}) (interface{}, error)
}

// challengeStore keeps a ceremony's state until the browser answers. The Valkey one is the only one a service
// gets; tests swap in memoryChallenges to run the ceremonies without a server.
type challengeStore interface {
	put(ctx context.Context, handle string, value []byte, ttl time.Duration) error
	// take returns the value and deletes it in one step; (nil, nil) when it is missing or expired.
	take(ctx context.Context, handle string) ([]byte, error)
}

// KEYS: challenge. ARGV: value, ttl ms. SET NX: a handle is 256 random bits, so a collision means a bug.
const scriptPut = `
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then return 1 end
return redis.error_reply('PASSKEY_CHALLENGE_COLLISION')
`

// KEYS: challenge. Returns the value and deletes it, or nil.
const scriptTake = `
local v = redis.call('GET', KEYS[1])
if v then redis.call('DEL', KEYS[1]) end
return v
`

type valkeyChallenges struct {
	conn   Connection
	prefix string
}

// key is the prefix and the SHA-256 of the handle: the handle the browser holds is never a key name.
func (v valkeyChallenges) key(handle string) string {
	sum := sha256.Sum256([]byte(handle))
	return v.prefix + "c:" + hex.EncodeToString(sum[:])
}

func (v valkeyChallenges) put(ctx context.Context, handle string, value []byte, ttl time.Duration) error {
	_, err := v.conn.Eval(ctx, "passkey challenge store", scriptPut, []string{v.key(handle)}, string(value), strconv.FormatInt(ttl.Milliseconds(), 10))
	return err
}

func (v valkeyChallenges) take(ctx context.Context, handle string) ([]byte, error) {
	res, err := v.conn.Eval(ctx, "passkey challenge take", scriptTake, []string{v.key(handle)})
	if err != nil || res == nil {
		return nil, err
	}
	s, ok := res.(string)
	if !ok {
		return nil, fmt.Errorf("passkey challenge take: unexpected reply from Valkey (%T)", res)
	}
	return []byte(s), nil
}

// memoryChallenges is for the package's tests only.
type memoryChallenges struct {
	mu   sync.Mutex
	m    map[string]memoryChallenge
	fail error
}

type memoryChallenge struct {
	v       []byte
	expires time.Time
}

func (m *memoryChallenges) put(_ context.Context, handle string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	if m.m == nil {
		m.m = map[string]memoryChallenge{}
	}
	m.m[handle] = memoryChallenge{v: value, expires: time.Now().Add(ttl)}
	return nil
}

func (m *memoryChallenges) take(_ context.Context, handle string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	c, ok := m.m[handle]
	delete(m.m, handle)
	if !ok || time.Now().After(c.expires) {
		return nil, nil
	}
	return c.v, nil
}

const handleBytes = 32

func newHandle() (string, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("passkey: reading %d random bytes for a challenge handle: %w", handleBytes, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// checkHandle refuses anything but a handle this package issued before it reaches Valkey.
func checkHandle(h string) error {
	if len(h) != base64.RawURLEncoding.EncodedLen(handleBytes) {
		return errors.New("the challenge handle is malformed")
	}
	if raw, err := base64.RawURLEncoding.Strict().DecodeString(h); err != nil || len(raw) != handleBytes {
		return errors.New("the challenge handle is malformed")
	}
	return nil
}
