package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) RolePrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestPagingList(aepr)
}

func (um *DxmUserManagement) RolePrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.RolePrivilege.DoCreate(aepr, map[string]any{
		`role_id`:      aepr.ParameterValues[`role_id`].Value.(string),
		`privilege_id`: aepr.ParameterValues[`privilege_id`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) RolePrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestSoftDelete(aepr)
}

func (um *DxmUserManagement) RolePrivilegeTxInsert(dtx *database.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64, err error) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		return 0, err
	}
	privilegeId := privilege[`id`].(int64)
	id, err = um.RolePrivilege.TxInsert(dtx, utils.JSON{
		`role_id`:      roleId,
		`privilege_id`: privilegeId,
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (um *DxmUserManagement) RolePrivilegeTxMustInsert(dtx *database.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		dtx.Log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.Privilege.TxShouldGetByNameId`, err)
		return 0
	}
	privilegeId := privilege[`id`].(int64)
	id, err = um.RolePrivilege.TxInsert(dtx, utils.JSON{
		`role_id`:      roleId,
		`privilege_id`: privilegeId,
	})
	if err != nil {
		dtx.Log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.TxInsert`, err)
		return 0
	}
	return id
}
