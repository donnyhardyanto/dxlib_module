package account_lockout

import (
	"net/http"

	"github.com/donnyhardyanto/dxlib/api"
)

// API Handlers - These match the API endpoint signature and can be registered directly
// Following Low IQ Tax Principle: ALL handlers in module, ONLY definitions in API file

// AccountLockoutStatusByUserUidHandler - Complete handler in module (FIXED/HOW)
func (al *DXMAccountLockout) AccountLockoutStatusByUserUidHandler(aepr *api.DXAPIEndPointRequest) error {
	_, userUid, err := aepr.GetParameterValueAsString("user_uid")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PARAMETER_USER_UID_REQUIRED", "Parameter user_uid is required")
	}

	return al.GetStatusByUserUid(aepr, userUid)
}

// AccountLockoutUnlockByUserUidHandler - Complete handler in module (FIXED/HOW)
func (al *DXMAccountLockout) AccountLockoutUnlockByUserUidHandler(aepr *api.DXAPIEndPointRequest) error {
	_, userUid, err := aepr.GetParameterValueAsString("user_uid")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PARAMETER_USER_UID_REQUIRED", "Parameter user_uid is required")
	}

	_, reason, err := aepr.GetParameterValueAsString("reason")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PARAMETER_REASON_REQUIRED", "Parameter reason is required")
	}

	return al.UnlockByUserUid(aepr, userUid, reason)
}

// AccountLockoutHistoryByUserUidHandler - Complete handler in module (FIXED/HOW)
func (al *DXMAccountLockout) AccountLockoutHistoryByUserUidHandler(aepr *api.DXAPIEndPointRequest) error {
	_, userUid, err := aepr.GetParameterValueAsString("user_uid")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PARAMETER_USER_UID_REQUIRED", "Parameter user_uid is required")
	}

	_, limit, err := aepr.GetParameterValueAsInt64("limit")
	if err != nil {
		limit = 0 // Will use module's default
	}

	return al.GetHistoryByUserUid(aepr, userUid, limit)
}
