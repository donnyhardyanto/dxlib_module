// Package accountlockout feeds module/password's logins to module/account_lockout.
//
// account_lockout keys its counters on the service's numeric user id and login id, while module/password knows the
// user by uuid; Resolve maps one to the other. Run account_lockout with RedisFailMode REDIS_FAIL_THEN_LOCK: in
// REDIS_FAIL_KEEP_UNLOCK it answers "not locked" when Redis is down, so logins go on without a lockout.
package accountlockout

import (
	"context"
	"errors"
	"fmt"

	"github.com/donnyhardyanto/dxlib/api"

	"github.com/donnyhardyanto/dxlib_module/module/account_lockout"
	"github.com/donnyhardyanto/dxlib_module/module/password"
)

// Resolver returns the service's numeric user id and login id of a user uuid.
type Resolver func(ctx context.Context, userUUID string) (userID int64, loginID string, err error)

// Adapter is a password.Lockout on an initialised *account_lockout.DXMAccountLockout.
type Adapter struct {
	lockout *account_lockout.DXMAccountLockout
	resolve Resolver
}

var _ password.Lockout = (*Adapter)(nil)

// New returns an Adapter. l must be initialised (its Config loaded).
func New(l *account_lockout.DXMAccountLockout, resolve Resolver) (*Adapter, error) {
	if l == nil || l.Config == nil {
		return nil, errors.New("accountlockout: New needs an initialised DXMAccountLockout (its Config is not loaded)")
	}
	if resolve == nil {
		return nil, errors.New("accountlockout: New needs a Resolver from user uuid to user id and login id")
	}
	return &Adapter{lockout: l, resolve: resolve}, nil
}

func (a *Adapter) ids(ctx context.Context, userUUID string) (int64, string, error) {
	id, login, err := a.resolve(ctx, userUUID)
	if err != nil {
		return 0, "", fmt.Errorf("accountlockout: resolving user %s to its numeric id: %w", userUUID, err)
	}
	return id, login, nil
}

// Locked asks account_lockout whether the user is locked.
func (a *Adapter) Locked(ctx context.Context, userUUID string) (bool, error) {
	id, _, err := a.ids(ctx, userUUID)
	if err != nil {
		return false, err
	}
	locked, _, err := a.lockout.CheckLockStatus(id)
	if err != nil {
		return false, fmt.Errorf("accountlockout: checking the lock of user %s (id %d): %w", userUUID, id, err)
	}
	return locked, nil
}

// Failed records a failed password login.
func (a *Adapter) Failed(ctx context.Context, userUUID string, client password.Client) error {
	id, login, err := a.ids(ctx, userUUID)
	if err != nil {
		return err
	}
	// account_lockout reads only the request's Context.
	req := &api.DXAPIEndPointRequest{Context: ctx}
	if err := a.lockout.RecordFailedAttempt(req, id, userUUID, login, 0, "", account_lockout.AttemptTypePassword,
		client.Address, client.UserAgent, password.AuthMethod); err != nil {
		return fmt.Errorf("accountlockout: recording a failed login of user %s (id %d): %w", userUUID, id, err)
	}
	return nil
}

// Succeeded records a successful login.
func (a *Adapter) Succeeded(ctx context.Context, userUUID string) error {
	id, login, err := a.ids(ctx, userUUID)
	if err != nil {
		return err
	}
	if err := a.lockout.RecordSuccessfulLogin(ctx, id, userUUID, login); err != nil {
		return fmt.Errorf("accountlockout: recording a successful login of user %s (id %d): %w", userUUID, id, err)
	}
	return nil
}

// Unlock ends the user's lock.
func (a *Adapter) Unlock(ctx context.Context, userUUID, reason string) error {
	id, login, err := a.ids(ctx, userUUID)
	if err != nil {
		return err
	}
	if err := a.lockout.UnlockAccount(ctx, id, userUUID, login, 0, "", reason); err != nil {
		return fmt.Errorf("accountlockout: unlocking user %s (id %d): %w", userUUID, id, err)
	}
	return nil
}
