package passkey

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// RelyingParty runs the passkey ceremonies.
type RelyingParty struct {
	cfg        Config
	wa         *webauthn.WebAuthn
	challenges challengeStore
	store      CredentialStore
	sessions   session.Store
	accounts   Accounts
	audit      auth_audit.Sink
	now        func() time.Time
}

// New returns a relying party. conn keeps the challenges in Valkey (credentials.RedisConnection); store keeps the
// passkeys; sessions starts the session of a passkey login; accounts answers whether a user may log in and whether
// they have a password; audit receives every registration, removal, login and failure. All are required.
func New(cfg Config, conn Connection, store CredentialStore, sessions session.Store, accounts Accounts, audit auth_audit.Sink) (*RelyingParty, error) {
	if conn == nil || store == nil || sessions == nil || accounts == nil || audit == nil {
		return nil, errors.New("passkey: New needs a Connection, a CredentialStore, a session.Store, Accounts and an audit Sink")
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: c.ChallengeTTL, TimeoutUVD: c.ChallengeTTL}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          c.RPID,
		RPDisplayName: c.RPDisplayName,
		RPOrigins:     append([]string(nil), c.RPOrigins...),
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts:              webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	return &RelyingParty{cfg: c, wa: wa, challenges: valkeyChallenges{conn: conn, prefix: c.KeyPrefix}, store: store,
		sessions: sessions, accounts: accounts, audit: audit, now: time.Now}, nil
}

// Config returns the configuration with its defaults filled in.
func (rp *RelyingParty) Config() Config { return rp.cfg }

const (
	kindRegister = "register"
	kindLogin    = "login"
)

// challengeRecord is what Valkey keeps between Begin and Finish.
type challengeRecord struct {
	Kind     string               `json:"k"`
	UserUUID string               `json:"u,omitempty"`
	Data     webauthn.SessionData `json:"d"`
}

// waUser is the library's view of a user: the user handle is the uuid's 16 bytes.
type waUser struct {
	id          []byte
	name        string
	displayName string
	creds       []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.id }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func userHandle(canonical string) []byte {
	u := uuid.MustParse(canonical)
	return u[:]
}

func encodeID(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func decodeCredential(p Passkey) (webauthn.Credential, error) {
	var c webauthn.Credential
	if err := json.Unmarshal(p.Credential, &c); err != nil {
		return c, fmt.Errorf("passkey %s of user %s: its stored credential record is not readable: %w", truncate(p.ID, 16), p.UserUUID, err)
	}
	return c, nil
}

// describe gives a library error with its type and debug text, which its Error() leaves out.
func describe(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		s := pe.Details
		if pe.Type != "" {
			s = pe.Type + ": " + s
		}
		if pe.DevInfo != "" {
			s += " (" + pe.DevInfo + ")"
		}
		if pe.Err != nil && pe.Err.Error() != pe.Details {
			s += ": " + pe.Err.Error()
		}
		return s
	}
	return err.Error()
}

func readResponse(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("there is no response body")
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response body: %w", err)
	}
	if len(b) > MaxResponseBytes {
		return nil, fmt.Errorf("the response body is larger than %d bytes", MaxResponseBytes)
	}
	return b, nil
}

func (rp *RelyingParty) putChallenge(ctx context.Context, rec challengeRecord) (string, error) {
	handle, err := newHandle()
	if err != nil {
		return "", err
	}
	v, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("passkey: encoding the challenge: %w", err)
	}
	if err := rp.challenges.put(ctx, handle, v, rp.cfg.ChallengeTTL); err != nil {
		return "", fmt.Errorf("passkey: the challenge could not be stored in Valkey: %w", err)
	}
	return handle, nil
}

// takeChallenge reads and deletes the challenge in one step, so it is used once even when the answer is refused.
func (rp *RelyingParty) takeChallenge(ctx context.Context, handle, kind string) (*challengeRecord, error) {
	if err := checkHandle(handle); err != nil {
		return nil, err
	}
	v, err := rp.challenges.take(ctx, handle)
	if err != nil {
		return nil, fmt.Errorf("the challenge could not be read from Valkey: %w", err)
	}
	if v == nil {
		return nil, errors.New("the challenge is unknown, already used or expired")
	}
	var rec challengeRecord
	if err := json.Unmarshal(v, &rec); err != nil {
		return nil, fmt.Errorf("the stored challenge is not readable: %w", err)
	}
	if rec.Kind != kind {
		return nil, fmt.Errorf("the challenge was issued for a %s, not a %s", rec.Kind, kind)
	}
	return &rec, nil
}

