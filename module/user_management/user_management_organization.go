package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) OrganizationList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.List(aepr)
}

func (um *DxmUserManagement) OrganizationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	o := utils.JSON{
		`name`:    aepr.ParameterValues[`name`].Value.(string),
		`type`:    aepr.ParameterValues[`type`].Value.(string),
		`address`: aepr.ParameterValues[`address`].Value.(string),
		`status`:  aepr.ParameterValues[`status`].Value.(string),
	}

	_, _, err = aepr.AssignParameterNullableInt64(&o, `parent_id`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `attribute1`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `attribute2`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `auth_source1`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `auth_source2`)
	if err != nil {
		return err
	}

	_, err = um.Organization.DoCreate(aepr, o)
	return err
}

func (um *DxmUserManagement) OrganizationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.Read(aepr)
}

func (um *DxmUserManagement) OrganizationReadByName(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.ReadByNameId(aepr)
}

func (um *DxmUserManagement) OrganizationEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.Edit(aepr)
}

func (um *DxmUserManagement) OrganizationDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.SoftDelete(aepr)
}
