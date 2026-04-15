package user_management

import (
	"context"
	"fmt"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	dxlibBase "github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/databases/db"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/types"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/string_template"
	"github.com/donnyhardyanto/dxlib_module/module/push_notification"
	"github.com/repoareta/pgn-partner-common/infrastructure/base"
)

const (
	UserStatusActive  = "ACTIVE"
	UserStatusSuspend = "SUSPEND"
	UserStatusDeleted = "DELETED"
)

const (
	OrganizationStatusActive  = "ACTIVE"
	OrganizationStatusSuspend = "SUSPEND"
	OrganizationStatusDeleted = "DELETED"
)

type UserOrganizationMembershipType string

const (
	UserOrganizationMembershipTypeSingleOrganizationPerUser   UserOrganizationMembershipType = "SINGLE_ORGANIZATION_PER_USER"
	UserOrganizationMembershipTypeMultipleOrganizationPerUser UserOrganizationMembershipType = "MULTIPLE_ORGANIZATION_PER_USER"
)

type DXMUserLoginIdSyncTo string

const (
	DXMUserLoginIdSyncToNone        DXMUserLoginIdSyncTo = "NONE"
	DXMUserLoginIdSyncToEmail       DXMUserLoginIdSyncTo = "EMAIL"
	DXMUserLoginIdSyncToPhoneNumber DXMUserLoginIdSyncTo = "PHONE_NUMBER"
	DXMUserLoginIdSyncToLdapLoginId DXMUserLoginIdSyncTo = "LDAP_LOGINID"
)

var (
	DXMUserLoginIdSyncToEnumSetAll = []any{DXMUserLoginIdSyncToNone, DXMUserLoginIdSyncToEmail, DXMUserLoginIdSyncToPhoneNumber, DXMUserLoginIdSyncToLdapLoginId}
)

const MinPasswordHashMethod byte = 2 // bcrypt — hardcoded floor

type OnUserPasswordValidationDef func(password string) (err error)
type DxmUserManagement struct {
	dxlibModule.DXModule
	CurrentPasswordHashMethod            byte
	UserPasswordEncryptionKeyDef         *databases.EncryptionKeyDef
	UserOrganizationMembershipType       UserOrganizationMembershipType
	SessionRedis                         *redis.DXRedis
	PreKeyRedis                          *redis.DXRedis
	User                                 *tables.DXTable
	UserPassword                         *tables.DXTable
	UserMessageChannelType               *tables.DXRawTable
	UserMessageCategory                  *tables.DXRawTable
	UserMessage                          *tables.DXTable
	Role                                 *tables.DXTable
	Organization                         *tables.DXTable
	OrganizationRoles                    *tables.DXTable
	UserOrganizationMembership           *tables.DXTable
	Privilege                            *tables.DXTable
	RolePrivilege                        *tables.DXTable
	UserRoleMembership                   *tables.DXTable
	MenuItem                             *tables.DXTable
	OnUserFormatPasswordValidation       OnUserPasswordValidationDef
	OnUserAfterCreate                    func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserResetPassword                  func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserRoleMembershipAfterCreate      func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, userRoleMembership utils.JSON, organizationId int64) (err error)
	OnUserRoleMembershipBeforeSoftDelete func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
	OnUserRoleMembershipBeforeHardDelete func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
	OnUserBeforeDelete                   func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, userId int64) (err error)
	OnUserAfterDelete                    func(aepr *api.DXAPIEndPointRequest, dtx *databases.DXDatabaseTx, userId int64) (err error)
}

func (um *DxmUserManagement) SetPasswordHashMethod(method byte) {
	if method < MinPasswordHashMethod {
		method = MinPasswordHashMethod
	}
	um.CurrentPasswordHashMethod = method
}