func (rp *RelyingParty) detail(c Client, ceremony string) map[string]string {
	return map[string]string{"auth_method": AuthMethod, "ceremony": ceremony, "client_address": c.Address, "user_agent": truncate(c.UserAgent, 256)}
}

// refuse audits a refused ceremony and returns its error, wrapping ErrRefused.
func (rp *RelyingParty) refuse(ctx context.Context, kind auth_audit.Kind, userUUID, op string, cause error, d map[string]string) error {
	err := fmt.Errorf("%w: %s: %s", ErrRefused, op, cause)
	if aerr := rp.audit.Write(ctx, auth_audit.Event{Time: rp.now(), Kind: kind, UserUUID: userUUID, Outcome: auth_audit.OutcomeFailed, Reason: err.Error(), Detail: d}); aerr != nil {
		return errors.Join(err, fmt.Errorf("passkey: the audit record of this refusal could not be written: %w", aerr))
	}
	return err
}

// BeginRegistration starts adding a passkey for the logged-in user. It returns the options to hand to
// navigator.credentials.create() (JSON, {"publicKey": ...}) and the handle the browser sends back with its answer.
func (rp *RelyingParty) BeginRegistration(ctx context.Context, u User) (options []byte, handle string, err error) {
	userUUID, err := canonicalUUID(u.UUID)
	if err != nil {
		return nil, "", err
	}
	existing, err := rp.store.ByUser(ctx, userUUID)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: reading the passkeys of user %s: %w", userUUID, err)
	}
	if len(existing) >= rp.cfg.MaxPerUser {
		return nil, "", fmt.Errorf("%w (%d)", ErrTooMany, rp.cfg.MaxPerUser)
	}
	// The user's passkeys are excluded, so an authenticator that already holds one says so instead of making
	// a second.
	exclude := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, p := range existing {
		c, err := decodeCredential(p)
		if err != nil {
			return nil, "", fmt.Errorf("passkey: %w", err)
		}
		exclude = append(exclude, c.Descriptor())
	}
	name := u.Username
	if name == "" {
		name = userUUID
	}
	display := u.DisplayName
	if display == "" {
		display = name
	}
	creation, sd, err := rp.wa.BeginRegistration(&waUser{id: userHandle(userUUID), name: name, displayName: display},
		webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()))
	if err != nil {
		return nil, "", fmt.Errorf("passkey: starting the registration: %s", describe(err))
	}
	handle, err = rp.putChallenge(ctx, challengeRecord{Kind: kindRegister, UserUUID: userUUID, Data: *sd})
	if err != nil {
		return nil, "", err
	}
	options, err = json.Marshal(creation)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: encoding the registration options: %w", err)
	}
	return options, handle, nil
}

