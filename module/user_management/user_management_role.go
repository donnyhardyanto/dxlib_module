package user_management

import (
	"encoding/json"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/pkg/errors"
)

func (um *DxmUserManagement) RoleList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestPagingList(aepr)
}

func (um *DxmUserManagement) RoleCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	isOrganizationTypes, organizationTypes, err := aepr.GetParameterValueAsStrings("organization_types")
	if err != nil {
		return errors.Wrap(err, "error occurred")
	}

	p := utils.JSON{
		"nameid":      aepr.ParameterValues["nameid"].Value.(string),
		"name":        aepr.ParameterValues["name"].Value.(string),
		"description": aepr.ParameterValues["description"].Value.(string),
	}

	if isOrganizationTypes {
		p["organization_types"] = organizationTypes
	}

	_, err = um.Role.DoCreate(aepr, p)
	return err
}

func (um *DxmUserManagement) RoleRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestRead(aepr)
}

func (um *DxmUserManagement) RoleReadByNameId(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestReadByNameId(aepr)
}

func (um *DxmUserManagement) RoleEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.Role
	_, id, err := aepr.GetParameterValueAsInt64(t.FieldNameForRowId)
	if err != nil {
		return errors.Wrap(err, "error occured")
	}

	_, newFieldValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return errors.Wrap(err, "error occured")
	}

	p := utils.JSON{}

	nameid, ok := newFieldValues["nameid"].(string)
	if ok {
		p["nameid"] = nameid

	}

	name, ok := newFieldValues["name"].(string)
	if ok {
		p["name"] = name

	}

	description, ok := newFieldValues["description"].(string)
	if ok {
		p["description"] = description

	}

	organizationTypes, ok := newFieldValues["organization_types"].([]string)
	if ok {
		jsonBytes, err := json.Marshal(organizationTypes)
		if err != nil {
			return err
		}
		jsonString := string(jsonBytes)
		p["organization_types"] = jsonString
	}

	err = t.DoEdit(aepr, id, p)
	if err != nil {
		return errors.Wrap(err, "error occured")
	}
	return nil
}

func (um *DxmUserManagement) RoleDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestSoftDelete(aepr)
}
