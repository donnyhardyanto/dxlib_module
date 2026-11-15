package passkey

// A software authenticator for the tests: it answers navigator.credentials.create() and .get() options the way a
// browser with a platform authenticator would, with an ES256 key and "none" attestation, and lets a test set the
// flags, counter, origin and user handle it reports.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

const (
	testRPID   = "login.example.test"
	testOrigin = "https://login.example.test"
)

type softKey struct {
	priv       *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	counter    uint32
	// backup holds the BE and BS flags it registered with; get reports them again, as a real authenticator does.
	backup byte
}

func (k *softKey) id() string { return base64.RawURLEncoding.EncodeToString(k.credID) }

type softAuthenticator struct {
	rpID   string
	origin string
}

func newSoftAuthenticator() *softAuthenticator {
	return &softAuthenticator{rpID: testRPID, origin: testOrigin}
}

type optionsJSON struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
		ExcludeCredentials []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
		UserVerification       string `json:"userVerification"`
		AuthenticatorSelection struct {
			ResidentKey      string `json:"residentKey"`
			UserVerification string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Extensions map[string]interface{} `json:"extensions"`
		Timeout    int                    `json:"timeout"`
	} `json:"publicKey"`
}

func parseOptions(t *testing.T, options []byte) optionsJSON {
	t.Helper()
	var o optionsJSON
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatalf("options are not JSON: %v\n%s", err, options)
	}
	if o.PublicKey.Challenge == "" {
		t.Fatalf("options hold no challenge: %s", options)
	}
	return o
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *softAuthenticator) clientData(t *testing.T, typ, challenge string) []byte {
	t.Helper()
	cd, err := json.Marshal(map[string]interface{}{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return cd
}

func (a *softAuthenticator) authData(flags byte, counter uint32, attested []byte) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	out := append([]byte(nil), rp[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, counter)
	return append(out, attested...)
}

// newKey makes a key pair and a random 32-byte credential id.
func newKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{priv: priv, credID: id}
}

// create answers registration options with key k (a new one when nil). rk is the credProps result (nil: not
// reported).
func (a *softAuthenticator) create(t *testing.T, options []byte, k *softKey, flags byte, rk *bool) ([]byte, *softKey) {
	t.Helper()
	o := parseOptions(t, options)
	if k == nil {
		k = newKey(t)
	}
	handle, err := base64.RawURLEncoding.DecodeString(o.PublicKey.User.ID)
	if err != nil {
		t.Fatalf("user id %q is not base64url: %v", o.PublicKey.User.ID, err)
	}
	k.userHandle = handle
	k.backup = flags & (flagBE | flagBS)
	pub, err := k.priv.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	xy := pub.Bytes() // 0x04 | x | y
	cose, err := webauthncbor.Marshal(map[int]interface{}{1: 2, 3: -7, -1: 1, -2: xy[1:33], -3: xy[33:65]})
	if err != nil {
		t.Fatal(err)
	}
	attested := make([]byte, 16) // AAGUID: zero
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(k.credID)))
	attested = append(attested, k.credID...)
	attested = append(attested, cose...)
	att, err := webauthncbor.Marshal(map[string]interface{}{"fmt": "none", "attStmt": map[string]interface{}{}, "authData": a.authData(flags|flagAT, k.counter, attested)})
	if err != nil {
		t.Fatal(err)
	}
	ext := map[string]interface{}{}
	if rk != nil {
		ext["credProps"] = map[string]interface{}{"rk": *rk}
	}
	resp, err := json.Marshal(map[string]interface{}{
		"id": k.id(), "rawId": k.id(), "type": "public-key",
		"response":               map[string]interface{}{"clientDataJSON": b64(a.clientData(t, "webauthn.create", o.PublicKey.Challenge)), "attestationObject": b64(att)},
		"clientExtensionResults": ext,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp, k
}

// get answers login options with key k, signing with its counter, and reports userHandle as the user handle.
func (a *softAuthenticator) get(t *testing.T, options []byte, k *softKey, flags byte, userHandle []byte) []byte {
	t.Helper()
	o := parseOptions(t, options)
	cd := a.clientData(t, "webauthn.get", o.PublicKey.Challenge)
	ad := a.authData(flags|k.backup, k.counter, nil)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte(nil), ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	resp, err := json.Marshal(map[string]interface{}{
		"id": k.id(), "rawId": k.id(), "type": "public-key",
		"response":               map[string]interface{}{"clientDataJSON": b64(cd), "authenticatorData": b64(ad), "signature": b64(sig), "userHandle": b64(userHandle)},
		"clientExtensionResults": map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
