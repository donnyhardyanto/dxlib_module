package user_photo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/donnyhardyanto/dxlib/log"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/module/session"
)

// Profiles composes the session profile: the service's own loader gives everything but the photo, and the
// photo reference and version come from the Store. Hand it to session.NewValkeyStore as the ProfileLoader, and
// build every login's profile through Fill (password and passkey Accounts.LoginProfile), so a session always
// carries the photo the Store names.
//
// The profile's Version stays the service's own: a photo change propagates the profile at the version the
// service's loader returns, and never raises it, so the session store's version marker stays on the service's
// scale and a later login or name change is never taken for an older copy.
type Profiles struct {
	inner session.ProfileLoader
	store Store
	cfg   Config
}

// NewProfiles returns the composer over the service's loader.
func NewProfiles(inner session.ProfileLoader, store Store, cfg Config) (*Profiles, error) {
	if inner == nil || store == nil {
		return nil, errors.New("user_photo: NewProfiles needs the service's ProfileLoader and a Store")
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Profiles{inner: inner, store: store, cfg: c}, nil
}

// LoadProfile loads the service's profile and fills in the photo.
func (p *Profiles) LoadProfile(ctx context.Context, userUUID string) (session.Profile, error) {
	prof, err := p.inner.LoadProfile(ctx, userUUID)
	if err != nil {
		return prof, err
	}
	if err := p.Fill(ctx, userUUID, &prof); err != nil {
		return session.Profile{}, err
	}
	return prof, nil
}

// Fill sets the profile's PhotoRef and PhotoVersion from the Store (empty and 0 when the user has no photo).
func (p *Profiles) Fill(ctx context.Context, userUUID string, prof *session.Profile) error {
	u, ok := canonicalUUID(userUUID)
	if !ok {
		return fmt.Errorf("user_photo: %q is not a user uuid", truncate(userUUID, 64))
	}
	prof.PhotoRef, prof.PhotoVersion = "", 0
	r, err := p.store.Get(ctx, u)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("user_photo: reading the photo row of user %s for the session profile: %w", u, err)
	}
	if r.ObjectKey != "" {
		prof.PhotoRef, prof.PhotoVersion = p.cfg.Ref(u, r.Version), r.Version
	}
	return nil
}

var _ session.ProfileLoader = (*Profiles)(nil)

// Dependencies are the parts a Manager works with.
type Dependencies struct {
	Store    Store
	Objects  ObjectStore
	Sessions session.Store
	// Profiles builds the profile propagated after a change (NewProfiles over the service's loader).
	Profiles *Profiles
	// Rights decides who may see and change a photo. Default SelfOrPermission{} (each user their own only).
	Rights Rights
	Audit  auth_audit.Sink
}

// Manager stores, serves and removes the photos.
type Manager struct {
	cfg         Config
	store       Store
	objects     ObjectStore
	sessions    session.Store
	profiles    *Profiles
	rights      Rights
	audit       auth_audit.Sink
	decodeSlots chan struct{}
	now         func() time.Time
	// Log receives what cannot be returned: an old object left behind, a failed clean-up. Default dxlib's log.Log.
	Log *log.DXLog
}

// New returns a Manager.
func New(cfg Config, deps Dependencies) (*Manager, error) {
	if deps.Store == nil || deps.Objects == nil || deps.Sessions == nil || deps.Profiles == nil || deps.Audit == nil {
		return nil, errors.New("user_photo: New needs a Store, an ObjectStore, a session.Store, Profiles and an audit Sink")
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if deps.Rights == nil {
		deps.Rights = SelfOrPermission{}
	}
	return &Manager{cfg: c, store: deps.Store, objects: deps.Objects, sessions: deps.Sessions, profiles: deps.Profiles,
		rights: deps.Rights, audit: deps.Audit, decodeSlots: make(chan struct{}, c.MaxConcurrentDecodes), now: time.Now, Log: &log.Log}, nil
}

// Config returns the configuration with its defaults filled in.
func (m *Manager) Config() Config { return m.cfg }

// detached gives a context for a step that must run even when the caller's context has ended.
func (m *Manager) detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), m.cfg.OperationTimeout)
}

func (m *Manager) write(ctx context.Context, e auth_audit.Event) error {
	actx, cancel := m.detached(ctx)
	defer cancel()
	e.Time = m.now()
	return m.audit.Write(actx, e)
}

