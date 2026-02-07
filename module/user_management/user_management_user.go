package user_management

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/errors"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/crypto/datablock"
	"github.com/donnyhardyanto/dxlib/utils/crypto/rand"
	utilsJson "github.com/donnyhardyanto/dxlib/utils/json"
	"github.com/donnyhardyanto/dxlib/utils/lv"
	security "github.com/donnyhardyanto/dxlib/utils/security"
	"github.com/tealeg/xlsx"
	"github.com/teris-io/shortid"
)

func (um *DxmUserManagement) UserCreateBulk(aepr *api.DXAPIEndPointRequest) (err error) {
	// Get the request body stream
	bs := aepr.Request.Body
	if bs == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "FAILED_TO_GET_BODY_STREAM:%s", "UserCreateBulk")
	}
	defer func() {
		_ = bs.Close()
	}()

	// Read the entire request body into a buffer
	var buf bytes.Buffer
	_, err = io.Copy(&buf, bs)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "FAILED_TO_READ_REQUEST_BODY", "UserCreateBulk:%+v", err.Error())
	}

	// Determine the file type and parse accordingly
	contentType := utils.GetStringFromMapStringStringDefault(aepr.EffectiveRequestHeader, "Content-Type", "")
	if strings.Contains(contentType, "csv") {
		err = um.parseAndCreateUsersFromCSV(&buf, aepr)
	} else if strings.Contains(contentType, "excel") || strings.Contains(contentType, "spreadsheetml") {
		err = um.parseAndCreateUsersFromXLSX(&buf, aepr)
	} else {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnsupportedMediaType, "UNSUPPORTED_FILE_TYPE:%s", contentType)
	}

	if err != nil {
		return errors.Wrap(err, "error occurred")
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (um *DxmUserManagement) parseAndCreateUsersFromCSV(buf *bytes.Buffer, aepr *api.DXAPIEndPointRequest) error {
	// Create a new reader with comma as a delimiter
	reader := csv.NewReader(buf)
	reader.Comma = ';'          // Set comma as a delimiter
	reader.LazyQuotes = true    // Handle quotes more flexibly
	reader.FieldsPerRecord = -1 // Allow variable number of fields

	// Read the header row
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

		// Create user data map
		userData := make(map[string]interface{})
		for i, value := range record {
			if i >= len(cleanHeaders) {
				break
			}
			// Clean and validate the value
			value = strings.TrimSpace(value)
			if value != "" {
				userData[cleanHeaders[i]] = value
			}
		}

		// Skip empty rows
		if len(userData) == 0 {
			continue
		}

		// Create user
		err = um.doUserCreate(&aepr.Log, userData)
		if err != nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "",
				"FAILED_TO_CREATE_USER_LINE_%d: %s", lineNum, err.Error())
		}
	}

	return nil
}

func (um *DxmUserManagement) parseAndCreateUsersFromXLSX(buf *bytes.Buffer, aepr *api.DXAPIEndPointRequest) error {
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
			userData := make(map[string]interface{}, len(headers))

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
					continue // Skip empty values instead of adding them to userData
				}

				// Try to convert numeric values for specific columns
				if um.isNumericUserColumn(headers[i]) {
					if numVal, err := cell.Float(); err == nil {
						userData[headers[i]] = numVal
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
					userData[headers[i]] = value
				}
			}

			if len(userData) == 0 {
				continue
			}

			if err = um.doUserCreate(&aepr.Log, userData); err != nil {
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
					"FAILED_TO_CREATE_USER_AT_ROW_%d: %s",
					rowIdx+2,
					err.Error(),
				)
			}
		}
	}

	return nil
}

// Helper function to identify numeric columns for users
func (um *DxmUserManagement) isNumericUserColumn(header string) bool {
	// Add your numeric column names here for users
	numericColumns := map[string]bool{
		"organization_id": true,
		"role_id":         true,
		// Add other numeric column names as needed
	}

	header = strings.ToLower(header)
	return numericColumns[header]
}

