package user_management

import "github.com/donnyhardyanto/dxlib/api"

func (um *DxmUserManagement) PrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.List(aepr)
}

func (um *DxmUserManagement) PrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.Privilege.DoCreate(aepr, map[string]any{
		`nameid`:      aepr.ParameterValues[`nameid`].Value.(string),
		`name`:        aepr.ParameterValues[`name`].Value.(string),
		`description`: aepr.ParameterValues[`description`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) PrivilegeRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.Read(aepr)
}

func (um *DxmUserManagement) PrivilegeEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.Edit(aepr)
}

func (um *DxmUserManagement) PrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.SoftDelete(aepr)
}