// FinishRegistration checks the browser's answer to BeginRegistration and stores the passkey under the given name
// (check it first with CheckName to tell the user what is wrong). userUUID is the logged-in user, who must be the
// one the registration was started for. A refused answer returns an error wrapping ErrRefused.
func (rp *RelyingParty) FinishRegistration(ctx context.Context, userUUID, handle, name string, response io.Reader, client Client) (*Passkey, error) {
	d := rp.detail(client, kindRegister)
	refuse := func(user string, cause error) (*Passkey, error) {
		return nil, rp.refuse(ctx, auth_audit.CredentialRegistered, user, "registration refused", cause, d)
	}
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return refuse("", err)
	}
	rec, err := rp.takeChallenge(ctx, handle, kindRegister)
	if err != nil {
		return refuse(u, err)
	}
	if rec.UserUUID != u {
		return refuse(u, fmt.Errorf("the challenge was issued to user %s", rec.UserUUID))
	}
	name, err = CheckName(name)
	if err != nil {
		return refuse(u, err)
	}
	d["passkey_name"] = name
	body, err := readResponse(response)
	if err != nil {
		return refuse(u, err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return refuse(u, fmt.Errorf("the answer is not a WebAuthn registration: %s", describe(err)))
	}
	d["passkey_id"] = truncate(parsed.ID, 128)
	cred, err := rp.wa.CreateCredential(&waUser{id: userHandle(u)}, rec.Data, parsed)
	if err != nil {
		return refuse(u, fmt.Errorf("the registration did not verify: %s", describe(err)))
	}
	if !cred.Flags.UserVerified {
		return refuse(u, errors.New("the authenticator did not verify the user"))
	}
	if cp := parsed.ClientExtensionResults.CredProps; cp != nil && cp.RK != nil && !*cp.RK {
		return refuse(u, errors.New("the authenticator made a credential that is not discoverable"))
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return refuse(u, fmt.Errorf("encoding the credential record: %w", err))
	}
	p := Passkey{ID: encodeID(cred.ID), UserUUID: u, Name: name, CreatedAt: rp.now().UTC().Truncate(time.Microsecond),
		BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState, Credential: raw}
	d["passkey_id"] = p.ID
	d["backup_eligible"] = strconv.FormatBool(p.BackupEligible)
	if err := rp.store.Add(ctx, p, rp.cfg.MaxPerUser); err != nil {
		return refuse(u, fmt.Errorf("storing the passkey: %w", err))
	}
	if aerr := rp.audit.Write(ctx, auth_audit.Event{Time: rp.now(), Kind: auth_audit.CredentialRegistered, UserUUID: u, Outcome: auth_audit.OutcomeOK, Detail: d}); aerr != nil {
		// No passkey stays without its audit record.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rp.cfg.OperationTimeout)
		defer cancel()
		err := fmt.Errorf("passkey: registration refused, its audit record could not be written: %w", aerr)
		if derr := rp.store.Remove(cctx, u, p.ID, nil); derr != nil {
			return nil, errors.Join(err, fmt.Errorf("passkey: the unaudited passkey %s of user %s could not be deleted: %w", p.ID, u, derr))
		}
		return nil, err
	}
	return &p, nil
}

// BeginLogin starts a passkey login. It returns the options to hand to navigator.credentials.get() (JSON,
// {"publicKey": ...}, with no allowed credentials: the authenticator offers the user's discoverable passkeys) and
// the handle the browser sends back with its answer.
func (rp *RelyingParty) BeginLogin(ctx context.Context) (options []byte, handle string, err error) {
	assertion, sd, err := rp.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", fmt.Errorf("passkey: starting the login: %s", describe(err))
	}
	handle, err = rp.putChallenge(ctx, challengeRecord{Kind: kindLogin, Data: *sd})
	if err != nil {
		return nil, "", err
	}
	options, err = json.Marshal(assertion)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: encoding the login options: %w", err)
	}
	return options, handle, nil
}