func (um *DxmUserManagement) Init(databaseNameId string, userPasswordEncryptionKeyDef *databases.EncryptionKeyDef) {
	um.DatabaseNameId = databaseNameId
	um.UserPasswordEncryptionKeyDef = userPasswordEncryptionKeyDef
	um.CurrentPasswordHashMethod = MinPasswordHashMethod
	um.User = tables.NewDXTableSimple(databaseNameId,
		"user_management.user", "user_management.user", "user_management.v_user",
		"id", "uid", "loginid", "data",
		nil,
		[][]string{{"loginid"}, {"identity_number"}},
		[]string{"loginid", "email", "fullname", "phonenumber", "status", "identity_number", "identity_type", "address_on_identity_card", "membership_number", "organization_name", "organization_type", "ldap_loginid", "attribute", "role_names_text", "role_nameids_text"},
		[]string{"fullname", "email", "membership_number", "organization_name", "status", "phonenumber", "loginid", "identity_type", "identity_number", "address_on_identity_card", "is_avatar_exist", "attribute", "ldap_loginid", "role_names_text", "role_nameids_text", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "loginid", "status", "identity_type", "identity_number", "address_on_identity_card", "is_avatar_exist", "membership_number", "ldap_loginid", "role_nameids_text", "created_at", "last_modified_at", "is_deleted", "organization_ids"},
	)
	um.User.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"organization_ids": types.APIParameterTypeArrayInt64,
	}
	um.UserPassword = tables.NewDXTableWithEncryption(databaseNameId,
		"user_management.user_password", "user_management.user_password", "user_management.v_user_password",
		"id", "uid", "", "data",
		nil,
		[]databases.EncryptionColumnDef{
			{FieldName: "value_encrypted", DataFieldName: "value", AliasName: "value", EncryptionKeyDef: um.UserPasswordEncryptionKeyDef, HashFieldName: "", ViewHasDecrypt: true},
		},
		nil,
		nil,
		[]string{"user_id", "created_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.Role = tables.NewDXTableSimple(databaseNameId,
		"user_management.role", "user_management.role", "user_management.v_role",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}, {"name"}},
		[]string{"nameid", "name", "description", "created_by_user_nameid", "last_modified_by_user_nameid", "organization_types_text"},
		[]string{"name", "nameid", "description", "organization_types", "organization_types_text", "parent_id", "parent_uid", "absolute_path", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "parent_id", "parent_uid", "absolute_path", "nameid", "created_at", "last_modified_at", "is_deleted"},
	)
	um.Role.FieldNameForRowUtag = "utag"
	um.Role.DownloadableOrderByFieldNames = []string{"name", "nameid", "description", "organization_types", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"}
	um.Role.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"organization_types": types.APIParameterTypeArrayString,
	}
	um.Organization = tables.NewDXTableSimple(databaseNameId,
		"user_management.organization", "user_management.organization", "user_management.v_organization",
		"id", "uid", "code", "data",
		nil,
		[][]string{{"code"}, {"name"}},
		[]string{"code", "name", "type", "address", "npwp", "email", "phonenumber", "status", "auth_source1", "auth_source2", "created_by_user_nameid", "last_modified_by_user_nameid", "tags", "role_names_text", "role_nameids_text"},
		[]string{"code", "name", "tags", "status", "parent_id", "parent_uid", "parent_name", "parent_code", "type", "email", "phonenumber", "npwp", "address", "auth_source1", "auth_source2", "attribute1", "attribute2", "role_names_text", "role_nameids_text", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "parent_id", "parent_uid", "parent_name", "parent_code", "code", "type", "status", "created_at", "last_modified_at", "is_deleted", "role_nameids_text"},
	)
	um.Organization.FieldNameForRowUtag = "utag"
	// Set download columns to match datatable columns
	um.Organization.DownloadableOrderByFieldNames = []string{"code", "name", "status", "type", "email", "phonenumber", "npwp", "address", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid"}
	um.OrganizationRoles = tables.NewDXTableSimple(databaseNameId,
		"user_management.organization_role", "user_management.organization_role", "user_management.v_organization_role",
		"id", "uid", "", "data",
		nil,
		[][]string{{"organization_id", "role_id"}},
		[]string{"role_nameid", "role_name", "role_description", "organization_name", "organization_type", "organization_address"},
		[]string{"role_nameid", "role_name", "organization_id", "role_id", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "organization_uid", "role_nameid", "role_name", "organization_id", "role_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.UserOrganizationMembership = tables.NewDXTableSimple(databaseNameId,
		"user_management.user_organization_membership", "user_management.user_organization_membership", "user_management.v_user_organization_membership",
		"id", "uid", "", "data",
		nil,
		[][]string{{"user_id"}},
		[]string{"membership_number", "organization_name", "organization_type"},
		[]string{"user_id", "organization_id", "order_index", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "organization_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.Privilege = tables.NewDXTableSimple(databaseNameId,
		"user_management.privilege", "user_management.privilege", "user_management.v_privilege",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "name", "description"},
		[]string{"name", "nameid", "description", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid", "menu_name", "parent_menu", "menu_item_id"},
		[]string{"id", "uid", "nameid", "created_at", "last_modified_at", "is_deleted", "menu_name", "parent_menu", "menu_item_id"},
	)
	// Set download columns without menu_name, parent_menu, menu_item_id
	um.Privilege.DownloadableOrderByFieldNames = []string{"name", "nameid", "description", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"}
	um.RolePrivilege = tables.NewDXTableSimple(databaseNameId,
		"user_management.role_privilege", "user_management.role_privilege", "user_management.v_role_privilege",
		"id", "uid", "", "data",
		nil,
		[][]string{{"role_id", "privilege_id"}},
		[]string{"privilege_nameid", "privilege_name"},
		[]string{"role_id", "privilege_id", "privilege_nameid", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "role_id", "role_uid", "privilege_id", "privilege_nameid", "created_at", "last_modified_at", "is_deleted"},
	)
	um.UserRoleMembership = tables.NewDXTableSimple(databaseNameId,
		"user_management.user_role_membership", "user_management.user_role_membership", "user_management.v_user_role_membership",
		"id", "uid", "", "data",
		nil,
		[][]string{{"user_id", "role_id"}},
		[]string{"role_nameid", "role_name"},
		[]string{"user_id", "role_utag", "role_nameid", "role_name", "role_id", "organization_id", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "role_id", "role_utag", "organization_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.MenuItem = tables.NewDXTableSimple(databaseNameId,
		"user_management.menu_item", "user_management.menu_item", "user_management.v_menu_item",
		"id", "uid", "composite_nameid", "data",
		nil,
		nil,
		[]string{"nameid", "name", "composite_nameid", "privilege_nameid"},
		[]string{"parent_id", "nameid", "name", "level", "item_index", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "parent_id", "nameid", "composite_nameid", "privilege_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.UserMessageChannelType = tables.NewDXRawTableSimple(databaseNameId,
		"user_management.user_message_channel_type", "user_management.user_message_channel_type", "user_management.user_message_channel_type",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}, {"name"}},
		[]string{"nameid", "name"},
		[]string{"nameid", "name", "id", "uid"},
		[]string{"id", "uid", "nameid"},
	)
	um.UserMessageCategory = tables.NewDXRawTableSimple(databaseNameId,
		"user_management.user_message_category", "user_management.user_message_category", "user_management.user_message_category",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}, {"name"}},
		[]string{"nameid", "name"},
		[]string{"nameid", "name", "id", "uid"},
		[]string{"id", "uid", "nameid"},
	)
	um.UserMessage = tables.NewDXTableSimple(databaseNameId,
		"user_management.user_message", "user_management.user_message", "user_management.v_user_message",
		"id", "uid", "", "data",
		nil,
		nil,
		[]string{"title", "body", "user_message_channel_type_nameid", "user_message_channel_type_name", "user_message_category_nameid", "user_message_category_name"},
		[]string{"title", "body", "data", "id", "user_id", "user_message_channel_type_id", "user_message_category_id", "is_read", "sent_at", "arrive_at", "read_at", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "title", "body", "data", "user_message_channel_type_id", "user_message_category_id", "is_read", "created_at", "last_modified_at", "is_deleted"},
	)
	um.UserMessage.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"data": types.APIParameterTypeMapStringString,
	}
}