// Helper function to create a user with proper validation
func (um *DxmUserManagement) doUserCreate(log *dxlibLog.DXLog, userData map[string]interface{}) error {
	// Validate required fields
	loginid, ok := userData["loginid"].(string)
	if !ok || loginid == "" {
		return errors.Errorf("loginid is required")
	}

	email, ok := userData["email"].(string)
	if !ok || email == "" {
		return errors.Errorf("email is required")
	}

	fullname, ok := userData["fullname"].(string)
	if !ok || fullname == "" {
		return errors.Errorf("fullname is required")
	}

	phonenumber, ok := userData["phonenumber"].(string)
	if !ok || phonenumber == "" {
		return errors.Errorf("phonenumber is required")
	}

	// Get organization ID
	var organizationId int64
	if orgId, ok := userData["organization_id"].(float64); ok {
		organizationId = int64(orgId)
	} else if orgName, ok := userData["organization_name"].(string); ok && orgName != "" {
		// Look up organization by name
		_, org, err := um.Organization.SelectOne(log, nil, utils.JSON{
			"name": orgName,
		}, nil, nil)
		if err != nil {
			return errors.Errorf("failed to find organization '%s': %v", orgName, err)
		}
		if org == nil {
			return errors.Errorf("organization '%s' not found", orgName)
		}
		organizationId, err = utils.GetInt64FromKV(org, "id")
		if err != nil {
			return err
		}
	} else {
		return errors.Errorf("organization_id or organization_name is required")
	}

	// Get role ID (default to a basic role if not specified)
	var roleId int64 = 1 // Default role ID, you might want to make this configurable
	if rId, ok := userData["role_id"].(float64); ok {
		roleId = int64(rId)
	}

	// Generate a default password (will be reset later)
	defaultPassword := generateRandomString(12)

	// Build user object
	userObj := utils.JSON{
		"loginid":              loginid,
		"email":                email,
		"fullname":             fullname,
		"phonenumber":          phonenumber,
		"status":               UserStatusActive,
		"must_change_password": true, // Force password change on first login
		"is_avatar_exist":      false,
	}

	// Handle optional fields
	if attribute, ok := userData["attribute"].(string); ok && attribute != "" {
		userObj["attribute"] = attribute
	}

	if identityNumber, ok := userData["identity_number"].(string); ok && identityNumber != "" {
		userObj["identity_number"] = identityNumber
	}

	if identityType, ok := userData["identity_type"].(string); ok && identityType != "" {
		userObj["identity_type"] = identityType
	}

	if gender, ok := userData["gender"].(string); ok && gender != "" {
		userObj["gender"] = gender
	}

	if addressOnId, ok := userData["address_on_identity_card"].(string); ok && addressOnId != "" {
		userObj["address_on_identity_card"] = addressOnId
	}

	membershipNumber, ok := userData["membership_number"].(string)
	if !ok {
		membershipNumber = ""
	}

	// Create a user in a transaction
	var userId int64
	var userOrganizationMembershipId int64
	var userRoleMembershipId int64

	err := databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) error {
		// Check if a user already exists
		_, existingUser, err := um.User.TxSelectOne(tx, nil, utils.JSON{
			"loginid": loginid,
		}, nil, nil, nil)
		if err != nil {
			return err
		}
		if existingUser != nil {
			return errors.Errorf("user with loginid '%s' already exists", loginid)
		}

		// Create user
		userId, err = um.User.TxInsertReturningId(tx, userObj)
		if err != nil {
			return err
		}

		// Create organization membership
		userOrganizationMembershipId, err = um.UserOrganizationMembership.TxInsertReturningId(tx, map[string]any{
			"user_id":           userId,
			"organization_id":   organizationId,
			"membership_number": membershipNumber,
		})
		if err != nil {
			return err
		}

		// Create role membership
		userRoleMembershipId, err = um.UserRoleMembership.TxInsertReturningId(tx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
			"role_id":         roleId,
		})
		if err != nil {
			return err
		}

		// Create password
		err = um.TxUserPasswordCreate(tx, userId, defaultPassword)
		if err != nil {
			return err
		}

		// Call post-create hooks if they exist
		if um.OnUserAfterCreate != nil {
			_, user, err := um.User.TxSelectOne(tx, nil, utils.JSON{
				"id": userId,
			}, nil, nil, nil)
			if err != nil {
				return err
			}
			err = um.OnUserAfterCreate(nil, tx, user, defaultPassword) // Pass nil for aepr since we don't have it in this context
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	log.Infof("Created user: %s (ID: %d, Org: %d, Role: %d)", loginid, userId, userOrganizationMembershipId, userRoleMembershipId)
	return nil
}

func (um *DxmUserManagement) UserSearchPaging(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.User

	qb := t.NewTableSelectQueryBuilder()

	return t.DoRequestSearchPagingList(aepr, qb, func(aepr *api.DXAPIEndPointRequest, list []utils.JSON) ([]utils.JSON, error) {
		for i, row := range list {
			userId, err := utils.GetInt64FromKV(row, "id")
			if err != nil {
				return list, err
			}
			_, userOrganizationMemberships, err := um.UserOrganizationMembership.Select(&aepr.Log, nil, utils.JSON{
				"user_id": userId,
			}, nil, nil, nil, nil)
			if err != nil {
				return list, err
			}
			list[i]["organizations"] = userOrganizationMemberships
			_, userRoleMemberships, err := um.UserRoleMembership.Select(&aepr.Log, nil, utils.JSON{
				"user_id": userId,
			}, nil, nil, nil, nil)
			if err != nil {
				return list, err
			}
			list[i]["roles"] = userRoleMemberships
		}
		return list, nil
	})
}

