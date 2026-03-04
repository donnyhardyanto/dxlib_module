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

// GetUserPrivilegeVersion reads privilege_version:{userId} from SessionRedis. Returns 0 if key missing or on error.
func (um *DxmUserManagement) GetUserPrivilegeVersion(ctx context.Context, userId int64) int64 {
	key := privilegeVersionKey(userId)
	val, err := um.SessionRedis.Connection.Get(ctx, key).Int64()
	if err != nil {
		return 0
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
