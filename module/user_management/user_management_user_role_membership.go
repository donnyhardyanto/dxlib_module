package user_management

import "github.com/donnyhardyanto/dxlib/api"

func (um *DxmUserManagement) UserRoleMembershipList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.UserRoleMembership.List(aepr)
}

func (um *DxmUserManagement) UserRoleMembershipCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.UserRoleMembership.DoCreate(aepr, map[string]any{
		`user_id`: aepr.ParameterValues[`user_id`].Value.(string),
		`role_id`: aepr.ParameterValues[`role_id`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) UserRoleMembershipDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.UserRoleMembership.SoftDelete(aepr)
}
