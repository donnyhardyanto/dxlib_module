package user_management

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/tables"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/tealeg/xlsx"
)

func (um *DxmUserManagement) OrganizationCreateBulk(aepr *api.DXAPIEndPointRequest) (err error) {
	// Get the request body stream
	bs := aepr.Request.Body
	if bs == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "FAILED_TO_GET_BODY_STREAM:%s", "OrganizationCreateBulk")
	}
	defer bs.Close()

	// Read the entire request body into a buffer
	var buf bytes.Buffer
	_, err = io.Copy(&buf, bs)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "FAILED_TO_READ_REQUEST_BODY:%s=%v", "OrganizationCreateBulk", err.Error())
	}

	// Determine the file type and parse accordingly
	contentType := utils.GetStringFromMapStringStringDefault(aepr.EffectiveRequestHeader, "Content-Type", "")
	if strings.Contains(contentType, "csv") {
		err = um.parseAndCreateOrganizationsFromCSV(&buf, aepr)
	} else if strings.Contains(contentType, "excel") || strings.Contains(contentType, "spreadsheetml") {
		err = um.parseAndCreateOrganizationsFromXLSX(&buf, aepr)
	} else {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnsupportedMediaType, "UNSUPPORTED_FILE_TYPE:%s", contentType)
	}

	if err != nil {
		return errors.Wrap(err, "error occurred")
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (um *DxmUserManagement) parseAndCreateOrganizationsFromCSV(buf *bytes.Buffer, aepr *api.DXAPIEndPointRequest) error {
	// Create a new reader with comma as delimiter
	reader := csv.NewReader(buf)
	reader.Comma = ';'          // Set comma as delimiter
	reader.LazyQuotes = true    // Handle quotes more flexibly
	reader.FieldsPerRecord = -1 // Allow variable number of fields

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity,
			"FAILED_TO_READ_CSV_HEADERS: %s", err.Error())
	}

	// Clean headers - trim spaces and empty fields
	cleanHeaders := make([]string, 0)
	for _, h := range headers {
		h = strings.TrimSpace(h)
		if h != "" {
			cleanHeaders = append(cleanHeaders, h)
		}
	}

	// Process each row
	lineNum := 1 // Keep track of line numbers for error reporting
	for {
		lineNum++
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "",
				"FAILED_TO_PARSE_CSV_LINE_%d: %s", lineNum, err.Error())
		}

		// Create organization data map
		organizationData := make(map[string]interface{})
		for i, value := range record {
			if i >= len(cleanHeaders) {
				break
			}
			// Clean and validate the value
			value = strings.TrimSpace(value)
			if value != "" {
				organizationData[cleanHeaders[i]] = value
			}
		}

		// Skip empty rows
		if len(organizationData) == 0 {
			continue
		}

		// Create organization
		_, err = um.doOrganizationCreate(&aepr.Log, organizationData)
		if err != nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "",
				"FAILED_TO_CREATE_ORGANIZATION_LINE_%d: %s", lineNum, err.Error())
		}
	}

	return nil
}

