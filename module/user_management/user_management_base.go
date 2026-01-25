package user_management

import (
	"fmt"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/table"
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

type DxmUserManagement struct {
	dxlibModule.DXModule
	UserOrganizationMembershipType       UserOrganizationMembershipType
	SessionRedis                         *redis.DXRedis
	PreKeyRedis                          *redis.DXRedis
	User                                 *table.DXTable
	UserPassword                         *table.DXTable
	UserMessageChannnelType              *table.DXRawTable
	UserMessageCategory                  *table.DXRawTable
	UserMessage                          *table.DXTable
	Role                                 *table.DXTable
	Organization                         *table.DXTable
	OrganizationRoles                    *table.DXTable
	UserOrganizationMembership           *table.DXTable
	Privilege                            *table.DXTable
	RolePrivilege                        *table.DXTable
	UserRoleMembership                   *table.DXTable
	MenuItem                             *table.DXTable
	OnUserAfterCreate                    func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserResetPassword                  func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserRoleMembershipAfterCreate      func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON, organizationId int64) (err error)
	OnUserRoleMembershipBeforeSoftDelete func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
	OnUserRoleMembershipBeforeHardDelete func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
}

func (um *DxmUserManagement) Init(databaseNameId string) {
	um.DatabaseNameId = databaseNameId
	// NewDXTable3Simple(databaseNameId, tableName, resultObjectName, listViewNameId, fieldNameForRowId, fieldNameForRowUid, fieldNameForRowNameId, responseEnvelopeObjectName)
	um.User = table.NewDXTable3Simple(databaseNameId, "user_management.user",
		"user_management.user", "user_management.v_user", "id", "uid", "loginid", "data")
	um.UserPassword = table.NewDXTable3Simple(databaseNameId, "user_management.user_password",
		"user_management.user_password", "user_management.user_password", "id", "uid", "", "data")
	um.Role = table.NewDXTable3Simple(databaseNameId, "user_management.role",
		"user_management.role", "user_management.role", "id", "uid", "nameid", "data")
	um.Role.FieldNameForRowUtag = "utag"
	um.Role.FieldTypeMapping = map[string]string{
		"organization_types": "array-string",
	}
	um.Organization = table.NewDXTable3Simple(databaseNameId, "user_management.organization",
		"user_management.organization", "user_management.organization", "id", "uid", "code", "data")
	um.Organization.FieldNameForRowUtag = "utag"
	um.OrganizationRoles = table.NewDXTable3Simple(databaseNameId, "user_management.organization_role",
		"user_management.organization_role", "user_management.v_organization_role", "id", "uid", "", "data")
	um.UserOrganizationMembership = table.NewDXTable3Simple(databaseNameId, "user_management.user_organization_membership",
		"user_management.user_organization_membership", "user_management.v_user_organization_membership", "id", "uid", "", "data")
	um.Privilege = table.NewDXTable3Simple(databaseNameId, "user_management.privilege",
		"user_management.privilege", "user_management.v_privilege", "id", "uid", "nameid", "data")
	um.RolePrivilege = table.NewDXTable3Simple(databaseNameId, "user_management.role_privilege",
		"user_management.role_privilege", "user_management.v_role_privilege", "id", "uid", "", "data")
	um.UserRoleMembership = table.NewDXTable3Simple(databaseNameId, "user_management.user_role_membership",
		"user_management.user_role_membership", "user_management.v_user_role_membership", "id", "uid", "", "data")
	um.MenuItem = table.NewDXTable3Simple(databaseNameId, "user_management.menu_item",
		"user_management.menu_item", "user_management.v_menu_item", "id", "uid", "composite_nameid", "data")
	um.UserMessageChannnelType = table.NewDXRawTable3Simple(databaseNameId, "user_management.user_message_channel_type",
		"user_management.user_message_channel_type", "user_management.user_message_channel_type", "id", "uid", "nameid", "data")
	um.UserMessageCategory = table.NewDXRawTable3Simple(databaseNameId, "user_management.user_message_category",
		"user_management.user_message_category", "user_management.user_message_category", "id", "uid", "nameid", "data")
	um.UserMessage = table.NewDXTable3Simple(databaseNameId, "user_management.user_message",
		"user_management.user_message", "user_management.v_user_message", "id", "uid", "", "data")
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
		func(dtx *database.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err2 error) {
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