func OrganizationIdsFragment(dbType dxlibBase.DXDatabaseType, userIdRef string) string {
	switch dbType {
	case dxlibBase.DXDatabaseTypePostgreSQL, dxlibBase.DXDatabaseTypePostgresSQLV2:
		return getPostgreSQLOrganizationIdsFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeSQLServer:
		return getSQLServerOrganizationIdsFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeOracle:
		return getOracleOrganizationIdsFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeMariaDB:
		return getMariaDBOrganizationIdsFragment(userIdRef)
	default:
		return getMariaDBOrganizationIdsFragment(userIdRef)
	}
}

func RoleNamesTextFragment(dbType dxlibBase.DXDatabaseType, userIdRef string) string {
	switch dbType {
	case dxlibBase.DXDatabaseTypePostgreSQL, dxlibBase.DXDatabaseTypePostgresSQLV2:
		return getPostgreSQLRoleNamesTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeSQLServer:
		return getSQLServerRoleNamesTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeOracle:
		return getOracleRoleNamesTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeMariaDB:
		return getMariaDBRoleNamesTextFragment(userIdRef)
	default:
		return getMariaDBRoleNamesTextFragment(userIdRef)
	}
}

func RoleNameidsTextFragment(dbType dxlibBase.DXDatabaseType, userIdRef string) string {
	switch dbType {
	case dxlibBase.DXDatabaseTypePostgreSQL, dxlibBase.DXDatabaseTypePostgresSQLV2:
		return getPostgreSQLRoleNameidsTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeSQLServer:
		return getSQLServerRoleNameidsTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeOracle:
		return getOracleRoleNameidsTextFragment(userIdRef)
	case dxlibBase.DXDatabaseTypeMariaDB:
		return getMariaDBRoleNameidsTextFragment(userIdRef)
	default:
		return getMariaDBRoleNameidsTextFragment(userIdRef)
	}
}