func (um *DxmUserManagement) UserCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	organizationId, ok := aepr.ParameterValues["organization_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ORGANIZATION_ID_MISSING", "")
	}
	_, _, err = um.Organization.ShouldGetById(&aepr.Log, organizationId)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ORGANIZATION_NOT_FOUND", "")
	}

	roleId, ok := aepr.ParameterValues["role_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ROLE_ID_MISSING", "")
	}
	_, _, err = um.Role.ShouldGetById(&aepr.Log, roleId)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ROLE_NOT_FOUND", "")
	}

	passwordI, ok := aepr.ParameterValues["password_i"].Value.(string)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "PASSWORD_PREKEY_INDEX_MISSING", "")
	}

	passwordD, ok := aepr.ParameterValues["password_d"].Value.(string)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "PASSWORD_DATA_BLOCK_MISSING", "")
	}

	lvPayloadElements, _, _, err := um.PreKeyUnpack(passwordI, passwordD)
	if err != nil {
		return err
	}

	lvPayloadPassword := lvPayloadElements[0]
	userPassword := string(lvPayloadPassword.Value)

	if um.OnUserFormatPasswordValidation != nil {
		err = um.OnUserFormatPasswordValidation(userPassword)
		if err != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PASSWORD_FORMAT:%s", "NOT_ERROR:INVALID_PASSWORD_FORMAT:%s", err.Error())
		}
	}

	attribute, ok := aepr.ParameterValues["attribute"].Value.(string)
	if !ok {
		attribute = ""
	}

	_, loginId, err := aepr.GetParameterValueAsString("loginid")
	if err != nil {
		return err
	}
	_, email, err := aepr.GetParameterValueAsString("email")
	if err != nil {
		return err
	}
	_, fullname, err := aepr.GetParameterValueAsString("fullname")
	if err != nil {
		return err
	}
	_, phonenumber, err := aepr.GetParameterValueAsString("phonenumber")
	if err != nil {
		return err
	}
	status := UserStatusActive

	p := utils.JSON{
		"loginid":              loginId,
		"email":                email,
		"fullname":             fullname,
		"phonenumber":          phonenumber,
		"status":               status,
		"attribute":            attribute,
		"must_change_password": false,
		"is_avatar_exist":      false,
	}

	identityNumber, ok := aepr.ParameterValues["identity_number"].Value.(string)
	if ok {
		p["identity_number"] = identityNumber
	}

	identityType, ok := aepr.ParameterValues["identity_type"].Value.(string)
	if ok {
		p["identity_type"] = identityType
	}

	gender, ok := aepr.ParameterValues["gender"].Value.(string)
	if ok {
		p["gender"] = gender
	}

	addressOnIdentityCard, ok := aepr.ParameterValues["address_on_identity_card"].Value.(string)
	if ok {
		p["address_on_identity_card"] = addressOnIdentityCard
	}

	membershipNumber, ok := aepr.ParameterValues["membership_number"].Value.(string)
	if !ok {
		membershipNumber = ""
	}

	var userId int64
	var userUid string
	var userOrganizationMembershipUid string
	var userRoleMembershipId int64
	var userRoleMembershipUid string

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"loginid": loginId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "USER_ALREADY_EXISTS", "USER_ALREADY_EXISTS:%v", loginId)
		}
		_, userReturning, err2 := um.User.TxInsert(tx, p, []string{"id", "uid"})
		if err2 != nil {
			return err2
		}
		userId, _ = utilsJson.GetInt64(userReturning, "id")
		if uid, ok := userReturning["uid"].(string); ok {
			userUid = uid
		}

		_, orgMemberReturning, err2 := um.UserOrganizationMembership.TxInsert(tx, map[string]any{
			"user_id":           userId,
			"organization_id":   organizationId,
			"membership_number": membershipNumber,
		}, []string{"uid"})
		if err2 != nil {
			return err2
		}
		if uid, ok := orgMemberReturning["uid"].(string); ok {
			userOrganizationMembershipUid = uid
		}

		_, roleMemberReturning, err2 := um.UserRoleMembership.TxInsert(tx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
			"role_id":         roleId,
		}, []string{"id", "uid"})
		if err2 != nil {
			return err2
		}
		userRoleMembershipId, _ = utilsJson.GetInt64(roleMemberReturning, "id")
		if uid, ok := roleMemberReturning["uid"].(string); ok {
			userRoleMembershipUid = uid
		}

		err2 = um.TxUserPasswordCreate(tx, userId, userPassword)
		if err2 != nil {
			return err2
		}

		if um.OnUserAfterCreate != nil {
			_, user, err2 = um.User.TxSelectOne(tx, nil, utils.JSON{
				"id": userId,
			}, nil, nil, nil)
			if err2 != nil {
				return err2
			}
			err2 = um.OnUserAfterCreate(aepr, tx, user, userPassword)
		}

		_, userRoleMembership, err := um.UserRoleMembership.TxSelectOne(tx, nil, utils.JSON{
			"id": userRoleMembershipId,
		}, nil, nil, nil)
		if err != nil {
			return err
		}
		if um.OnUserRoleMembershipAfterCreate != nil {
			err2 = um.OnUserRoleMembershipAfterCreate(aepr, tx, userRoleMembership, organizationId)
		}
		return nil
	})

	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"data": utils.JSON{
			"uid":                              userUid,
			"user_organization_membership_uid": userOrganizationMembershipUid,
			"user_role_membership_uid":         userRoleMembershipUid,
		}})

	return nil
}

