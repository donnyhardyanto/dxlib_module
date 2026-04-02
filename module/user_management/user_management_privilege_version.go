package user_management

import (
	"context"
	"fmt"
	"log/slog"

	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

func privilegeVersionKey(userId int64) string {
	return fmt.Sprintf("privilege_version:%d", userId)
}

// GetOrInitUserPrivilegeVersion reads privilege_version:{userId} from SessionRedis.
// If the key does not exist, it initializes it to 1 and returns 1.
// This prevents the 0==0 blind spot where both a stale session and a missing key default to 0,
// causing the middleware to skip session regeneration after privilege changes.
func (um *DxmUserManagement) GetOrInitUserPrivilegeVersion(ctx context.Context, userId int64) int64 {
	key := privilegeVersionKey(userId)
	val, err := um.SessionRedis.Connection.Get(ctx, key).Int64()
	if err != nil {
		// Key missing or error — initialize to 1 so any session with privilege_version=0 will mismatch and refresh
		initErr := um.SessionRedis.Connection.SetNX(ctx, key, 1, 0).Err()
		if initErr != nil {
			slog.Warn("Failed to initialize privilege version for user", "user_id", userId, "error", initErr)
		}
		// Re-read in case another process set it between our Get and SetNX
		val, err = um.SessionRedis.Connection.Get(ctx, key).Int64()
		if err != nil {
			return 1
		}
		return val
	}
	return val
}

// IncrementUserPrivilegeVersion increments privilege_version:{userId} in SessionRedis. Best-effort: logs error, does not fail.
func (um *DxmUserManagement) IncrementUserPrivilegeVersion(ctx context.Context, userId int64) {
	key := privilegeVersionKey(userId)
	err := um.SessionRedis.Connection.Incr(ctx, key).Err()
	if err != nil {
		slog.Warn("Failed to increment privilege version for user", "user_id", userId, "error", err)
	}
}

// IncrementPrivilegeVersionForRole queries active UserRoleMembership for roleId, then increments privilege version for each affected user.
func (um *DxmUserManagement) IncrementPrivilegeVersionForRole(ctx context.Context, l *dxlibLog.DXLog, roleId int64) {
	_, memberships, err := um.UserRoleMembership.Select(ctx, l, nil, utils.JSON{
		"role_id": roleId,
	}, nil, nil, nil, nil)
	if err != nil {
		l.Warnf("Failed to query role memberships for role %d: %v", roleId, err)
		return
	}
	for _, m := range memberships {
		userId, err := utils.GetInt64FromKV(m, "user_id")
		if err != nil {
			l.Warnf("Failed to get user_id from role membership: %v", err)
			continue
		}
		um.IncrementUserPrivilegeVersion(ctx, userId)
	}
}

// IncrementPrivilegeVersionForOrganization queries active UserOrganizationMembership for organizationId,
// then increments privilege version for each affected user. This forces session refresh for all members
// when organization status changes (e.g., activate, suspend, delete).
func (um *DxmUserManagement) IncrementPrivilegeVersionForOrganization(ctx context.Context, l *dxlibLog.DXLog, organizationId int64) {
	_, memberships, err := um.UserOrganizationMembership.Select(ctx, l, nil, utils.JSON{
		"organization_id": organizationId,
	}, nil, nil, nil, nil)
	if err != nil {
		l.Warnf("Failed to query organization memberships for organization %d: %v", organizationId, err)
		return
	}
	for _, m := range memberships {
		userId, err := utils.GetInt64FromKV(m, "user_id")
		if err != nil {
			l.Warnf("Failed to get user_id from organization membership: %v", err)
			continue
		}
		um.IncrementUserPrivilegeVersion(ctx, userId)
	}
}
