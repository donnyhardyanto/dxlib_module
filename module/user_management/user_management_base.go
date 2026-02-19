package user_management

import (
	"fmt"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/databases/db"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/types"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/module/push_notification"
	"github.com/repoareta/pgn-partner-common/infrastructure/base"
)

const (
	UserStatusActive  = "ACTIVE"
	UserStatusSuspend = "SUSPEND"
	UserStatusDeleted = "DELETED"
)

type UserOrganizationMembershipType string

const (
	UserOrganizationMembershipTypeSingleOrganizationPerUser   UserOrganizationMembershipType = "SINGLE_ORGANIZATION_PER_USER"
	UserOrganizationMembershipTypeMultipleOrganizationPerUser UserOrganizationMembershipType = "MULTIPLE_ORGANIZATION_PER_USER"
)

type DXMUserLoginIdSyncTo string

const (
	DXMUserLoginIdSyncToNone        DXMUserLoginIdSyncTo = ""
	DXMUserLoginIdSyncToEmail       DXMUserLoginIdSyncTo = "EMAIL"
	DXMUserLoginIdSyncToPhoneNumber DXMUserLoginIdSyncTo = "PHONE_NUMBER"
)

var (
	DXMUserLoginIdSyncToEnumSetAll = []any{DXMUserLoginIdSyncToNone, DXMUserLoginIdSyncToEmail, DXMUserLoginIdSyncToPhoneNumber}
)

type OnUserPasswordValidationDef func(password string) (err error)
type DxmUserManagement struct {
	dxlibModule.DXModule
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
}

func (um *DxmUserManagement) Init(databaseNameId string, userPasswordEncryptionKeyDef *databases.EncryptionKeyDef) {
	um.DatabaseNameId = databaseNameId
	um.UserPasswordEncryptionKeyDef = userPasswordEncryptionKeyDef
	um.User = tables.NewDXTableSimple(databaseNameId,
		"user_management.user", "user_management.user", "user_management.v_user",
		"id", "uid", "loginid", "data",
		nil,
		[][]string{{"loginid"}, {"identity_number"}},
		[]string{"loginid", "email", "fullname", "phonenumber", "status", "identity_number", "identity_type", "address_on_identity_card", "membership_number", "organization_name", "organization_type"},
		[]string{"fullname", "email", "membership_number", "organization_name", "status", "phonenumber", "loginid", "identity_type", "identity_number", "attribute", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "loginid", "status", "identity_type", "identity_number", "membership_number", "created_at", "last_modified_at", "is_deleted"},
	)
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
		"user_management.role", "user_management.role", "user_management.role",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}, {"name"}},
		[]string{"nameid", "name", "description"},
		[]string{"name", "nameid", "description", "organization_types", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "nameid", "created_at", "last_modified_at", "is_deleted"},
	)
	um.Role.FieldNameForRowUtag = "utag"
	um.Role.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"organization_types": types.APIParameterTypeArrayString,
	}
	um.Organization = tables.NewDXTableSimple(databaseNameId,
		"user_management.organization", "user_management.organization", "user_management.v_organization",
		"id", "uid", "code", "data",
		nil,
		[][]string{{"code"}, {"name"}},
		[]string{"code", "name", "type", "address", "npwp", "email", "phonenumber", "status"},
		[]string{"code", "name", "tags", "status", "parent_id", "parent_uid", "type", "email", "phonenumber", "npwp", "address", "auth_source1", "auth_source2", "attribute1", "attribute2", "created_at", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_nameid", "id", "uid"},
		[]string{"id", "uid", "parent_id", "parent_uid", "code", "type", "status", "created_at", "last_modified_at", "is_deleted"},
	)
	um.Organization.FieldNameForRowUtag = "utag"
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
	um.RolePrivilege = tables.NewDXTableSimple(databaseNameId,
		"user_management.role_privilege", "user_management.role_privilege", "user_management.v_role_privilege",
		"id", "uid", "", "data",
		nil,
		[][]string{{"role_id", "privilege_id"}},
		[]string{"privilege_nameid", "privilege_name"},
		[]string{"role_id", "privilege_id", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "role_id", "privilege_id", "created_at", "last_modified_at", "is_deleted"},
	)
	um.UserRoleMembership = tables.NewDXTableSimple(databaseNameId,
		"user_management.user_role_membership", "user_management.user_role_membership", "user_management.v_user_role_membership",
		"id", "uid", "", "data",
		nil,
		[][]string{{"user_id", "role_id"}},
		[]string{"role_nameid", "role_name"},
		[]string{"user_id", "role_nameid", "role_name", "role_id", "organization_id", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "role_id", "organization_id", "created_at", "last_modified_at", "is_deleted"},
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
		[]string{"title", "body", "id", "user_id", "user_message_channel_type_id", "user_message_category_id", "is_read", "created_at", "last_modified_at", "id", "uid"},
		[]string{"id", "uid", "user_id", "title", "body", "user_message_channel_type_id", "user_message_category_id", "fcm_message_id", "fcm_application_id", "is_read", "created_at", "last_modified_at", "is_deleted"},
	)
}

func (um *DxmUserManagement) UserMessageCreateFCMAllApplication(l *log.DXLog, userId int64, userMessageCategoryId int64, templateTitle, templateBody string, templateData utils.JSON, attachedData map[string]string) (err error) {
	for key, value := range templateData {
		placeholder := fmt.Sprintf("<%s>", key)
		aValue := fmt.Sprintf("%v", value)
		templateBody = strings.ReplaceAll(templateBody, placeholder, aValue)
		templateTitle = strings.ReplaceAll(templateTitle, placeholder, aValue)
	}

	msgBody := templateBody
	msgTitle := templateTitle

	attachedDataAsJSON := utils.MapStringStringToJSON(attachedData)
	attachedDataAsJSONString, err := utils.JSONToString(attachedDataAsJSON)
	if err != nil {
		return err
	}
	err = push_notification.ModulePushNotification.FCM.AllApplicationSendToUser(l, userId, msgTitle, msgBody, attachedData,
		func(dtx *databases.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err2 error) {
			_, _, err2 = um.UserMessage.TxInsert(dtx, utils.JSON{
				"user_message_channel_type_id": base.UserMessageChannelTypeIdFCM,
				"user_message_category_id":     userMessageCategoryId,
				"fcm_message_id":               fcmMessageId,
				"fcm_application_id":           fcmApplicationId,
				"user_id":                      userId,
				"title":                        msgTitle,
				"body":                         msgBody,
				"data":                         attachedDataAsJSONString,
			}, nil)
			if err2 != nil {
				return err2
			}
			return nil
		})

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
