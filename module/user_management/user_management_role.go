package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
)

func (um *DxmUserManagement) RoleList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.List(aepr)
}

func (um *DxmUserManagement) RoleCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.Role.DoCreate(aepr, map[string]any{
		`nameid`:      aepr.ParameterValues[`nameid`].Value.(string),
		`name`:        aepr.ParameterValues[`name`].Value.(string),
		`description`: aepr.ParameterValues[`description`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) RoleRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.Read(aepr)
}

func (um *DxmUserManagement) RoleReadByNameId(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.ReadByNameId(aepr)
}

func (um *DxmUserManagement) RoleEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.Edit(aepr)
}

func (um *DxmUserManagement) RoleDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.SoftDelete(aepr)
}