func (um *DxmUserManagement) UserMessageCreateFCMAllApplication(ctx context.Context, l *log.DXLog, userId int64, userMessageCategoryId int64, templateTitle, templateBody string, templateData utils.JSON, attachedData map[string]string) (err error) {
	for key, value := range templateData {
		if nestedMap, ok := value.(map[string]any); ok {
			templateBody = string_template.ReplaceTagWithValue(templateBody, key, nestedMap)
			templateTitle = string_template.ReplaceTagWithValue(templateTitle, key, nestedMap)
		} else {
			placeholder := fmt.Sprintf("<%s>", key)
			aValue := fmt.Sprintf("%v", value)
			templateBody = strings.ReplaceAll(templateBody, placeholder, aValue)
			templateTitle = strings.ReplaceAll(templateTitle, placeholder, aValue)
		}
	}

	msgBody := templateBody
	msgTitle := templateTitle

	attachedDataAsJSON := utils.MapStringStringToJSON(attachedData)
	attachedDataAsJSONString, err := utils.JSONToString(attachedDataAsJSON)
	if err != nil {
		return err
	}

	userMessageId, err := um.UserMessage.InsertReturningId(ctx, l, utils.JSON{
		"user_message_channel_type_id": base.UserMessageChannelTypeIdFCM,
		"user_message_category_id":     userMessageCategoryId,
		"user_id":                      userId,
		"title":                        msgTitle,
		"body":                         msgBody,
		"data":                         attachedDataAsJSONString,
	})
	if err != nil {
		return err
	}

	err = push_notification.ModulePushNotification.FCM.AllApplicationSendToUser(ctx, l, userId, msgTitle, msgBody, attachedData, userMessageId)
	if err != nil {
		return err
	}

	return nil
}

var ModuleUserManagement DxmUserManagement

func init() {
	ModuleUserManagement = DxmUserManagement{
		UserOrganizationMembershipType: UserOrganizationMembershipTypeMultipleOrganizationPerUser,
	}
}
