package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) OrganizationList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestPagingList(aepr)
}

func (um *DxmUserManagement) OrganizationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	o := utils.JSON{
		`parent_id`: aepr.ParameterValues[`parent_id`].Value,
		`code`:      aepr.ParameterValues[`code`].Value.(string),
		`name`:      aepr.ParameterValues[`name`].Value.(string),
		`type`:      aepr.ParameterValues[`type`].Value.(string),
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `address`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `npwp`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `email`)
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, `phonenumber`)
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

	_, _, err = aepr.AssignParameterNullableString(&o, `utag`)
	if err != nil {
		return err
	}

	_, err = um.Organization.DoCreate(aepr, o)
	return err
}

func (um *DxmUserManagement) OrganizationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestRead(aepr)
}

func (um *DxmUserManagement) OrganizationReadByName(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestReadByNameId(aepr)
}

func (um *DxmUserManagement) OrganizationEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestEdit(aepr)
}

func (um *DxmUserManagement) OrganizationDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestSoftDelete(aepr)
}