// withAudit writes the event and adds a failure to write it to err.
func (m *Manager) withAudit(ctx context.Context, e auth_audit.Event, err error) error {
	if aerr := m.write(ctx, e); aerr != nil {
		if err == nil {
			return fmt.Errorf("user_photo: the %s audit record of user %s could not be written: %w", e.Kind, e.UserUUID, aerr)
		}
		return fmt.Errorf("%w (and its audit record could not be written: %v)", err, aerr)
	}
	return err
}

func actorOf(caller *session.Session, userUUID string) string {
	if caller == nil || caller.UserUUID == userUUID {
		return ""
	}
	return caller.UserUUID
}

// authorize checks the caller's right to change userUUID's photo. A refusal is audited and answered as
// ErrNotFound, so it does not tell whether the user exists.
func (m *Manager) authorize(ctx context.Context, kind auth_audit.Kind, caller *session.Session, userUUID string) (string, error) {
	if caller == nil || caller.UserUUID == "" {
		return "", ErrNoCaller
	}
	u, ok := canonicalUUID(userUUID)
	if !ok {
		return "", ErrNotFound
	}
	may, err := m.rights.MayChange(ctx, caller, u)
	if err != nil {
		return "", m.withAudit(ctx, auth_audit.Event{Kind: kind, UserUUID: u, ActorUUID: actorOf(caller, u), SessionID: caller.ID,
			Outcome: auth_audit.OutcomeFailed, Reason: "checking the caller's right failed: " + err.Error()},
			fmt.Errorf("user_photo: checking whether user %s may change the photo of user %s failed: %w", caller.UserUUID, u, err))
	}
	if !may {
		return "", m.withAudit(ctx, auth_audit.Event{Kind: kind, UserUUID: u, ActorUUID: actorOf(caller, u), SessionID: caller.ID,
			Outcome: auth_audit.OutcomeFailed, Reason: "the caller may not change this user's photo"}, ErrNotFound)
	}
	return u, nil
}

func newObjectKey(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("user_photo: reading 32 random bytes for an object key: %w", err)
	}
	return prefix + hex.EncodeToString(b) + ".webp", nil
}

// Set replaces the photo of userUUID with the image read from body, for the caller (their own photo, or anyone's
// when Rights allows). The service's route checks the caller's CSRF token (session.CheckCSRF) before calling.
//
// The image is refused (an error wrapping ErrRejected) when it is over Config.MaxBytes, not PNG, JPEG or WebP by
// its bytes, over the pixel limit or undecodable. A caller who may not change this photo gets ErrNotFound. Once
// the photo is stored, Change is returned and says what the sessions and the old object's clean-up did; an error
// beside a non-nil Change means only that the change's audit record could not be written.
func (m *Manager) Set(ctx context.Context, caller *session.Session, userUUID string, body io.Reader) (*Change, error) {
	u, err := m.authorize(ctx, auth_audit.PhotoChanged, caller, userUUID)
	if err != nil {
		return nil, err
	}
	fail := func(err error, d map[string]string) (*Change, error) {
		return nil, m.withAudit(ctx, auth_audit.Event{Kind: auth_audit.PhotoChanged, UserUUID: u, ActorUUID: actorOf(caller, u),
			SessionID: caller.ID, Outcome: auth_audit.OutcomeFailed, Reason: err.Error(), Detail: d}, err)
	}
	data, err := readLimited(body, m.cfg.MaxBytes)
	if err != nil {
		return fail(err, nil)
	}
	out, err := m.reencode(ctx, data)
	if err != nil {
		return fail(err, map[string]string{"upload_bytes": strconv.Itoa(len(data))})
	}
	key, err := newObjectKey(m.cfg.KeyPrefix)
	if err != nil {
		return fail(err, nil)
	}
	if err := m.objects.Put(ctx, key, out, ContentType); err != nil {
		return fail(fmt.Errorf("user_photo: storing the new photo of user %s: %w", u, err), nil)
	}
	before, after, err := m.store.Replace(ctx, u, key, m.now())
	if errors.Is(err, ErrConflict) {
		before, after, err = m.store.Replace(ctx, u, key, m.now())
	}
	if err != nil {
		err = fmt.Errorf("user_photo: recording the new photo of user %s: %w", u, err)
		d := map[string]string{}
		dctx, cancel := m.detached(ctx)
		if derr := m.objects.Delete(dctx, key); derr != nil {
			err = fmt.Errorf("%w (and deleting its unreferenced object %s failed too: %v)", err, key, derr)
			d["object_left"] = key
			m.Log.Errorf(derr, "user_photo: unreferenced object %s left after a failed database write", key)
		}
		cancel()
		return fail(err, d)
	}
	ch := &Change{Photo: Photo{UserUUID: u, Version: after.Version, Ref: m.cfg.Ref(u, after.Version), Bytes: len(out)}}
	d := map[string]string{"version": strconv.FormatInt(after.Version, 10), "bytes": strconv.Itoa(len(out)), "upload_bytes": strconv.Itoa(len(data))}
	m.finish(ctx, ch, u, before, d)
	return ch, m.withAudit(ctx, auth_audit.Event{Kind: auth_audit.PhotoChanged, UserUUID: u, ActorUUID: actorOf(caller, u),
		SessionID: caller.ID, Outcome: auth_audit.OutcomeOK, Detail: d}, nil)
}

