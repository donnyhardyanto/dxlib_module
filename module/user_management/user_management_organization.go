package user_management

import (
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) OrganizationList(aepr *api.DXAPIEndPointRequest) (err error) {
	isExistFilterWhere, filterWhere, err := aepr.GetParameterValueAsString("filter_where")
	if err != nil {
		return err
	}
	if !isExistFilterWhere {
		filterWhere = ""
	}
	isExistFilterOrderBy, filterOrderBy, err := aepr.GetParameterValueAsString("filter_order_by")
	if err != nil {
		return err
	}
	if !isExistFilterOrderBy {
		filterOrderBy = ""
	}

	isExistFilterKeyValues, filterKeyValues, err := aepr.GetParameterValueAsJSON("filter_key_values")
	if err != nil {
		return err
	}
	if !isExistFilterKeyValues {
		filterKeyValues = nil
	}

	t := um.Organization

	_, isDeletedIncluded, err := aepr.GetParameterValueAsBool("is_deleted", false)
	if err != nil {
		return err
	}

	if !isDeletedIncluded {
		if filterWhere != "" {
			filterWhere = fmt.Sprintf("(%s) and ", filterWhere)
		}

		switch t.Database.DatabaseType.String() {
		case "sqlserver":
			filterWhere = filterWhere + "(is_deleted=0)"
		case "postgres":
			filterWhere = filterWhere + "(is_deleted=false)"
		default:
			filterWhere = filterWhere + "(is_deleted=0)"
		}
	}

	return t.DoRequestPagingList(aepr, filterWhere, filterOrderBy, filterKeyValues, func(listRow utils.JSON) (utils.JSON, error) {
		organizationId := listRow[`id`].(int64)
		_, organizationRoles, err := um.OrganizationRoles.Select(&aepr.Log, nil, utils.JSON{`organization_id`: organizationId}, map[string]string{"id": "asc"}, nil)
		if err != nil {
			return listRow, err
		}
		listRow["organization_roles"] = organizationRoles
		return listRow, nil
	})

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
