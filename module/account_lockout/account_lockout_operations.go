package account_lockout

import (
	"fmt"
	"time"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

// CheckLockStatus checks if a user account is locked
// Returns: isLocked, remainingTimeSeconds, error
func (al *DXMAccountLockout) CheckLockStatus(userID int64) (bool, int64, error) {
	if !al.Config.Enabled {
		return false, 0, nil
	}

	return al.CheckLockStatusRedis(userID)
}

// RecordFailedAttempt records a failed login attempt and locks if threshold reached
func (al *DXMAccountLockout) RecordFailedAttempt(
	aepr *api.DXAPIEndPointRequest,
	userID int64,
	userUID string,
	userLoginID string,
	organizationID int64,
	organizationUID string,
	attemptType string,
	attemptIP string,
	attemptUserAgent string,
	attemptAuthSource string,
) error {
	if !al.Config.Enabled {
		return nil
	}

	// Check if we should track this attempt type
	if attemptType == AttemptTypePassword && !al.Config.TrackPasswordFailures {
		return nil
	}
	if attemptType == AttemptTypeLDAP && !al.Config.TrackLDAPFailures {
		return nil
	}

	// Increment counter in Redis
	count, err := al.IncrementFailedAttemptCounterRedis(userID, attemptIP, attemptType)
	if err != nil {
		log.Log.Errorf(err, "Failed to increment counter for user %d", userID)
		// Don't fail the authentication on counter error
		return nil
	}

	log.Log.Infof("Failed login attempt recorded: user_id=%d, count=%d/%d, type=%s, ip=%s",
		userID, count, al.Config.MaxFailedAttempts, attemptType, attemptIP)

	// Log to audit database
	if al.Config.LogFailedAttempts {
		event := &LockoutEvent{
			EventType:           EventTypeFailedAttempt,
			EventTimestamp:      time.Now().Format(time.RFC3339),
			UserID:              userID,
			UserUID:             userUID,
			UserLoginID:         userLoginID,
			OrganizationID:      organizationID,
			OrganizationUID:     organizationUID,
			FailedAttemptsCount: int(count),
			AttemptType:         attemptType,
			AttemptIPAddress:    attemptIP,
			AttemptUserAgent:    attemptUserAgent,
			AttemptAuthSource:   attemptAuthSource,
		}
		al.writeAuditLog(event)
	}

	// Check if threshold reached - lock account
	if count >= int64(al.Config.MaxFailedAttempts) {
		err := al.lockAccount(userID, userUID, userLoginID, organizationID, organizationUID, count, "EXCEEDED_FAILED_ATTEMPTS")
		if err != nil {
			log.Log.Errorf(err, "Failed to lock account for user %d", userID)
		}
	}

	return nil
}

// RecordSuccessfulLogin records successful login and resets counters
func (al *DXMAccountLockout) RecordSuccessfulLogin(userID int64, userUID string, userLoginID string) error {
	if !al.Config.Enabled {
		return nil
	}

	if !al.Config.ResetCounterOnSuccess {
		return nil
	}

	// Reset counter in Redis
	err := al.ResetCounterRedis(userID)
	if err != nil {
		log.Log.Warnf("Failed to reset counter for user %d: %v", userID, err)
	}

	// Log successful login if enabled
	if al.Config.LogSuccessfulLogins {
		event := &LockoutEvent{
			EventType:      "SUCCESSFUL_LOGIN",
			EventTimestamp: time.Now().Format(time.RFC3339),
			UserID:         userID,
			UserUID:        userUID,
			UserLoginID:    userLoginID,
		}
		al.writeAuditLog(event)
	}

	return nil
}

// UnlockAccount manually unlocks an account (admin action)
func (al *DXMAccountLockout) UnlockAccount(
	userID int64,
	userUID string,
	userLoginID string,
	unlockedByUserID int64,
	unlockedByUserUID string,
	reason string,
) error {
	if !al.Config.Enabled {
		return nil
	}

	// Remove lock from Redis
	err := al.UnlockAccountRedis(userID)
	if err != nil {
		return fmt.Errorf("failed to unlock in Redis: %w", err)
	}

	// Write audit log
	event := &LockoutEvent{
		EventType:         EventTypeAccountUnlockedAdmin,
		EventTimestamp:    time.Now().Format(time.RFC3339),
		UserID:            userID,
		UserUID:           userUID,
		UserLoginID:       userLoginID,
		UnlockedByUserID:  unlockedByUserID,
		UnlockedByUserUID: unlockedByUserUID,
		UnlockReason:      reason,
	}
	al.writeAuditLog(event)

	log.Log.Infof("Account manually unlocked: user_id=%d, unlocked_by=%d, reason=%s",
		userID, unlockedByUserID, reason)

	return nil
}

// lockAccount locks the account (internal helper)
func (al *DXMAccountLockout) lockAccount(
	userID int64,
	userUID string,
	userLoginID string,
	organizationID int64,
	organizationUID string,
	failedCount int64,
	reason string,
) error {
	// Lock in Redis
	err := al.LockAccountRedis(userID, failedCount, reason)
	if err != nil {
		return fmt.Errorf("failed to lock in Redis: %w", err)
	}

	// Write audit log
	now := time.Now()
	unlockAt := now.Add(time.Duration(al.Config.LockoutDurationMinutes) * time.Minute)

	event := &LockoutEvent{
		EventType:              EventTypeAccountLocked,
		EventTimestamp:         now.Format(time.RFC3339),
		UserID:                 userID,
		UserUID:                userUID,
		UserLoginID:            userLoginID,
		OrganizationID:         organizationID,
		OrganizationUID:        organizationUID,
		LockoutReason:          reason,
		FailedAttemptsCount:    int(failedCount),
		LockoutDurationSeconds: al.Config.LockoutDurationMinutes * 60,
		LockedAt:               now.Format(time.RFC3339),
		UnlockAt:               unlockAt.Format(time.RFC3339),
	}
	al.writeAuditLog(event)

	log.Log.Warnf("ACCOUNT LOCKED: user_id=%d, loginid=%s, failed_attempts=%d, duration=%d min",
		userID, userLoginID, failedCount, al.Config.LockoutDurationMinutes)

	return nil
}

// GetLockoutStatus gets detailed lockout status for a user
func (al *DXMAccountLockout) GetLockoutStatus(userID int64) (utils.JSON, error) {
	isLocked, remainingSeconds, err := al.CheckLockStatus(userID)
	if err != nil {
		return nil, err
	}

	status := utils.JSON{
		"is_locked":         isLocked,
		"remaining_seconds": remainingSeconds,
	}

	if isLocked {
		unlockAt := time.Now().Add(time.Duration(remainingSeconds) * time.Second)
		status["unlock_at"] = unlockAt.Format(time.RFC3339)

		// Get failed attempt count
		count, _ := al.GetFailedAttemptCountRedis(userID)
		status["failed_attempts"] = count
		status["lockout_reason"] = "EXCEEDED_FAILED_ATTEMPTS"
	}

	return status, nil
}