// Remove removes the photo of userUUID for the caller. ErrNotFound when there is none or the caller may not. As
// with Set, an error beside a non-nil Change means only that the audit record could not be written.
func (m *Manager) Remove(ctx context.Context, caller *session.Session, userUUID string) (*Change, error) {
	u, err := m.authorize(ctx, auth_audit.PhotoRemoved, caller, userUUID)
	if err != nil {
		return nil, err
	}
	before, after, err := m.store.Replace(ctx, u, "", m.now())
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			err = fmt.Errorf("user_photo: recording the removal of the photo of user %s: %w", u, err)
		}
		return nil, m.withAudit(ctx, auth_audit.Event{Kind: auth_audit.PhotoRemoved, UserUUID: u, ActorUUID: actorOf(caller, u),
			SessionID: caller.ID, Outcome: auth_audit.OutcomeFailed, Reason: err.Error()}, err)
	}
	ch := &Change{Photo: Photo{UserUUID: u, Version: after.Version}}
	d := map[string]string{"version": strconv.FormatInt(after.Version, 10)}
	m.finish(ctx, ch, u, before, d)
	return ch, m.withAudit(ctx, auth_audit.Event{Kind: auth_audit.PhotoRemoved, UserUUID: u, ActorUUID: actorOf(caller, u),
		SessionID: caller.ID, Outcome: auth_audit.OutcomeOK, Detail: d}, nil)
}

// finish runs after the row is committed: delete the replaced object, then give every session of the user the
// new profile. Neither undoes the change; their failures go into ch, the audit detail d and the log.
func (m *Manager) finish(ctx context.Context, ch *Change, u string, before *Record, d map[string]string) {
	dctx, cancel := m.detached(ctx)
	defer cancel()
	if before != nil && before.ObjectKey != "" {
		if err := m.objects.Delete(dctx, before.ObjectKey); err != nil {
			ch.OldObjectErr = fmt.Errorf("user_photo: deleting the replaced photo object %s of user %s failed; it is no longer referenced and can be removed by hand: %w", before.ObjectKey, u, err)
			d["old_object_left"] = before.ObjectKey
			m.Log.Errorf(err, "user_photo: replaced object %s of user %s left behind", before.ObjectKey, u)
		}
	}
	prof, err := m.profiles.LoadProfile(dctx, u)
	if err != nil {
		// The new reference cannot be written, so drop every cached profile of the user: each session rebuilds
		// it from the authority on its next request. If even that fails, end the sessions (fail closed).
		ch.SessionsErr = fmt.Errorf("user_photo: loading the profile of user %s to propagate the photo failed: %w", u, err)
		if ierr := m.sessions.InvalidateProfiles(dctx, u); ierr != nil {
			n, rerr := m.sessions.RevokeAll(dctx, u, "photo change could not be propagated")
			if rerr != nil {
				ch.SessionsErr = fmt.Errorf("%w; dropping the cached profiles failed (%v) and ending the sessions failed too (%v)", ch.SessionsErr, ierr, rerr)
			} else {
				ch.SessionsErr = fmt.Errorf("%w; dropping the cached profiles failed (%v), so the user's %d sessions were ended", ch.SessionsErr, ierr, n)
			}
		} else {
			ch.SessionsErr = fmt.Errorf("%w; the cached profiles were dropped, each session reloads it on its next request", ch.SessionsErr)
		}
	} else {
		ch.Propagation, err = m.sessions.PropagateProfile(dctx, u, prof)
		if err != nil {
			ch.SessionsErr = err // a *session.PropagationError: the sessions were deleted (fail closed), already audited
		}
	}
	if ch.SessionsErr != nil {
		d["sessions_error"] = truncate(ch.SessionsErr.Error(), 512)
		m.Log.Errorf(ch.SessionsErr, "user_photo: sessions of user %s not given the photo change", u)
	} else {
		d["sessions_updated"] = strconv.Itoa(ch.Propagation.Updated)
	}
}