func (um *DxmUserManagement) parseAndCreateOrganizationsFromXLSX(buf *bytes.Buffer, aepr *api.DXAPIEndPointRequest) error {
	xlFile, err := xlsx.OpenBinary(buf.Bytes())
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", "FAILED_TO_PARSE_XLSX: %s", err.Error())
	}

	for _, sheet := range xlFile.Sheets {
		if len(sheet.Rows) < 2 {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", "XLSX_FILE_MUST_HAVE_HEADER_AND_DATA")
		}

		// Validate and extract headers
		headers := make([]string, 0, len(sheet.Rows[0].Cells))
		for _, cell := range sheet.Rows[0].Cells {
			header := strings.TrimSpace(cell.String())
			if header == "" {
				return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", "EMPTY_HEADER_NOT_ALLOWED")
			}
			headers = append(headers, header)
		}

		// Process data rows
		for rowIdx, row := range sheet.Rows[1:] {
			organizationData := make(map[string]interface{}, len(headers))

			if len(row.Cells) == 0 {
				continue // Skip empty rows
			}

			// Map cell values to headers with type conversion
			for i, cell := range row.Cells {
				if i >= len(headers) {
					break
				}

				value := strings.TrimSpace(cell.String())
				if value == "" {
					continue // Skip empty values instead of adding them to organizationData
				}

				// Try to convert numeric values for specific columns
				if um.isNumericOrganizationColumn(headers[i]) {
					if numVal, err := cell.Float(); err == nil {
						organizationData[headers[i]] = numVal
					} else {
						return aepr.WriteResponseAndNewErrorf(
							http.StatusUnprocessableEntity, "",
							"INVALID_NUMERIC_VALUE_AT_ROW_%d_COLUMN_%s: %q",
							rowIdx+2,
							headers[i],
							value,
						)
					}
				} else {
					organizationData[headers[i]] = value
				}
			}

			if len(organizationData) == 0 {
				continue
			}

			if _, err = um.doOrganizationCreate(&aepr.Log, organizationData); err != nil {
				// Check for specific PostgreSQL errors
				if strings.Contains(err.Error(), "invalid input syntax for type double precision") {
					return aepr.WriteResponseAndNewErrorf(
						http.StatusUnprocessableEntity, "",
						"INVALID_NUMERIC_VALUE_AT_ROW_%d: Please ensure all numeric fields contain valid numbers",
						rowIdx+2,
					)
				}
				return aepr.WriteResponseAndNewErrorf(
					http.StatusUnprocessableEntity, "",
					"FAILED_TO_CREATE_ORGANIZATION_AT_ROW_%d: %s",
					rowIdx+2,
					err.Error(),
				)
			}
		}
	}

	return nil
}

// Helper function to identify numeric columns for organizations
func (um *DxmUserManagement) isNumericOrganizationColumn(header string) bool {
	// Add your numeric column names here for organizations
	numericColumns := map[string]bool{
		"parent_id": true,
		// Add other numeric column names as needed
	}

	header = strings.ToLower(header)
	return numericColumns[header]
}

// Helper function to create organization with proper validation
func (um *DxmUserManagement) doOrganizationCreate(log *dxlibLog.DXLog, organizationData map[string]interface{}) (int64, error) {
	// Validate required fields
	code, ok := organizationData["code"].(string)
	if !ok || code == "" {
		return 0, errors.Errorf("organization code is required")
	}

	name, ok := organizationData["name"].(string)
	if !ok || name == "" {
		return 0, errors.Errorf("organization name is required")
	}

	orgType, ok := organizationData["type"].(string)
	if !ok || orgType == "" {
		return 0, errors.Errorf("organization type is required")
	}

	// Build organization object
	o := utils.JSON{
		"code": code,
		"name": name,
		"type": orgType,
	}

	// Handle optional fields
	if parentId, ok := organizationData["parent_id"]; ok && parentId != nil {
		o["parent_id"] = parentId
	}

	if address, ok := organizationData["address"].(string); ok && address != "" {
		o["address"] = address
	}

	if npwp, ok := organizationData["npwp"].(string); ok && npwp != "" {
		o["npwp"] = npwp
	}

	if email, ok := organizationData["email"].(string); ok && email != "" {
		o["email"] = email
	}

	if phonenumber, ok := organizationData["phonenumber"].(string); ok && phonenumber != "" {
		o["phonenumber"] = phonenumber
	}

	if attribute1, ok := organizationData["attribute1"].(string); ok && attribute1 != "" {
		o["attribute1"] = attribute1
	}

	if authSource1, ok := organizationData["auth_source1"].(string); ok && authSource1 != "" {
		o["auth_source1"] = authSource1
	}

	if attribute2, ok := organizationData["attribute2"].(string); ok && attribute2 != "" {
		o["attribute2"] = attribute2
	}

	if authSource2, ok := organizationData["auth_source2"].(string); ok && authSource2 != "" {
		o["auth_source2"] = authSource2
	}

	if utag, ok := organizationData["utag"].(string); ok && utag != "" {
		o["utag"] = utag
	}

	// Create the organization using the existing method - create a dummy aepr for this
	id, err := um.Organization.InsertReturningId(log, o)
	return id, err
}

