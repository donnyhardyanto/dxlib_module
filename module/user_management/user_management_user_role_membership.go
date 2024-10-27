package user_management

import (
	"database/sql"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/utils"
	"net/http"
)

func (um *DxmUserManagement) UserRoleMembershipList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.UserRoleMembership.List(aepr)
}

func (um *DxmUserManagement) UserRoleMembershipCreate(aepr *api.DXAPIEndPointRequest) (err error) {

	dbTaskDispatcher := database.Manager.Databases[um.DatabaseNameId]
	dtx, err := dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(&aepr.Log, err)

	var userRoleMembershipId int64
	userRoleMembershipId, err = um.UserRoleMembership.TxInsert(dtx, map[string]any{
		`user_id`: aepr.ParameterValues[`user_id`].Value.(int64),
		`role_id`: aepr.ParameterValues[`role_id`].Value.(int64),
	})
	if err != nil {
		return err
	}

	var userRoleMembership utils.JSON
	_, userRoleMembership, err = um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
	if err != nil {
		return err
	}

	if um.OnUserRoleMembershipAfterCreate != nil {
		err = um.OnUserRoleMembershipAfterCreate(aepr, dtx, userRoleMembership)
		if err != nil {
			return err
		}

	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
	})
	return err
}

func (um *DxmUserManagement) UserRoleMembershipSoftDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userRoleMembershipId, err := aepr.GetParameterValueAsInt64(`id`)
	if err != nil {
		return err
	}

	dbTaskDispatcher := database.Manager.Databases[um.DatabaseNameId]
	dtx, err := dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(&aepr.Log, err)

	var userRoleMembership utils.JSON
	_, userRoleMembership, err = um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
	if err != nil {
		return err
	}

	if um.OnUserRoleMembershipBeforeSoftDelete != nil {
		err = um.OnUserRoleMembershipBeforeSoftDelete(aepr, dtx, userRoleMembership)
	}

	_, err = um.UserRoleMembership.TxSoftDelete(dtx, utils.JSON{
		um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
	})

	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
	})
	return nil
}
func (um *DxmUserManagement) UserRoleMembershipHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {

	_, userRoleMembershipId, err := aepr.GetParameterValueAsInt64(`id`)
	if err != nil {
		return err
	}

	dbTaskDispatcher := database.Manager.Databases[um.DatabaseNameId]
	dtx, err := dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(&aepr.Log, err)

	var userRoleMembership utils.JSON
	_, userRoleMembership, err = um.UserRoleMembership.TxShouldGetById(dtx, userRoleMembershipId)
	if err != nil {
		return err
	}

	if um.OnUserRoleMembershipBeforeHardDelete != nil {
		err = um.OnUserRoleMembershipBeforeHardDelete(aepr, dtx, userRoleMembership)
	}

	_, err = um.UserRoleMembership.TxHardDelete(dtx, utils.JSON{
		um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
	})

	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		um.UserRoleMembership.FieldNameForRowId: userRoleMembershipId,
	})
	return nil
}