func (um *DxmUserManagement) UserCreateV2(aepr *api.DXAPIEndPointRequest) (err error) {
	organizationId, ok := aepr.ParameterValues["organization_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ORGANIZATION_ID_MISSING", "")
	}
	_, _, err = um.Organization.ShouldGetById(&aepr.Log, organizationId)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ORGANIZATION_NOT_FOUND", "")
	}

	roleId, ok := aepr.ParameterValues["role_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ROLE_ID_MISSING", "")
	}
	_, _, err = um.Role.ShouldGetById(&aepr.Log, roleId)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "ROLE_NOT_FOUND", "")
	}

	userPassword, ok := aepr.ParameterValues["password"].Value.(string)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "PASSWORD_MISSING", "")
	}

	attribute, ok := aepr.ParameterValues["attribute"].Value.(string)
	if !ok {
		attribute = ""
	}

	_, loginId, err := aepr.GetParameterValueAsString("loginid")
	if err != nil {
		return err
	}
	_, email, err := aepr.GetParameterValueAsString("email")
	if err != nil {
		return err
	}
	_, fullname, err := aepr.GetParameterValueAsString("fullname")
	if err != nil {
		return err
	}
	_, phonenumber, err := aepr.GetParameterValueAsString("phonenumber")
	if err != nil {
		return err
	}
	_, isOrganic, err := aepr.GetParameterValueAsBool("is_organic", false)
	if err != nil {
		return err
	}
	_, loginIdSyncTo, err := aepr.GetParameterValueAsString("loginid_sync_to", string(DXMUserLoginIdSyncToNone))
	if err != nil {
		return err
	}

	_, membershipNumber, err := aepr.GetParameterValueAsString("membership_number", "")
	if err != nil {
		return err
	}
	status := UserStatusActive

	loginidSyncToAsEnum := DXMUserLoginIdSyncTo(loginIdSyncTo)
	switch loginidSyncToAsEnum {
	case DXMUserLoginIdSyncToNone:
		break
	case DXMUserLoginIdSyncToEmail:
		loginId = email
	case DXMUserLoginIdSyncToPhoneNumber:
		loginId = phonenumber
	default:
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "INVALID_LOGINID_SYNC_TO", "INVALID_LOGINID_SYNC_TO:%s", loginIdSyncTo)
	}

	p := utils.JSON{
		"loginid":              loginId,
		"email":                email,
		"fullname":             fullname,
		"phonenumber":          phonenumber,
		"status":               status,
		"attribute":            attribute,
		"must_change_password": false,
		"is_avatar_exist":      false,
		"is_organic":           isOrganic,
		"loginid_sync_to":      loginIdSyncTo,
	}

	identityNumber, ok := aepr.ParameterValues["identity_number"].Value.(string)
	if ok {
		p["identity_number"] = identityNumber
	}

	identityType, ok := aepr.ParameterValues["identity_type"].Value.(string)
	if ok {
		p["identity_type"] = identityType
	}

	gender, ok := aepr.ParameterValues["gender"].Value.(string)
	if ok {
		p["gender"] = gender
	}

	addressOnIdentityCard, ok := aepr.ParameterValues["address_on_identity_card"].Value.(string)
	if ok {
		p["address_on_identity_card"] = addressOnIdentityCard
	}

	if um.OnUserFormatPasswordValidation != nil {
		err = um.OnUserFormatPasswordValidation(userPassword)
		if err != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PASSWORD_FORMAT:%s", "NOT_ERROR:INVALID_PASSWORD_FORMAT:%s", err.Error())
		}
	}

	var userId int64
	var userUid string
	var userOrganizationMembershipUid string
	var userRoleMembershipId int64
	var userRoleMembershipUid string

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"loginid": loginId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "USER_ALREADY_EXISTS", "USER_ALREADY_EXISTS:%v", loginId)
		}
		_, userReturning, err2 := um.User.TxInsert(tx, p, []string{"id", "uid"})
		if err2 != nil {
			return err2
		}
		userId, _ = utilsJson.GetInt64(userReturning, "id")
		if uid, ok := userReturning["uid"].(string); ok {
			userUid = uid
		}

		_, orgMemberReturning, err2 := um.UserOrganizationMembership.TxInsert(tx, map[string]any{
			"user_id":           userId,
			"organization_id":   organizationId,
			"membership_number": membershipNumber,
		}, []string{"uid"})
		if err2 != nil {
			return err2
		}
		if uid, ok := orgMemberReturning["uid"].(string); ok {
			userOrganizationMembershipUid = uid
		}

		_, roleMemberReturning, err2 := um.UserRoleMembership.TxInsert(tx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
			"role_id":         roleId,
		}, []string{"id", "uid"})
		if err2 != nil {
			return err2
		}
		userRoleMembershipId, _ = utilsJson.GetInt64(roleMemberReturning, "id")
		if uid, ok := roleMemberReturning["uid"].(string); ok {
			userRoleMembershipUid = uid
		}

		err2 = um.TxUserPasswordCreate(tx, userId, userPassword)
		if err2 != nil {
			return err2
		}

		if um.OnUserAfterCreate != nil {
			_, user, err2 = um.User.TxSelectOne(tx, nil, utils.JSON{
				"id": userId,
			}, nil, nil, nil)
			if err2 != nil {
				return err2
			}
			err2 = um.OnUserAfterCreate(aepr, tx, user, userPassword)
			if err2 != nil {
				return err2
			}

		}

		_, userRoleMembership, err2 := um.UserRoleMembership.TxSelectOne(tx, nil, utils.JSON{
			"id": userRoleMembershipId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if um.OnUserRoleMembershipAfterCreate != nil {
			err2 = um.OnUserRoleMembershipAfterCreate(aepr, tx, userRoleMembership, organizationId)
			if err2 != nil {
				return err2
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"data": utils.JSON{
			"uid":                              userUid,
			"user_organization_membership_uid": userOrganizationMembershipUid,
			"user_role_membership_uid":         userRoleMembershipUid,
		}})

	return nil
}

func (um *DxmUserManagement) UserRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.RequestRead(aepr)
}

