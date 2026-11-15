package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/donnyhardyanto/dxlib/log"

	"github.com/donnyhardyanto/dxlib_module/module/auth_audit"
)

// Connection runs the store's commands on Valkey. credentials.RedisConnection implements it: a dxlib redis
// connection made with credentials from the credential provider, reconnecting when they rotate. Replies are plain
// RESP values (string, int64, []interface{}), a nil reply is (nil, nil), and op names the operation in errors, so
// no client library type is part of this interface.
type Connection interface {
	// Eval runs a Lua script (EVALSHA, then EVAL when the server does not have it cached).
	Eval(ctx context.Context, op, script string, keys []string, args ...interface{}) (interface{}, error)
	// HGet returns a hash field and whether it exists.
	HGet(ctx context.Context, op, key, field string) (string, bool, error)
}

// ValkeyStore is the Store in Valkey. Every multi-key change is one Lua script, so it is atomic.
type ValkeyStore struct {
	conn   Connection
	cfg    Config
	audit  auth_audit.Sink
	loader ProfileLoader
	// Log receives the propagation failures (also audited). Default dxlib's log.Log.
	Log *log.DXLog
}

var _ Store = (*ValkeyStore)(nil)

// NewValkeyStore returns a store. audit is required (every login and logout is audited); loader rebuilds a
// session's profile when its cached copy is missing and may be nil, in which case such a session is refused.
func NewValkeyStore(conn Connection, cfg Config, audit auth_audit.Sink, loader ProfileLoader) (*ValkeyStore, error) {
	if conn == nil {
		return nil, errors.New("session: NewValkeyStore needs a Connection")
	}
	if audit == nil {
		return nil, errors.New("session: NewValkeyStore needs an audit Sink")
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &ValkeyStore{conn: conn, cfg: c, audit: audit, loader: loader, Log: &log.Log}, nil
}

// Config returns the store's configuration with its defaults filled in (for Cookie and TokenFromRequest).
func (s *ValkeyStore) Config() Config { return s.cfg }

func (s *ValkeyStore) sessionKey(id string) string { return s.cfg.KeyPrefix + "s:" + id }
func (s *ValkeyStore) indexKey(u string) string    { return s.cfg.KeyPrefix + "u:" + u }

// versionKey holds the highest profile version propagated for a user, so a login or rebuild that read an older
// profile from the database just before a change cannot store it after the change was propagated.
func (s *ValkeyStore) versionKey(u string) string { return s.cfg.KeyPrefix + "v:" + u }

// The scripts read the time from Valkey (TIME), so every instance of the service shares one clock.
const luaNow = `local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local function n(x) return string.format('%.0f', x) end
`

// KEYS: session, index, version marker. ARGV: uuid, method, csrf, profile, version, idle ms, absolute ms, id.
// Returns {created, absolute expiry, 1 when the profile was stored or 0 when it is older than the propagated one}.
const scriptCreate = luaNow + `
local idle, abs = tonumber(ARGV[6]), tonumber(ARGV[7])
if redis.call('EXISTS', KEYS[1]) == 1 then return redis.error_reply('SESSION_TOKEN_COLLISION') end
local ta = now + abs
local mv = tonumber(redis.call('HGET', KEYS[3], 'pv') or '')
local fresh = (not mv) or tonumber(ARGV[5]) >= mv
redis.call('HSET', KEYS[1], 'u', ARGV[1], 'm', ARGV[2], 'c', ARGV[3], 't0', n(now), 'ta', n(ta))
if fresh then redis.call('HSET', KEYS[1], 'p', ARGV[4], 'pv', ARGV[5]) end
redis.call('PEXPIRE', KEYS[1], n(math.min(idle, abs)))
redis.call('SADD', KEYS[2], ARGV[8])
if redis.call('PTTL', KEYS[2]) < abs then redis.call('PEXPIRE', KEYS[2], n(abs)) end
if fresh then return {n(now), n(ta), 1} end
return {n(now), n(ta), 0}
`

// KEYS: session. ARGV: idle ms, prefix, id.
const scriptGet = luaNow + `
local v = redis.call('HMGET', KEYS[1], 'u', 'm', 'c', 't0', 'ta', 'p', 'pv')
if not v[1] then return false end
local ta = tonumber(v[5])
if (not ta) or ta <= now then
  redis.call('DEL', KEYS[1])
  redis.call('SREM', ARGV[2] .. 'u:' .. v[1], ARGV[3])
  return false
end
redis.call('PEXPIRE', KEYS[1], n(math.min(tonumber(ARGV[1]), ta - now)))
return v
`

// KEYS: old session, new session, version marker. ARGV: prefix, old id, new id, new csrf, idle ms,
// profile (empty: keep), version (empty: keep). A given profile older than the cached or propagated one is ignored.
const scriptRotate = luaNow + `
local v = redis.call('HMGET', KEYS[1], 'u', 'm', 't0', 'ta', 'p', 'pv')
if not v[1] then return false end
local idx = ARGV[1] .. 'u:' .. v[1]
local ta = tonumber(v[4])
if (not ta) or ta <= now then
  redis.call('DEL', KEYS[1])
  redis.call('SREM', idx, ARGV[2])
  return false
end
if redis.call('EXISTS', KEYS[2]) == 1 then return redis.error_reply('SESSION_TOKEN_COLLISION') end
local p, pv = v[5], v[6]
local mv = tonumber(redis.call('HGET', KEYS[3], 'pv') or '')
if ARGV[6] ~= '' and ((not mv) or tonumber(ARGV[7]) >= mv) and ((not pv) or tonumber(pv) <= tonumber(ARGV[7])) then p, pv = ARGV[6], ARGV[7] end
redis.call('HSET', KEYS[2], 'u', v[1], 'm', v[2], 'c', ARGV[4], 't0', v[3], 'ta', v[4])
if p then redis.call('HSET', KEYS[2], 'p', p) end
if pv then redis.call('HSET', KEYS[2], 'pv', pv) end
redis.call('PEXPIRE', KEYS[2], n(math.min(tonumber(ARGV[5]), ta - now)))
redis.call('DEL', KEYS[1])
redis.call('SREM', idx, ARGV[2])
redis.call('SADD', idx, ARGV[3])
return {v[1], v[2], ARGV[4], v[3], v[4], p, pv}
`

// KEYS: session. ARGV: prefix, id, owner uuid (empty: any owner). Returns the owner's uuid, or false.
const scriptDelete = `
local u = redis.call('HGET', KEYS[1], 'u')
if not u then return false end
if ARGV[3] ~= '' and u ~= ARGV[3] then return false end
redis.call('DEL', KEYS[1])
redis.call('SREM', ARGV[1] .. 'u:' .. u, ARGV[2])
return u
`

// KEYS: index. ARGV: prefix. Returns the number of sessions deleted.
const scriptDeleteAll = `
local ids = redis.call('SMEMBERS', KEYS[1])
local count = 0
for _, id in ipairs(ids) do count = count + redis.call('DEL', ARGV[1] .. 's:' .. id) end
redis.call('DEL', KEYS[1])
return count
`

// KEYS: index, version marker. ARGV: prefix, profile, version, uuid, absolute ms. Returns {updated, newer, stale}.
// The marker keeps the highest version for as long as a session can live, also when the user has no session yet.
const scriptPropagate = `
local mv = tonumber(redis.call('HGET', KEYS[2], 'pv') or '')
if (not mv) or tonumber(ARGV[3]) > mv then redis.call('HSET', KEYS[2], 'pv', ARGV[3]) end
redis.call('PEXPIRE', KEYS[2], ARGV[5])
local ids = redis.call('SMEMBERS', KEYS[1])
local updated, newer, stale = 0, 0, 0
for _, id in ipairs(ids) do
  local k = ARGV[1] .. 's:' .. id
  local cur = redis.call('HMGET', k, 'u', 'pv')
  if (not cur[1]) or cur[1] ~= ARGV[4] then
    redis.call('SREM', KEYS[1], id)
    stale = stale + 1
  elseif cur[2] and tonumber(cur[2]) > tonumber(ARGV[3]) then
    newer = newer + 1
  else
    redis.call('HSET', k, 'p', ARGV[2], 'pv', ARGV[3])
    updated = updated + 1
  end
end
return {updated, newer, stale}
`

// KEYS: index. ARGV: prefix, uuid. Drops the cached profile of every session of the user.
const scriptInvalidate = `
local ids = redis.call('SMEMBERS', KEYS[1])
for _, id in ipairs(ids) do
  local k = ARGV[1] .. 's:' .. id
  if redis.call('HGET', k, 'u') == ARGV[2] then redis.call('HDEL', k, 'p') else redis.call('SREM', KEYS[1], id) end
end
return #ids
`

// KEYS: session, version marker. ARGV: profile, version, uuid. Writes a rebuilt profile unless a newer one is
// cached or was propagated (-1), or the session is gone (0).
const scriptSetProfile = `
local cur = redis.call('HMGET', KEYS[1], 'u', 'pv', 'p')
if cur[1] ~= ARGV[3] then return 0 end
local mv = tonumber(redis.call('HGET', KEYS[2], 'pv') or '')
if mv and tonumber(ARGV[2]) < mv then return -1 end
if cur[3] and cur[2] and tonumber(cur[2]) > tonumber(ARGV[2]) then return -1 end
redis.call('HSET', KEYS[1], 'p', ARGV[1], 'pv', ARGV[2])
return 1
`

// KEYS: index. ARGV: prefix, uuid. Returns {id, method, created, absolute, profile, version} per live session.
const scriptList = luaNow + `
local ids = redis.call('SMEMBERS', KEYS[1])
local out = {}
for _, id in ipairs(ids) do
  local v = redis.call('HMGET', ARGV[1] .. 's:' .. id, 'u', 'm', 't0', 'ta', 'p', 'pv')
  if v[1] == ARGV[2] and tonumber(v[4]) and tonumber(v[4]) > now then
    table.insert(out, {id, v[2], v[3], v[4], v[5], v[6]})
  else
    redis.call('SREM', KEYS[1], id)
  end
end
return out
`

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

func (s *ValkeyStore) run(ctx context.Context, op string, script string, keys []string, args ...interface{}) (interface{}, error) {
	return s.conn.Eval(ctx, op, script, keys, args...)
}

func (s *ValkeyStore) writeAudit(ctx context.Context, e auth_audit.Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if err := s.audit.Write(ctx, e); err != nil {
		return fmt.Errorf("session: writing the %s audit record for user %s: %w", e.Kind, e.UserUUID, err)
	}
	return nil
}

func encodeProfile(p Profile) (string, string, error) {
	if p.Roles == nil {
		p.Roles = []string{}
	}
	if p.Permissions == nil {
		p.Permissions = []string{}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", "", fmt.Errorf("session: encoding the profile: %w", err)
	}
	return string(b), strconv.FormatInt(p.Version, 10), nil
}

func asString(v interface{}) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func msTime(v interface{}) time.Time {
	s, _ := asString(v)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

func decodeProfile(raw, version interface{}) (Profile, bool) {
	rs, ok := asString(raw)
	if !ok || rs == "" {
		return Profile{}, false
	}
	var p Profile
	if err := json.Unmarshal([]byte(rs), &p); err != nil {
		return Profile{}, false
	}
	if vs, ok := asString(version); ok {
		p.Version, _ = strconv.ParseInt(vs, 10, 64)
	}
	return p, true
}

// Create starts a session. When Valkey cannot store it the login is refused with Valkey's own error, and the
// refusal is audited; there is no fallback. When the audit record cannot be written the new session is deleted
// again and the login refused, so no session exists without its audit record.
func (s *ValkeyStore) Create(ctx context.Context, login LoginInfo) (string, *Session, error) {
	userUUID, err := canonicalUUID(login.UserUUID)
	if err != nil {
		return "", nil, err
	}
	if !authMethodPattern.MatchString(login.AuthMethod) {
		return "", nil, fmt.Errorf("session: auth method %q must be 1 to 32 lower-case letters, digits, _ or -", truncate(login.AuthMethod, 40))
	}
	detail := map[string]string{"auth_method": login.AuthMethod, "client_address": login.ClientAddress, "user_agent": truncate(login.UserAgent, 256)}
	token, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	csrf, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	id, _ := SessionID(token)
	profile, version, err := encodeProfile(login.Profile)
	if err != nil {
		return "", nil, err
	}
	res, err := s.run(ctx, "session create", scriptCreate,
		[]string{s.sessionKey(id), s.indexKey(userUUID), s.versionKey(userUUID)},
		userUUID, login.AuthMethod, csrf, profile, version, ms(s.cfg.IdleTTL), ms(s.cfg.AbsoluteTTL), id)
	if err != nil {
		err = fmt.Errorf("session: login refused, the session store could not save the session: %w", err)
		if aerr := s.writeAudit(ctx, auth_audit.Event{Kind: auth_audit.LoginFailed, UserUUID: userUUID, Outcome: auth_audit.OutcomeFailed, Reason: err.Error(), Detail: detail}); aerr != nil {
			return "", nil, errors.Join(err, aerr)
		}
		return "", nil, err
	}
	arr, _ := res.([]interface{})
	if len(arr) != 3 {
		return "", nil, fmt.Errorf("session create: unexpected reply from Valkey (%T, %d items)", res, len(arr))
	}
	sess := &Session{ID: id, UserUUID: userUUID, AuthMethod: login.AuthMethod, CSRFToken: csrf,
		CreatedAt: msTime(arr[0]), AbsoluteExpiry: msTime(arr[1]), Profile: login.Profile}
	if arr[2] == int64(0) {
		// The profile the login read is older than a change already propagated: take the current one from the
		// authority, or refuse the login.
		p, rerr := s.rebuildProfile(ctx, sess)
		if rerr != nil {
			rerr = fmt.Errorf("session: login refused: profile version %d of user %s is older than the version already propagated, and reloading it failed: %w", login.Profile.Version, userUUID, rerr)
			if _, derr := s.run(context.WithoutCancel(ctx), "session delete after a stale profile", scriptDelete, []string{s.sessionKey(id)}, s.cfg.KeyPrefix, id, ""); derr != nil {
				return "", nil, errors.Join(rerr, fmt.Errorf("session: the session %s could not be deleted: %w", id, derr))
			}
			return "", nil, rerr
		}
		sess.Profile = p
	}
	if aerr := s.writeAudit(ctx, auth_audit.Event{Kind: auth_audit.LoginSucceeded, UserUUID: userUUID, SessionID: id, Outcome: auth_audit.OutcomeOK, Detail: detail}); aerr != nil {
		if _, derr := s.run(context.WithoutCancel(ctx), "session delete after a failed audit", scriptDelete, []string{s.sessionKey(id)}, s.cfg.KeyPrefix, id, ""); derr != nil {
			return "", nil, errors.Join(fmt.Errorf("session: login refused: %w", aerr), fmt.Errorf("session: the unaudited session %s could not be deleted: %w", id, derr))
		}
		return "", nil, fmt.Errorf("session: login refused: %w", aerr)
	}
	return token, sess, nil
}

// Get returns the session of a token and slides its idle TTL, never past its absolute expiry.
func (s *ValkeyStore) Get(ctx context.Context, token string) (*Session, error) {
	id, err := SessionID(token)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, "session get", scriptGet, []string{s.sessionKey(id)}, ms(s.cfg.IdleTTL), s.cfg.KeyPrefix, id)
	if err != nil {
		return nil, fmt.Errorf("session: reading the session failed: %w", err)
	}
	if res == nil {
		return nil, ErrNotFound
	}
	v, _ := res.([]interface{})
	if len(v) != 7 {
		return nil, fmt.Errorf("session get: unexpected reply from Valkey (%T, %d items)", res, len(v))
	}
	sess := &Session{ID: id, CreatedAt: msTime(v[3]), AbsoluteExpiry: msTime(v[4])}
	sess.UserUUID, _ = asString(v[0])
	sess.AuthMethod, _ = asString(v[1])
	sess.CSRFToken, _ = asString(v[2])
	p, ok := decodeProfile(v[5], v[6])
	if !ok {
		p, err = s.rebuildProfile(ctx, sess)
		if err != nil {
			return nil, err
		}
	}
	sess.Profile = p
	return sess, nil
}

// rebuildProfile reloads a missing or unreadable cached profile from the authoritative store.
func (s *ValkeyStore) rebuildProfile(ctx context.Context, sess *Session) (Profile, error) {
	if s.loader == nil {
		return Profile{}, fmt.Errorf("session: session %s of user %s has no cached profile and the store has no ProfileLoader to rebuild it", sess.ID, sess.UserUUID)
	}
	p, err := s.loader.LoadProfile(ctx, sess.UserUUID)
	if err != nil {
		return Profile{}, fmt.Errorf("session: rebuilding the profile of user %s from the authoritative store: %w", sess.UserUUID, err)
	}
	raw, version, err := encodeProfile(p)
	if err != nil {
		return Profile{}, err
	}
	res, err := s.run(ctx, "session profile rebuild", scriptSetProfile, []string{s.sessionKey(sess.ID), s.versionKey(sess.UserUUID)}, raw, version, sess.UserUUID)
	if err != nil {
		return Profile{}, fmt.Errorf("session: saving the rebuilt profile of user %s: %w", sess.UserUUID, err)
	}
	switch res {
	case int64(0):
		return Profile{}, ErrNotFound
	case int64(-1):
		return Profile{}, fmt.Errorf("session: the authoritative store returned profile version %d of user %s, older than the one already cached or propagated; refusing the stale copy", p.Version, sess.UserUUID)
	}
	return p, nil
}

// Rotate gives a session a new token and CSRF token.
func (s *ValkeyStore) Rotate(ctx context.Context, token string, profile *Profile) (string, *Session, error) {
	oldID, err := SessionID(token)
	if err != nil {
		return "", nil, err
	}
	newToken, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	csrf, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	newID, _ := SessionID(newToken)
	raw, version := "", ""
	if profile != nil {
		if raw, version, err = encodeProfile(*profile); err != nil {
			return "", nil, err
		}
	}
	// The owner is known only inside the script, so the version marker is named by the user the token's session
	// belongs to: read it first. A token with no session ends here as ErrNotFound.
	owner, err := s.ownerOf(ctx, oldID)
	if err != nil {
		return "", nil, err
	}
	res, err := s.run(ctx, "session rotate", scriptRotate, []string{s.sessionKey(oldID), s.sessionKey(newID), s.versionKey(owner)},
		s.cfg.KeyPrefix, oldID, newID, csrf, ms(s.cfg.IdleTTL), raw, version)
	if err != nil {
		return "", nil, fmt.Errorf("session: rotating the session token failed: %w", err)
	}
	if res == nil {
		return "", nil, ErrNotFound
	}
	v, _ := res.([]interface{})
	if len(v) < 5 {
		return "", nil, fmt.Errorf("session rotate: unexpected reply from Valkey (%T, %d items)", res, len(v))
	}
	for len(v) < 7 {
		v = append(v, nil) // Lua drops trailing nils from a table
	}
	sess := &Session{ID: newID, CSRFToken: csrf, CreatedAt: msTime(v[3]), AbsoluteExpiry: msTime(v[4])}
	sess.UserUUID, _ = asString(v[0])
	sess.AuthMethod, _ = asString(v[1])
	p, ok := decodeProfile(v[5], v[6])
	if !ok {
		if p, err = s.rebuildProfile(ctx, sess); err != nil {
			// The new session exists but its token is never handed out and it has no audit record: delete it, as
			// Create does. The old token is already gone.
			err = fmt.Errorf("session: rotation refused, the session is ended: %w", err)
			if _, derr := s.run(context.WithoutCancel(ctx), "session delete after a failed profile rebuild", scriptDelete, []string{s.sessionKey(newID)}, s.cfg.KeyPrefix, newID, ""); derr != nil {
				return "", nil, errors.Join(err, fmt.Errorf("session: the rotated session %s could not be deleted: %w", newID, derr))
			}
			return "", nil, err
		}
	}
	sess.Profile = p
	if aerr := s.writeAudit(ctx, auth_audit.Event{Kind: auth_audit.SessionRotated, UserUUID: sess.UserUUID, SessionID: newID, Outcome: auth_audit.OutcomeOK, Detail: map[string]string{"previous_session_id": oldID}}); aerr != nil {
		// As at login: no session lives without its audit record. The old token is already gone.
		if _, derr := s.run(context.WithoutCancel(ctx), "session delete after a failed audit", scriptDelete, []string{s.sessionKey(newID)}, s.cfg.KeyPrefix, newID, ""); derr != nil {
			return "", nil, errors.Join(fmt.Errorf("session: rotation refused: %w", aerr), fmt.Errorf("session: the unaudited session %s could not be deleted: %w", newID, derr))
		}
		return "", nil, fmt.Errorf("session: rotation refused, the session is ended: %w", aerr)
	}
	return newToken, sess, nil
}

// ownerOf returns the user uuid of a session id, or ErrNotFound.
func (s *ValkeyStore) ownerOf(ctx context.Context, id string) (string, error) {
	owner, _, err := s.conn.HGet(ctx, "session owner", s.sessionKey(id), "u")
	if err != nil {
		return "", fmt.Errorf("session: reading the session failed: %w", err)
	}
	if owner == "" {
		return "", ErrNotFound
	}
	return owner, nil
}

// Logout deletes the session of a token. A token with no session is ErrNotFound. When the audit record cannot be
// written the session is still deleted and the audit error returned.
func (s *ValkeyStore) Logout(ctx context.Context, token string) error {
	id, err := SessionID(token)
	if err != nil {
		return err
	}
	res, err := s.run(ctx, "session logout", scriptDelete, []string{s.sessionKey(id)}, s.cfg.KeyPrefix, id, "")
	if err != nil {
		return fmt.Errorf("session: logout could not delete the session: %w", err)
	}
	u, ok := asString(res)
	if !ok {
		return ErrNotFound
	}
	return s.writeAudit(ctx, auth_audit.Event{Kind: auth_audit.Logout, UserUUID: u, SessionID: id, Outcome: auth_audit.OutcomeOK})
}

// List returns a user's open sessions, without CSRF tokens.
func (s *ValkeyStore) List(ctx context.Context, userUUID string) ([]Session, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, "session list", scriptList, []string{s.indexKey(u)}, s.cfg.KeyPrefix, u)
	if err != nil {
		return nil, fmt.Errorf("session: listing the sessions of user %s failed: %w", u, err)
	}
	rows, _ := res.([]interface{})
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		v, _ := r.([]interface{})
		for len(v) < 6 {
			v = append(v, nil)
		}
		sess := Session{UserUUID: u, CreatedAt: msTime(v[2]), AbsoluteExpiry: msTime(v[3])}
		sess.ID, _ = asString(v[0])
		sess.AuthMethod, _ = asString(v[1])
		sess.Profile, _ = decodeProfile(v[4], v[5])
		out = append(out, sess)
	}
	return out, nil
}

