package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
)

func (um *DxmUserManagement) PrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.Privilege.DoCreate(aepr, map[string]any{
		"nameid":      aepr.ParameterValues["nameid"].Value.(string),
		"name":        aepr.ParameterValues["name"].Value.(string),
		"description": aepr.ParameterValues["description"].Value.(string),
	})
	return err
}