func (um *DxmUserManagement) UserReadByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.RequestReadByUid(aepr)
}

func (um *DxmUserManagement) DoUserEdit(aepr *api.DXAPIEndPointRequest, userId int64) (id int64, userUid any, err error) {
	t := um.User

	_, newKeyValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return 0, nil, err
	}

	p1 := utils.JSON{}
	membershipNumber, ok := newKeyValues["membership_number"].(string)
	if ok {
		p1["membership_number"] = membershipNumber
		delete(newKeyValues, "membership_number")
	}

	for k, v := range newKeyValues {
		if v == nil {
			delete(newKeyValues, k)
		}
	}

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) (err2 error) {
		if len(newKeyValues) > 0 {
			_, err2 = um.User.TxUpdateSimple(dtx, newKeyValues, utils.JSON{
				t.FieldNameForRowId: userId,
			})
			if err2 != nil {
				return err2
			}
			_, user, err2 := um.User.ShouldGetById(&aepr.Log, userId)
			if err2 != nil {
				return err2
			}
			_, ok := newKeyValues["loginid_sync_to"]
			if ok {
				loginIdSyncTo, err := utils.GetStringFromKV(newKeyValues, "loginid_sync_to")
				if err != nil {
					return err
				}
				loginidSyncToAsEnum := DXMUserLoginIdSyncTo(loginIdSyncTo)
				switch loginidSyncToAsEnum {
				case DXMUserLoginIdSyncToNone:
					break
				case DXMUserLoginIdSyncToEmail:
					email, err2 := utils.GetStringFromKV(user, "email")
					if err2 != nil {
						return err2
					}
					newKeyValues["loginid"] = email
				case DXMUserLoginIdSyncToPhoneNumber:
					phonenumber, err2 := utils.GetStringFromKV(user, "phonenumber")
					if err2 != nil {
						return err2
					}
					newKeyValues["loginid"] = phonenumber
				default:
					return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "INVALID_LOGINID_SYNC_TO", "INVALID_LOGINID_SYNC_TO:%s", loginIdSyncTo)
				}
			}
		}
		if len(p1) > 0 {
			_, err2 = um.UserOrganizationMembership.TxUpdateSimple(dtx, p1, utils.JSON{
				"user_id": userId,
			})
			if err2 != nil {
				return err2
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	_, userRow, err := t.ShouldGetById(&aepr.Log, userId)
	if err != nil {
		return 0, nil, err
	}

	return userId, userRow["uid"], nil
}

func (um *DxmUserManagement) UserEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.User
	_, id, err := aepr.GetParameterValueAsInt64(t.FieldNameForRowId)
	if err != nil {
		return err
	}

	userId, userUid, err := um.DoUserEdit(aepr, id)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"id":  userId,
		"uid": userUid,
	}})

	return nil

}

