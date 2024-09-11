package user_management

import "github.com/donnyhardyanto/dxlib/api"

func (um *DxmUserManagement) RolePrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.List(aepr)
}

func (um *DxmUserManagement) RolePrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.RolePrivilege.DoCreate(aepr, map[string]any{
		`role_id`:      aepr.ParameterValues[`role_id`].Value.(string),
		`privilege_id`: aepr.ParameterValues[`privilege_id`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) RolePrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.SoftDelete(aepr)
}
