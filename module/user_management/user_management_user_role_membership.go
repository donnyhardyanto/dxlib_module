package user_management

import (
	"database/sql"
	"net/http"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) UserRoleMembershipCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")
	if err != nil {
		return err
	}
	_, roleId, err := aepr.GetParameterValueAsInt64("role_id")
	if err != nil {
		return err
	}
	_, organizationId, err := aepr.GetParameterValueAsInt64("organization_id")
	if err != nil {
		return err
	}

	_, _, err = um.OrganizationRoles.ShouldSelectOne(aepr.Context, &aepr.Log, nil, utils.JSON{
		"organization_id": organizationId,
		"role_id":         roleId,
	}, nil, nil)
	if err != nil {
		return err
	}

	var userRoleMembershipId int64
	var userRoleMembershipUid string
	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		var err2 error
		userRoleMembershipId, err2 = um.UserRoleMembership.TxInsertReturningId(dtx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
			"role_id":         roleId,
		})
		if err2 != nil {
			return err2
		}

		var userRoleMembership utils.JSON
		_, userRoleMembership, err2 = um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
		if err2 != nil {
			return err2
		}
		if uid, ok := userRoleMembership["uid"].(string); ok {
			userRoleMembershipUid = uid
		}

		if um.OnUserRoleMembershipAfterCreate != nil {
			err2 = um.OnUserRoleMembershipAfterCreate(aepr, dtx, userRoleMembership, 0)
			if err2 != nil {
				return err2
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"uid": userRoleMembershipUid,
	}})
	return nil
}

func (um *DxmUserManagement) UserRoleMembershipSoftDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userRoleMembershipId, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	var userRoleMembershipUid string
	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		var userRoleMembership utils.JSON
		_, userRoleMembership, err2 := um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
		if err2 != nil {
			return err2
		}
		if uid, ok := userRoleMembership["uid"].(string); ok {
			userRoleMembershipUid = uid
		}

		if um.OnUserRoleMembershipBeforeSoftDelete != nil {
			err2 = um.OnUserRoleMembershipBeforeSoftDelete(aepr, dtx, userRoleMembership)
			if err2 != nil {
				return err2
			}
		}

		_, err2 = um.UserRoleMembership.TxSoftDelete(dtx, utils.JSON{
			um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
		})
		return err2
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"uid": userRoleMembershipUid,
	}})
	return nil
}

func (um *DxmUserManagement) UserRoleMembershipHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userRoleMembershipId, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	var userRoleMembershipUid string
	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		var userRoleMembership utils.JSON
		_, userRoleMembership, err2 := um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
		if err2 != nil {
			return err2
		}
		if uid, ok := userRoleMembership["uid"].(string); ok {
			userRoleMembershipUid = uid
		}

		if um.OnUserRoleMembershipBeforeHardDelete != nil {
			err2 = um.OnUserRoleMembershipBeforeHardDelete(aepr, dtx, userRoleMembership)
			if err2 != nil {
				return err2
			}
		}

		_, err2 = um.UserRoleMembership.TxHardDelete(dtx, utils.JSON{
			um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
		})
		return err2
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"uid": userRoleMembershipUid,
	}})
	return nil
}
