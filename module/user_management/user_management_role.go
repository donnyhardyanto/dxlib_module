package user_management

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/utils"
	utilsJson "github.com/donnyhardyanto/dxlib/utils/json"
)

func (um *DxmUserManagement) RoleList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestPagingList(aepr)
}

func (um *DxmUserManagement) RoleCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	isOrganizationTypes, organizationTypes, err := aepr.GetParameterValueAsStrings("organization_types")
	if err != nil {
		return err
	}

	p := utils.JSON{
		"nameid":      aepr.ParameterValues["nameid"].Value.(string),
		"name":        aepr.ParameterValues["name"].Value.(string),
		"description": aepr.ParameterValues["description"].Value.(string),
	}

	if isOrganizationTypes {
		p["organization_types"] = organizationTypes
	}

	t := um.Role
	t.SetInsertAuditFields(aepr, p)

	err = t.EnsureDatabase()
	if err != nil {
		return err
	}

	var newUid string

	txErr := t.Database.Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		err := t.TxCheckValidationUniqueFieldNameGroupsForInsert(dtx, p)
		if err != nil {
			return err
		}
		_, returningValues, err := t.DXRawTable.TxInsert(dtx, p, []string{t.FieldNameForRowUid})
		if err != nil {
			return err
		}
		if uid, ok := returningValues[t.FieldNameForRowUid].(string); ok {
			newUid = uid
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utilsJson.Encapsulate(t.ResponseEnvelopeObjectName, utils.JSON{
		t.FieldNameForRowUid: newUid,
	}))
	return nil
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
		return err
	}

	_, newFieldValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return err
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
		return err
	}
	return nil
}

func (um *DxmUserManagement) RoleEditByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.Role
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}
	_, row, err := t.ShouldGetByUid(&aepr.Log, uid)
	if err != nil {
		return err
	}
	id := row[t.FieldNameForRowId].(int64)

	_, newFieldValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return err
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
		return err
	}
	return nil
}

func (um *DxmUserManagement) RoleDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestSoftDelete(aepr)
}

func (um *DxmUserManagement) RoleDeleteByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Role.RequestSoftDeleteByUid(aepr)
}