// DeleteUser removes the user's photo row and its object, as part of deleting the user (the service also ends
// the user's sessions with session.Store.RevokeAll). actorUUID names who deleted the user. A user without a photo
// row gives nil. When the object cannot be deleted the row is already gone, and the error names the object's key.
func (m *Manager) DeleteUser(ctx context.Context, userUUID, actorUUID string) error {
	u, ok := canonicalUUID(userUUID)
	if !ok {
		return fmt.Errorf("user_photo: %q is not a user uuid", truncate(userUUID, 64))
	}
	ev := auth_audit.Event{Kind: auth_audit.PhotoRemoved, UserUUID: u, ActorUUID: actorUUID, Detail: map[string]string{"reason": "user deleted"}}
	rec, err := m.store.Delete(ctx, u)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		err = fmt.Errorf("user_photo: deleting the photo row of user %s: %w", u, err)
		ev.Outcome, ev.Reason = auth_audit.OutcomeFailed, err.Error()
		return m.withAudit(ctx, ev, err)
	}
	ev.Detail["version"] = strconv.FormatInt(rec.Version, 10)
	if rec.ObjectKey != "" {
		dctx, cancel := m.detached(ctx)
		err = m.objects.Delete(dctx, rec.ObjectKey)
		cancel()
		if err != nil {
			err = fmt.Errorf("user_photo: the photo row of user %s is deleted, but deleting its object %s failed; remove it by hand: %w", u, rec.ObjectKey, err)
			ev.Outcome, ev.Reason = auth_audit.OutcomeFailed, err.Error()
			ev.Detail["object_left"] = rec.ObjectKey
			return m.withAudit(ctx, ev, err)
		}
	}
	ev.Outcome = auth_audit.OutcomeOK
	return m.withAudit(ctx, ev, nil)
}

// Image is a photo read for serving.
type Image struct {
	Data    []byte
	Version int64
}

// Get returns the photo of userUUID if the caller may see it: Rights is asked before anything is read. Every
// refusal, a malformed uuid, no photo and a removed one give ErrNotFound alike.
func (m *Manager) Get(ctx context.Context, caller *session.Session, userUUID string) (*Image, error) {
	if caller == nil || caller.UserUUID == "" {
		return nil, ErrNoCaller
	}
	u, ok := canonicalUUID(userUUID)
	if !ok {
		return nil, ErrNotFound
	}
	may, err := m.rights.MayView(ctx, caller, u)
	if err != nil {
		return nil, fmt.Errorf("user_photo: checking whether user %s may see the photo of user %s failed: %w", caller.UserUUID, u, err)
	}
	if !may {
		return nil, ErrNotFound
	}
	rec, err := m.store.Get(ctx, u)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("user_photo: reading the photo row of user %s: %w", u, err)
	}
	if rec.ObjectKey == "" {
		return nil, ErrNotFound
	}
	if !objectKeyPattern.MatchString(rec.ObjectKey) {
		return nil, fmt.Errorf("user_photo: the photo row of user %s names an object key %q this package never makes", u, truncate(rec.ObjectKey, 80))
	}
	data, err := m.objects.Get(ctx, rec.ObjectKey, HighestMaxBytes)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			m.Log.Errorf(err, "user_photo: the photo row of user %s (version %d) names object %s, which is missing", u, rec.Version, rec.ObjectKey)
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("user_photo: reading the photo of user %s: %w", u, err)
	}
	return &Image{Data: data, Version: rec.Version}, nil
}