func (um *DxmUserManagement) DoUserDelete(aepr *api.DXAPIEndPointRequest, userId int64) (id int64, userUid any, err error) {
	var uid any
	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"id": userId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user == nil {
			return errors.New("USER_NOT_FOUND")
		}
		userIsDeleted, ok := user["is_deleted"].(bool)
		if !ok {
			return errors.New("USER_IS_DELETED_NOT_FOUND")
		}
		if userIsDeleted {
			return errors.New("USER_IS_DELETED")
		}
		uid = user["uid"]

		_, err2 = um.User.TxUpdateSimple(tx, utils.JSON{
			"is_deleted": true,
			"status":     UserStatusDeleted,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err2 != nil {
			return err2
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	return userId, uid, nil
}

func (um *DxmUserManagement) UserDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	id, uid, err := um.DoUserDelete(aepr, userId)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"id":  id,
		"uid": uid,
	}})
	return nil
}

func (um *DxmUserManagement) UserEditByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.User
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}
	_, row, err := t.ShouldGetByUid(&aepr.Log, uid)
	if err != nil {
		return err
	}
	id, err := utils.GetInt64FromKV(row, t.FieldNameForRowId)
	if err != nil {
		return err
	}

	_, userUid, err := um.DoUserEdit(aepr, id)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"uid": userUid,
	}})

	return nil
}

func (um *DxmUserManagement) UserDeleteByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}
	_, row, err := um.User.ShouldGetByUid(&aepr.Log, uid)
	if err != nil {
		return err
	}
	userId, err := utils.GetInt64FromKV(row, um.User.FieldNameForRowId)
	if err != nil {
		return err
	}

	_, userUid, err := um.DoUserDelete(aepr, userId)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"uid": userUid,
	}})
	return nil
}