// FinishLogin checks the browser's answer to BeginLogin and starts the user's session (auth method "passkey"). It
// returns the session token for the cookie and the session. A refused answer returns an error wrapping ErrRefused;
// a session store failure is returned as session.Store.Create gives it (Create audits it).
func (rp *RelyingParty) FinishLogin(ctx context.Context, handle string, response io.Reader, client Client) (string, *session.Session, error) {
	d := rp.detail(client, kindLogin)
	userUUID := ""
	refuse := func(cause error) (string, *session.Session, error) {
		return "", nil, rp.refuse(ctx, auth_audit.LoginFailed, userUUID, "login refused", cause, d)
	}
	rec, err := rp.takeChallenge(ctx, handle, kindLogin)
	if err != nil {
		return refuse(err)
	}
	body, err := readResponse(response)
	if err != nil {
		return refuse(err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return refuse(fmt.Errorf("the answer is not a WebAuthn assertion: %s", describe(err)))
	}
	id := encodeID(parsed.RawID)
	d["passkey_id"] = truncate(id, 128)
	var found *Passkey
	handler := func(rawID, handleBytes []byte) (webauthn.User, error) {
		p, err := rp.store.ByID(ctx, encodeID(rawID))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(handleBytes, userHandle(p.UserUUID)) {
			return nil, fmt.Errorf("the passkey belongs to user %s, but the authenticator named another user handle", p.UserUUID)
		}
		c, err := decodeCredential(*p)
		if err != nil {
			return nil, err
		}
		found = p
		userUUID = p.UserUUID
		return &waUser{id: userHandle(p.UserUUID), creds: []webauthn.Credential{c}}, nil
	}
	_, cred, err := rp.wa.ValidatePasskeyLogin(handler, rec.Data, parsed)
	if err != nil {
		return refuse(fmt.Errorf("the assertion did not verify: %s", describe(err)))
	}
	if found == nil || !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
		return refuse(errors.New("the authenticator did not verify the user"))
	}
	if cred.Authenticator.CloneWarning {
		// A counter that does not go up means two authenticators hold the same key: one is a copy.
		return refuse(fmt.Errorf("the passkey's signature counter went from %d to %d, so the passkey may have been copied", cred.Authenticator.SignCount, parsed.Response.AuthenticatorData.Counter))
	}
	profile, err := rp.accounts.LoginProfile(ctx, found.UserUUID)
	if err != nil {
		return refuse(fmt.Errorf("the account may not log in: %w", err))
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return refuse(fmt.Errorf("encoding the credential record: %w", err))
	}
	if err := rp.store.RecordUse(ctx, found.UserUUID, found.ID, raw, rp.now().UTC().Truncate(time.Microsecond)); err != nil {
		return refuse(fmt.Errorf("the passkey's new signature counter could not be stored: %w", err))
	}
	token, sess, err := rp.sessions.Create(ctx, session.LoginInfo{UserUUID: found.UserUUID, AuthMethod: AuthMethod, Profile: profile,
		ClientAddress: client.Address, UserAgent: client.UserAgent, AuditDetail: map[string]string{"passkey_id": found.ID}})
	if err != nil {
		return "", nil, fmt.Errorf("passkey: the passkey verified, but the session could not be started: %w", err)
	}
	return token, sess, nil
}

// List returns the user's passkeys, oldest first.
func (rp *RelyingParty) List(ctx context.Context, userUUID string) ([]Passkey, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return nil, err
	}
	return rp.store.ByUser(ctx, u)
}

// Remove deletes one of the user's passkeys. It returns ErrNotFound when the user holds no passkey with this id (also
// when another user does), and ErrLastWayIn when it is the user's last passkey and they have no password.
func (rp *RelyingParty) Remove(ctx context.Context, userUUID, id string, client Client) error {
	d := rp.detail(client, "remove")
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return err
	}
	d["passkey_id"] = truncate(id, 128)
	fail := func(err error) error {
		if aerr := rp.audit.Write(ctx, auth_audit.Event{Time: rp.now(), Kind: auth_audit.CredentialRemoved, UserUUID: u, Outcome: auth_audit.OutcomeFailed, Reason: err.Error(), Detail: d}); aerr != nil {
			return errors.Join(err, fmt.Errorf("passkey: the audit record of this refusal could not be written: %w", aerr))
		}
		return err
	}
	if err := checkID(id); err != nil {
		return fail(err)
	}
	allow := func(remaining int) error {
		if remaining > 0 {
			return nil
		}
		has, err := rp.accounts.HasPassword(ctx, u)
		if err != nil {
			return fmt.Errorf("passkey: asking whether user %s has a password, before removing their last passkey: %w", u, err)
		}
		if !has {
			return ErrLastWayIn
		}
		return nil
	}
	if err := rp.store.Remove(ctx, u, id, allow); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrLastWayIn) {
			return fail(err)
		}
		return fail(fmt.Errorf("passkey: removing passkey %s of user %s: %w", truncate(id, 16), u, err))
	}
	if aerr := rp.audit.Write(ctx, auth_audit.Event{Time: rp.now(), Kind: auth_audit.CredentialRemoved, UserUUID: u, Outcome: auth_audit.OutcomeOK, Detail: d}); aerr != nil {
		return fmt.Errorf("passkey: passkey %s of user %s was removed, but its audit record could not be written: %w", truncate(id, 16), u, aerr)
	}
	return nil
}