// RevokeSession ends one session of the caller. A session id of another user is ErrNotFound, the same answer as
// a session that does not exist, so ids of other users cannot be probed.
func (s *ValkeyStore) RevokeSession(ctx context.Context, callerUserUUID, sessionID string) error {
	u, err := canonicalUUID(callerUserUUID)
	if err != nil {
		return err
	}
	if !sessionIDPattern.MatchString(sessionID) {
		return ErrNotFound
	}
	res, err := s.run(ctx, "session revoke", scriptDelete, []string{s.sessionKey(sessionID)}, s.cfg.KeyPrefix, sessionID, u)
	if err != nil {
		return fmt.Errorf("session: revoking session %s failed: %w", sessionID, err)
	}
	if _, ok := asString(res); !ok {
		return ErrNotFound
	}
	return s.writeAudit(ctx, auth_audit.Event{Kind: auth_audit.SessionRevoked, UserUUID: u, SessionID: sessionID, Outcome: auth_audit.OutcomeOK})
}

func (s *ValkeyStore) deleteAll(ctx context.Context, u string) (int, error) {
	res, err := s.run(ctx, "session revoke all", scriptDeleteAll, []string{s.indexKey(u)}, s.cfg.KeyPrefix)
	if err != nil {
		return 0, err
	}
	n, _ := res.(int64)
	return int(n), nil
}