func (um *DxmUserManagement) UserSuspend(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("id")

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"id": userId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user == nil {
			return errors.New("USER_NOT_FOUND")
		}
		userIsDeleted, ok := user["is_deleted"].(bool)
		if !ok {
			return errors.New("USER_IS_DELETED_NOT_FOUND")
		}
		if userIsDeleted {
			return errors.New("USER_IS_DELETED")
		}
		_, err2 = um.User.TxUpdateSimple(tx, utils.JSON{
			"status": UserStatusSuspend,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err2 != nil {
			return err2
		}
		return nil
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (um *DxmUserManagement) UserActivate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("id")

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"id": userId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user == nil {
			return errors.New("USER_NOT_FOUND")
		}
		userIsDeleted, ok := user["is_deleted"].(bool)
		if !ok {
			return errors.New("USER_IS_DELETED_NOT_FOUND")
		}
		if userIsDeleted {
			return errors.New("USER_IS_DELETED")
		}
		_, err2 = um.User.TxUpdateSimple(tx, utils.JSON{
			"status": UserStatusActive,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err2 != nil {
			return err2
		}
		return nil
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (um *DxmUserManagement) UserUndelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("id")

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, nil, utils.JSON{
			"id": userId,
		}, nil, nil, nil)
		if err2 != nil {
			return err2
		}
		if user == nil {
			return errors.New("USER_NOT_FOUND")
		}
		userIsDeleted, ok := user["is_deleted"].(bool)
		if !ok {
			return errors.New("USER_IS_DELETED_NOT_FOUND")
		}
		if !userIsDeleted {
			return errors.New("USER_IS_NOT_DELETED")
		}

		_, err2 = um.User.TxUpdateSimple(tx, utils.JSON{
			"status":     UserStatusActive,
			"is_deleted": false,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": true,
		})

		if err2 != nil {
			return err2
		}
		return nil
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (um *DxmUserManagement) TxUserPasswordCreate(tx *databases.DXDatabaseTx, userId int64, password string) (err error) {
	hashedPasswordAsHexString, err := um.passwordHashCreate(password)
	if err != nil {
		return err
	}
	_, err = um.UserPassword.TxInsertAutoReturningId(tx, utils.JSON{
		"user_id": userId,
		"value":   hashedPasswordAsHexString,
	})
	if err != nil {
		return err
	}
	return nil
}

func hashBlock(saltValue []byte, saltMethod byte, data []byte) ([]byte, error) {
	passwordBlock := append(saltValue, saltMethod)
	passwordBlock = append(passwordBlock, data...)

	var hashPasswordBlock []byte
	switch saltMethod {
	case 1:
		hashPasswordBlock = security.HashSHA512(data)
	case 2:
		hashPasswordBlock, err := security.HashBcrypt(data)
		if err != nil {
			return hashPasswordBlock, err
		}
	default:
		return hashPasswordBlock, errors.New(fmt.Sprintf("Unknown salt method %d", saltMethod))
	}
	return hashPasswordBlock, nil
}

func (um *DxmUserManagement) passwordHashCreate(password string) (hashedString string, err error) {
	salt := shortid.MustGenerate()[:8]
	passwordAsBytes := []byte(password)

	lvSalt, err := lv.NewLV([]byte(salt))
	if err != nil {
		return "", err
	}

	var saltMethod byte
	saltMethod = 1 // 1: sha512
	saltMethodAsByte := []byte{saltMethod}

	lvSaltMethod, err := lv.NewLV(saltMethodAsByte)
	if err != nil {
		return "", err
	}

	hashPasswordBlock, err := hashBlock(lvSalt.Value, lvSaltMethod.Value[0], passwordAsBytes)

	lvHashedPasswordBlock, err := lv.NewLV(hashPasswordBlock)
	if err != nil {
		return "", err
	}

	lvHashedPassword, err := lv.CombineLV(lvSalt, lvSaltMethod, lvHashedPasswordBlock)
	if err != nil {
		return "", err
	}

	lvHashedPasswordAsBytes, err := lvHashedPassword.MarshalBinary()
	if err != nil {
		return "", err
	}

	hashPasswordBlockAsHexString := hex.EncodeToString(lvHashedPasswordAsBytes)
	return hashPasswordBlockAsHexString, nil
}

func (um *DxmUserManagement) passwordHashVerify(tryPassword string, hashedPasswordAsHexString string) (verificationResult bool, err error) {
	hashedPasswordAsBytes, err := hex.DecodeString(hashedPasswordAsHexString)
	if err != nil {
		return false, err
	}

	lvHashedPassword := lv.LV{}
	err = lvHashedPassword.UnmarshalBinary(hashedPasswordAsBytes)
	if err != nil {
		return false, err
	}

	lvSeparateElements, err := lvHashedPassword.Expand()
	if err != nil {
		return false, err
	}

	if lvSeparateElements == nil {
		return false, errors.New("lvSeparateElements.IS_NIL")
	}

	if len(lvSeparateElements) < 3 {
		return false, errors.New("lvSeparateElements.IS_NOT_3")
	}

	lvSalt := lvSeparateElements[0]
	lvSaltMethod := lvSeparateElements[1]
	saltMethod := lvSaltMethod.Value[0]
	lvHashedUserPasswordBlock := lvSeparateElements[2]

	tryPasswordAsBytes := []byte(tryPassword)

	tryHashPasswordBlock, err := hashBlock(lvSalt.Value, saltMethod, tryPasswordAsBytes)
	if err != nil {
		return false, err
	}

	verificationResult = bytes.Equal(tryHashPasswordBlock, lvHashedUserPasswordBlock.Value)
	return verificationResult, nil
}

func (um *DxmUserManagement) UserPasswordVerify(l *dxlibLog.DXLog, userId int64, tryPassword string) (verificationResult bool, err error) {
	_, userPasswordRow, err := um.UserPassword.SelectOneAuto(l, []string{"id", "user_id", "value"}, utils.JSON{
		"user_id": userId,
	}, nil, map[string]string{"id": "DESC"})
	if err != nil {
		return false, err
	}
	if userPasswordRow == nil {
		return false, errors.New("userPasswordVerify:USER_PASSWORD_NOT_FOUND")
	}
	userPasswordValue, err := utils.GetStringFromKV(userPasswordRow, "value")
	if err != nil {
		return false, err
	}
	verificationResult, err = um.passwordHashVerify(tryPassword, userPasswordValue)
	if err != nil {
		return false, err
	}
	return verificationResult, nil
}

func (um *DxmUserManagement) PreKeyUnpack(preKeyIndex string, datablockAsString string) (lvPayloadElements []*lv.LV, sharedKey2AsBytes []byte, edB0PrivateKeyAsBytes []byte, err error) {
	if preKeyIndex == "" || datablockAsString == "" {
		return nil, nil, nil, errors.New("PARAMETER_IS_EMPTY")
	}

	preKeyData, err := um.PreKeyRedis.Get(preKeyIndex)
	if err != nil {
		return nil, nil, nil, err
	}
	if preKeyData == nil {
		return nil, nil, nil, errors.New("PREKEY_NOT_FOUND")
	}

	sharedKey1AsHexString, err := utils.GetStringFromKV(preKeyData, "shared_key_1")
	if err != nil {
		return nil, nil, nil, err
	}
	sharedKey2AsHexString, err := utils.GetStringFromKV(preKeyData, "shared_key_2")
	if err != nil {
		return nil, nil, nil, err
	}
	edA0PublicKeyAsHexString, err := utils.GetStringFromKV(preKeyData, "a0_public_key")
	if err != nil {
		return nil, nil, nil, err
	}
	edB0PrivateKeyAsHexString, err := utils.GetStringFromKV(preKeyData, "b0_private_key")
	if err != nil {
		return nil, nil, nil, err
	}

	sharedKey1AsBytes, err := hex.DecodeString(sharedKey1AsHexString)
	if err != nil {
		return nil, nil, nil, err
	}
	sharedKey2AsBytes, err = hex.DecodeString(sharedKey2AsHexString)
	if err != nil {
		return nil, nil, nil, err
	}
	edA0PublicKeyAsBytes, err := hex.DecodeString(edA0PublicKeyAsHexString)
	if err != nil {
		return nil, nil, nil, err
	}

	edB0PrivateKeyAsBytes, err = hex.DecodeString(edB0PrivateKeyAsHexString)
	if err != nil {
		return nil, nil, nil, err
	}

	lvPayloadElements, err = datablock.UnpackLVPayload(preKeyIndex, edA0PublicKeyAsBytes, sharedKey1AsBytes, datablockAsString)
	if err != nil {
		return nil, nil, nil, err
	}

	return lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, nil
}

func (um *DxmUserManagement) PreKeyUnpackCaptcha(preKeyIndex string, datablockAsString string) (
	lvPayloadElements []*lv.LV, sharedKey2AsBytes []byte, edB0PrivateKeyAsBytes []byte, captchaId string, captchaText string, err error,
) {
	if preKeyIndex == "" || datablockAsString == "" {
		return nil, nil, nil, "", "", errors.New("PARAMETER_IS_EMPTY")
	}

	preKeyData, err := um.PreKeyRedis.Get(preKeyIndex)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	if preKeyData == nil {
		return nil, nil, nil, "", "", errors.New("PREKEY_NOT_FOUND")
	}

	sharedKey1AsHexString, err := utils.GetStringFromKV(preKeyData, "shared_key_1")
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	sharedKey2AsHexString, err := utils.GetStringFromKV(preKeyData, "shared_key_2")
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	edA0PublicKeyAsHexString, err := utils.GetStringFromKV(preKeyData, "a0_public_key")
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	edB0PrivateKeyAsHexString, err := utils.GetStringFromKV(preKeyData, "b0_private_key")
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	captchaId, err = utils.GetStringFromKV(preKeyData, "captcha_id")
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	captchaText, err = utils.GetStringFromKV(preKeyData, "captcha_text")
	if err != nil {
		return nil, nil, nil, "", "", err
	}

	sharedKey1AsBytes, err := hex.DecodeString(sharedKey1AsHexString)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	sharedKey2AsBytes, err = hex.DecodeString(sharedKey2AsHexString)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	edA0PublicKeyAsBytes, err := hex.DecodeString(edA0PublicKeyAsHexString)
	if err != nil {
		return nil, nil, nil, "", "", err
	}

	edB0PrivateKeyAsBytes, err = hex.DecodeString(edB0PrivateKeyAsHexString)
	if err != nil {
		return nil, nil, nil, "", "", err
	}

	lvPayloadElements, err = datablock.UnpackLVPayload(preKeyIndex, edA0PublicKeyAsBytes, sharedKey1AsBytes, datablockAsString)
	if err != nil {
		return nil, nil, nil, "", "", err
	}

	return lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, captchaId, captchaText, nil
}

func generateRandomString(n int) string {
	return rand.RandomString(n)
}

func (um *DxmUserManagement) UserResetPassword(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")
	if err != nil {
		return err
	}

	_, user, err := um.User.SelectOne(&aepr.Log, nil, utils.JSON{
		"id": userId,
	}, nil, nil)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("USER_NOT_FOUND")
	}

	userPasswordNew := generateRandomString(10)

	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err error) {

		err = um.TxUserPasswordCreate(tx, userId, userPasswordNew)
		if err != nil {
			return err
		}
		aepr.Log.Infof("User password changed")

		_, err = um.User.TxUpdateSimple(tx, utils.JSON{
			"must_change_password": true,
		}, utils.JSON{
			"id": userId,
		})
		if err != nil {
			return err
		}

		if um.OnUserResetPassword != nil {
			err = um.OnUserResetPassword(aepr, tx, user, userPasswordNew)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return nil
}
