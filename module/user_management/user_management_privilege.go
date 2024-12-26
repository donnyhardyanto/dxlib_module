package user_management

import (
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	"github.com/donnyhardyanto/dxlib/database/protected/export"
	"net/http"
	"strings"
	"time"
)

func (um *DxmUserManagement) PrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.RequestPagingList(aepr)
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
	return um.Privilege.RequestRead(aepr)
}

func (um *DxmUserManagement) PrivilegeEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.RequestEdit(aepr)
}

func (um *DxmUserManagement) PrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Privilege.RequestSoftDelete(aepr)
}

func (um *DxmUserManagement) PrivilegeListDownload(aepr *api.DXAPIEndPointRequest) (err error) {
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

	_, format, err := aepr.GetParameterValueAsString("format")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, `FORMAT_PARAMETER_ERROR:%s`, err.Error())
	}

	format = strings.ToLower(format)

	t := um.Privilege

	isDeletedIncluded := false
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

	if t.Database == nil {
		t.Database = database.Manager.Databases[t.DatabaseNameId]
	}

	if !t.Database.Connected {
		err := t.Database.Connect()
		if err != nil {
			aepr.Log.Errorf("error At reconnect db At table %s list (%s) ", t.NameId, err.Error())
			return err
		}
	}

	rowsInfo, list, err := db.NamedQueryList(t.Database.Connection, t.FieldTypeMapping, "*", t.ListViewNameId,
		filterWhere, "", filterOrderBy, filterKeyValues)

	if err != nil {
		return err
	}

	// Set export options
	opts := export.ExportOptions{
		Format:     export.ExportFormat(format),
		SheetName:  "Sheet1",
		DateFormat: "2006-01-02 15:04:05",
	}

	// Get file as stream
	data, contentType, err := export.ExportToStream(rowsInfo, list, opts)
	if err != nil {
		return err
	}

	// Set response headers
	filename := fmt.Sprintf("export_%s_%s.%s", t.NameId, time.Now().Format("20060102_150405"), format)

	responseWriter := *aepr.GetResponseWriter()
	responseWriter.Header().Set("Content-Type", contentType)
	responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	responseWriter.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	responseWriter.WriteHeader(http.StatusOK)
	aepr.ResponseStatusCode = http.StatusOK

	_, err = responseWriter.Write(data)
	if err != nil {
		return err
	}

	aepr.ResponseHeaderSent = true
	aepr.ResponseBodySent = true

	return nil
}