// RevokeAll ends every session of a user. It runs even when ctx has ended, within Config.OperationTimeout.
func (s *ValkeyStore) RevokeAll(ctx context.Context, userUUID, reason string) (int, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return 0, err
	}
	octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.OperationTimeout)
	defer cancel()
	n, err := s.deleteAll(octx, u)
	if err != nil {
		err = fmt.Errorf("session: deleting every session of user %s (%s) failed: %w", u, reason, err)
		s.Log.Errorf(err, "session revoke all failed")
		if aerr := s.writeAudit(octx, auth_audit.Event{Kind: auth_audit.SessionsRevoked, UserUUID: u, Outcome: auth_audit.OutcomeFailed, Reason: err.Error(), Detail: map[string]string{"reason": reason}}); aerr != nil {
			return 0, errors.Join(err, aerr)
		}
		return 0, err
	}
	return n, s.writeAudit(octx, auth_audit.Event{Kind: auth_audit.SessionsRevoked, UserUUID: u, Outcome: auth_audit.OutcomeOK, Detail: map[string]string{"reason": reason, "sessions": strconv.Itoa(n)}})
}

// PropagateProfile writes a changed profile into every session of the user in one atomic step. Call it after the
// database commit; it runs even when ctx has ended, within Config.OperationTimeout. When the write fails, every
// session of the user is deleted (fail closed), the cause logged and audited, and a *PropagationError returned.
func (s *ValkeyStore) PropagateProfile(ctx context.Context, userUUID string, p Profile) (PropagationResult, error) {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return PropagationResult{}, err
	}
	raw, version, err := encodeProfile(p)
	if err != nil {
		return PropagationResult{}, err
	}
	octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.OperationTimeout)
	defer cancel()
	res, err := s.run(octx, "session profile propagation", scriptPropagate, []string{s.indexKey(u), s.versionKey(u)}, s.cfg.KeyPrefix, raw, version, u, ms(s.cfg.AbsoluteTTL))
	if err == nil {
		arr, _ := res.([]interface{})
		if len(arr) == 3 {
			r := PropagationResult{}
			if x, ok := arr[0].(int64); ok {
				r.Updated = int(x)
			}
			if x, ok := arr[1].(int64); ok {
				r.Newer = int(x)
			}
			if x, ok := arr[2].(int64); ok {
				r.Stale = int(x)
			}
			return r, s.writeAudit(octx, auth_audit.Event{Kind: auth_audit.ProfilePropagated, UserUUID: u, Outcome: auth_audit.OutcomeOK,
				Detail: map[string]string{"version": version, "updated": strconv.Itoa(r.Updated), "newer": strconv.Itoa(r.Newer), "stale": strconv.Itoa(r.Stale)}})
		}
		err = fmt.Errorf("session profile propagation: unexpected reply from Valkey (%T, %d items)", res, len(arr))
	}
	perr := &PropagationError{UserUUID: u, Version: p.Version, UpdateErr: err}
	perr.Deleted, perr.DeleteErr = s.deleteAll(octx, u)
	s.Log.Errorf(perr, "session profile propagation failed")
	if aerr := s.writeAudit(octx, auth_audit.Event{Kind: auth_audit.PropagationFailed, UserUUID: u, Outcome: auth_audit.OutcomeFailed, Reason: perr.Error(),
		Detail: map[string]string{"version": version, "sessions_deleted": strconv.Itoa(perr.Deleted)}}); aerr != nil {
		return PropagationResult{}, errors.Join(perr, aerr)
	}
	return PropagationResult{}, perr
}

// InvalidateProfiles drops the cached profile from every session of a user; each is rebuilt on its next Get.
func (s *ValkeyStore) InvalidateProfiles(ctx context.Context, userUUID string) error {
	u, err := canonicalUUID(userUUID)
	if err != nil {
		return err
	}
	if _, err := s.run(ctx, "session profile invalidation", scriptInvalidate, []string{s.indexKey(u)}, s.cfg.KeyPrefix, u); err != nil {
		return fmt.Errorf("session: dropping the cached profiles of user %s failed: %w", u, err)
	}
	return nil
}
