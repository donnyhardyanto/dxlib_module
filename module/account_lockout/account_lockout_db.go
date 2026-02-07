package account_lockout

import (
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

// writeAuditLog writes lockout event to audit database
func (al *DXMAccountLockout) writeAuditLog(event *LockoutEvent) {
	if al.Config.AsyncDBWrites {
		// Send to async queue
		select {
		case al.asyncWriteQueue <- event:
			// Queued successfully
		default:
			// Queue full, write synchronously
			log.Log.Warnf("Audit write queue full, writing synchronously")
			al.writeAuditLogSync(event)
		}
	} else {
		// Write synchronously
		al.writeAuditLogSync(event)
	}
}

// writeAuditLogSync writes to database synchronously
func (al *DXMAccountLockout) writeAuditLogSync(event *LockoutEvent) {
	data := utils.JSON{
		"event_type":               event.EventType,
		"event_timestamp":          event.EventTimestamp,
		"user_id":                  event.UserID,
		"user_uid":                 event.UserUID,
		"user_loginid":             event.UserLoginID,
		"organization_id":          event.OrganizationID,
		"organization_uid":         event.OrganizationUID,
		"lockout_reason":           event.LockoutReason,
		"failed_attempts_count":    event.FailedAttemptsCount,
		"lockout_duration_seconds": event.LockoutDurationSeconds,
		"locked_at":                event.LockedAt,
		"unlock_at":                event.UnlockAt,
		"unlocked_by_user_id":      event.UnlockedByUserID,
		"unlocked_by_user_uid":     event.UnlockedByUserUID,
		"unlock_reason":            event.UnlockReason,
		"attempt_type":             event.AttemptType,
		"attempt_ip_address":       event.AttemptIPAddress,
		"attempt_user_agent":       event.AttemptUserAgent,
		"attempt_auth_source":      event.AttemptAuthSource,
		"metadata":                 event.Metadata,
	}

	_, _, err := al.AccountLockoutEvents.Insert(nil, data, nil)
	if err != nil {
		log.Log.Errorf(err, "Failed to write audit log")
	}
}

// GetLockoutHistory retrieves lockout history for a user
func (al *DXMAccountLockout) GetLockoutHistory(userID int64, limit int) ([]utils.JSON, error) {
	where := utils.JSON{"user_id": userID}
	orderBy := map[string]string{"event_timestamp": "DESC"}

	_, events, err := al.AccountLockoutEvents.Select(nil, nil, where, nil, orderBy, limit, nil)
	if err != nil {
		return nil, err
	}

	return events, nil
}
