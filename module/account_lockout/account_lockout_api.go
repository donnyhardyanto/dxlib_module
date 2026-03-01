package account_lockout

import (
	"net/http"
	"strconv"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/module/user_management"
)

// This file contains ALL API handler logic (FIXED/HOW) separated from API configuration (FLUID/WHAT)
// Following Low IQ Tax Principle: Logic goes in module, API definitions stay in cmd/service files
// The handlers here match the API signature and can be referenced directly in endpoint definitions

// GetStatusByUserUid gets lockout status for a user by UID
// This is the FIXED/HOW logic separated from API configuration
func (al *DXMAccountLockout) GetStatusByUserUid(aepr *api.DXAPIEndPointRequest, userUid string) error {
	// Get user by UID
	_, user, err := user_management.ModuleUserManagement.User.GetByUid(aepr.Context, &aepr.Log, userUid)
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER", err)
		return err
	}
	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, "USER_NOT_FOUND", "User with uid %s not found", userUid)
	}

	userId, err := utils.GetInt64FromKV(user, "id")
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER_ID", err)
		return err
	}

	// Check if module is enabled
	if !al.Config.Enabled {
		aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
			"is_enabled": false,
			"is_locked":  false,
			"message":    "Account lockout module is disabled",
		})
		return nil
	}

	// Get lockout status
	status, err := al.GetLockoutStatus(userId)
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_LOCKOUT_STATUS", err)
		return err
	}

	status["is_enabled"] = true
	status["user_uid"] = userUid
	status["user_id"] = userId

	aepr.WriteResponseAsJSON(http.StatusOK, nil, status)
	return nil
}

// UnlockByUserUid unlocks a user account by UID
// This is the FIXED/HOW logic separated from API configuration
func (al *DXMAccountLockout) UnlockByUserUid(aepr *api.DXAPIEndPointRequest, userUid string, reason string) error {
	// Get user by UID
	_, user, err := user_management.ModuleUserManagement.User.GetByUid(aepr.Context, &aepr.Log, userUid)
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER", err)
		return err
	}
	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, "USER_NOT_FOUND", "User with uid %s not found", userUid)
	}

	userId, err := utils.GetInt64FromKV(user, "id")
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER_ID", err)
		return err
	}

	userLoginId, err := utils.GetStringFromKV(user, "loginid")
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER_LOGINID", err)
		return err
	}

	// Check if module is enabled
	if !al.Config.Enabled {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "MODULE_DISABLED", "Account lockout module is disabled")
	}

	// Get admin user info from session
	adminUserUid := aepr.CurrentUser.Uid
	adminUserId := int64(0)
	if aepr.CurrentUser.Id != "" {
		// Convert string ID to int64 if possible
		if id, parseErr := strconv.ParseInt(aepr.CurrentUser.Id, 10, 64); parseErr == nil {
			adminUserId = id
		}
	}

	// Unlock account
	err = al.UnlockAccount(
		aepr.Context,
		userId,
		userUid,
		userLoginId,
		adminUserId,
		adminUserUid,
		reason,
	)
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_UNLOCK_ACCOUNT", err)
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"message":         "Account unlocked successfully",
		"user_uid":        userUid,
		"user_id":         userId,
		"unlocked_by_uid": adminUserUid,
		"reason":          reason,
	})
	return nil
}

// GetHistoryByUserUid gets lockout history for a user by UID
// This is the FIXED/HOW logic separated from API configuration
func (al *DXMAccountLockout) GetHistoryByUserUid(aepr *api.DXAPIEndPointRequest, userUid string, limit int64) error {
	// Validate and set default limit
	if limit == 0 {
		limit = 50 // default
	}
	if limit > 500 {
		limit = 500 // max
	}

	// Get user by UID
	_, user, err := user_management.ModuleUserManagement.User.GetByUid(aepr.Context, &aepr.Log, userUid)
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER", err)
		return err
	}
	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, "USER_NOT_FOUND", "User with uid %s not found", userUid)
	}

	userId, err := utils.GetInt64FromKV(user, "id")
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_USER_ID", err)
		return err
	}

	// Check if module is enabled
	if !al.Config.Enabled {
		aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
			"is_enabled": false,
			"events":     []utils.JSON{},
			"message":    "Account lockout module is disabled",
		})
		return nil
	}

	// Get lockout history
	events, err := al.GetLockoutHistory(aepr.Context, userId, int(limit))
	if err != nil {
		aepr.WriteResponseAndLogAsError(http.StatusInternalServerError, "FAILED_TO_GET_LOCKOUT_HISTORY", err)
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"is_enabled": true,
		"user_uid":   userUid,
		"user_id":    userId,
		"events":     events,
		"count":      len(events),
	})
	return nil
}