func (um *DxmUserManagement) OrganizationSearchPaging(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.Organization

	userOrganizationId, ok := aepr.LocalData["organization_id"].(int64)
	if !ok {
		return errors.New("USER_HAS_NO_ORGANIZATION_ID_OR_NOT_INT64")
	}

	_, searchText, err := aepr.GetParameterValueAsString("search_text")
	if err != nil {
		return err
	}

	_, filterKeyValues, err := aepr.GetParameterValueAsJSON("filter_key_values")
	if err != nil {
		return err
	}

	_, orderByArray, err := aepr.GetParameterValueAsArrayOfAny("order_by")
	if err != nil {
		return err
	}
	orderByStr := tables.BuildOrderByString(orderByArray)

	_, rowPerPage, err := aepr.GetParameterValueAsInt64("row_per_page")
	if err != nil {
		return err
	}

	_, pageIndex, err := aepr.GetParameterValueAsInt64("page_index")
	if err != nil {
		return err
	}

	_, isDeletedIncluded, err := aepr.GetParameterValueAsBool("is_include_deleted", false)
	if err != nil {
		return err
	}

	if err := t.EnsureDatabase(); err != nil {
		return err
	}

	qb := tables.NewQueryBuilder(t.Database.DatabaseType, t)
	if !isDeletedIncluded {
		qb.NotDeleted()
	}
	if searchText != "" {
		qb.SearchLike(searchText, t.SearchTextFieldNames...)
	}
	if filterKeyValues != nil {
		for k, v := range filterKeyValues {
			qb.EqOrIn(k, v)
		}
	}
	// Organization scope: if not root org (id=1), only show own org and children
	if userOrganizationId != 1 {
		qb.And(fmt.Sprintf("(id = %d OR parent_id = %d)", userOrganizationId, userOrganizationId))
	}

	result, err := t.PagingWithBuilder(&aepr.Log, rowPerPage, pageIndex, qb, orderByStr)
	if err != nil {
		return err
	}

	for i, row := range result.Rows {
		organizationId, err := utils.GetInt64FromKV(row, "id")
		if err != nil {
			return err
		}
		_, organizationRoles, err := um.OrganizationRoles.Select(&aepr.Log, nil, utils.JSON{
			"organization_id": organizationId,
		}, nil, nil, nil, nil)
		if err != nil {
			return err
		}
		result.Rows[i]["organization_roles"] = organizationRoles
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, result.ToResponseJSON())
	return nil
}

func (um *DxmUserManagement) OrganizationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, organizationCode, err := aepr.GetParameterValueAsString("code")
	if err != nil {
		return err
	}
	_, organizationName, err := aepr.GetParameterValueAsString("name")
	if err != nil {
		return err
	}
	_, organizationType, err := aepr.GetParameterValueAsString("type")
	if err != nil {
		return err
	}

	o := utils.JSON{
		"code": organizationCode,
		"name": organizationName,
		"type": organizationType,
	}

	_, _, err = aepr.AssignParameterNullableInt64(&o, "parent_id")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "address")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "npwp")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "email")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "phonenumber")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "attribute1")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "attribute2")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "auth_source1")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "auth_source2")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "utag")
	if err != nil {
		return err
	}

	_, err = um.Organization.DoCreate(aepr, o)
	return err
}
func (um *DxmUserManagement) OrganizationCreateByUid(aepr *api.DXAPIEndPointRequest) (err error) {

	_, parentUid, err := aepr.GetParameterValueAsString("parent_uid")
	if err != nil {
		return err
	}
	_, parentOrganization, err := um.Organization.ShouldGetByUid(&aepr.Log, parentUid)
	if err != nil {
		return err
	}
	parentOrganizationId, err := utils.GetInt64FromKV(parentOrganization, "id")
	if err != nil {
		return err
	}
	_, code, err := aepr.GetParameterValueAsString("code")
	if err != nil {
		return err
	}
	_, name, err := aepr.GetParameterValueAsString("name")
	if err != nil {
		return err
	}
	_, organizationType, err := aepr.GetParameterValueAsString("type")
	if err != nil {
		return err
	}

	o := utils.JSON{
		"parent_id": parentOrganizationId,
		"code":      code,
		"name":      name,
		"type":      organizationType,
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "address")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "npwp")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "email")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "phonenumber")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "attribute1")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "attribute2")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "auth_source1")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "auth_source2")
	if err != nil {
		return err
	}

	_, _, err = aepr.AssignParameterNullableString(&o, "utag")
	if err != nil {
		return err
	}

	_, err = um.Organization.DoCreate(aepr, o)
	return err
}

func (um *DxmUserManagement) OrganizationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestRead(aepr)
}

func (um *DxmUserManagement) OrganizationReadByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.Organization.RequestReadByUid(aepr)
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
