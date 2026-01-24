package user_management

import (
	"fmt"
	"strings"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database2"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/table3"
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
	User                                 *table3.DXTable3
	UserPassword                         *table3.DXTable3
	UserMessageChannnelType              *table3.DXRawTable3
	UserMessageCategory                  *table3.DXRawTable3
	UserMessage                          *table3.DXTable3
	Role                                 *table3.DXTable3
	Organization                         *table3.DXTable3
	OrganizationRoles                    *table3.DXTable3
	UserOrganizationMembership           *table3.DXTable3
	Privilege                            *table3.DXTable3
	RolePrivilege                        *table3.DXTable3
	UserRoleMembership                   *table3.DXTable3
	MenuItem                             *table3.DXTable3
	OnUserAfterCreate                    func(aepr *api.DXAPIEndPointRequest, dtx *database2.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserResetPassword                  func(aepr *api.DXAPIEndPointRequest, dtx *database2.DXDatabaseTx, user utils.JSON, userPassword string) (err error)
	OnUserRoleMembershipAfterCreate      func(aepr *api.DXAPIEndPointRequest, dtx *database2.DXDatabaseTx, userRoleMembership utils.JSON, organizationId int64) (err error)
	OnUserRoleMembershipBeforeSoftDelete func(aepr *api.DXAPIEndPointRequest, dtx *database2.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
	OnUserRoleMembershipBeforeHardDelete func(aepr *api.DXAPIEndPointRequest, dtx *database2.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
}

func (um *DxmUserManagement) Init(databaseNameId string) {
	um.DatabaseNameId = databaseNameId
	// NewDXTable3Simple(databaseNameId, tableName, listViewNameId, fieldNameForRowId, fieldNameForRowUid, fieldNameForRowNameId)
	um.User = table3.NewDXTable3Simple(databaseNameId, "user_management.user",
		"user_management.v_user", "id", "uid", "loginid")
	um.UserPassword = table3.NewDXTable3Simple(databaseNameId, "user_management.user_password",
		"user_management.user_password", "id", "uid", "")
	um.Role = table3.NewDXTable3Simple(databaseNameId, "user_management.role",
		"user_management.role", "id", "uid", "nameid")
	um.Role.FieldTypeMapping = map[string]string{
		"organization_types": "array-string",
	}
	um.Organization = table3.NewDXTable3Simple(databaseNameId, "user_management.organization",
		"user_management.organization", "id", "uid", "code")
	um.OrganizationRoles = table3.NewDXTable3Simple(databaseNameId, "user_management.organization_role",
		"user_management.v_organization_role", "id", "uid", "")
	um.UserOrganizationMembership = table3.NewDXTable3Simple(databaseNameId, "user_management.user_organization_membership",
		"user_management.v_user_organization_membership", "id", "uid", "")
	um.Privilege = table3.NewDXTable3Simple(databaseNameId, "user_management.privilege",
		"user_management.v_privilege", "id", "uid", "nameid")
	um.RolePrivilege = table3.NewDXTable3Simple(databaseNameId, "user_management.role_privilege",
		"user_management.v_role_privilege", "id", "uid", "")
	um.UserRoleMembership = table3.NewDXTable3Simple(databaseNameId, "user_management.user_role_membership",
		"user_management.v_user_role_membership", "id", "uid", "")
	um.MenuItem = table3.NewDXTable3Simple(databaseNameId, "user_management.menu_item",
		"user_management.v_menu_item", "id", "uid", "composite_nameid")
	um.UserMessageChannnelType = table3.NewDXRawTable3Simple(databaseNameId, "user_management.user_message_channel_type",
		"user_management.user_message_channel_type", "id", "uid", "nameid")
	um.UserMessageCategory = table3.NewDXRawTable3Simple(databaseNameId, "user_management.user_message_category",
		"user_management.user_message_category", "id", "uid", "nameid")
	um.UserMessage = table3.NewDXTable3Simple(databaseNameId, "user_management.user_message",
		"user_management.v_user_message", "id", "uid", "")
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
		func(dtx *database2.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err2 error) {
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
