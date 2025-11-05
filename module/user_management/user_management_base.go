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
	um.User = table.Manager.NewTable(databaseNameId, "user_management.user",
		"user_management.user",
		"user_management.v_user", "loginid", "id", "uid", "data")
	um.UserPassword = table.Manager.NewTable(databaseNameId, "user_management.user_password",
		"user_management.user_password",
		"user_management.user_password", "id", "id", "uid", "data")
	um.Role = table.Manager.NewTable(databaseNameId, "user_management.role",
		"user_management.role",
		"user_management.role", "nameid", "id", "uid", "data")
	um.Role.FieldTypeMapping = map[string]string{
		"organization_types": "array-string",
	}
	um.Organization = table.Manager.NewTable(databaseNameId, "user_management.organization",
		"user_management.organization",
		"user_management.organization", "code", "id", "uid", "data")
	um.OrganizationRoles = table.Manager.NewTable(databaseNameId, "user_management.organization_role",
		"user_management.organization_role",
		"user_management.v_organization_role", "id", "id", "uid", "data")
	um.UserOrganizationMembership = table.Manager.NewTable(databaseNameId, "user_management.user_organization_membership",
		"user_management.user_organization_membership",
		"user_management.v_user_organization_membership", "id", "id", "uid", "data")
	um.Privilege = table.Manager.NewTable(databaseNameId, "user_management.privilege",
		"user_management.privilege",
		"user_management.privilege", "nameid", "id", "uid", "data")
	um.RolePrivilege = table.Manager.NewTable(databaseNameId, "user_management.role_privilege",
		"user_management.role_privilege",
		"user_management.v_role_privilege", "id", "id", "uid", "data")
	um.UserRoleMembership = table.Manager.NewTable(databaseNameId, "user_management.user_role_membership",
		"user_management.user_role_membership",
		"user_management.v_user_role_membership", "id", "id", "uid", "data")
	um.MenuItem = table.Manager.NewTable(databaseNameId, "user_management.menu_item",
		"user_management.menu_item",
		"user_management.v_menu_item", "composite_nameid", "id", "uid", "data")
	um.UserMessage = table.Manager.NewTable(databaseNameId, "user_management.user_message",
		"user_management.user_message",
		"user_management.user_message", "id", "id", "uid", "data")
}

func (um *DxmUserManagement) UserMessageCreateAllApplication(l *log.DXLog, userId int64, templateTitle, templateBody string, templateData utils.JSON, attachedData map[string]string) (err error) {
	for key, value := range templateData {
		placeholder := fmt.Sprintf("<%s>", key)
		aValue := fmt.Sprintf("%v", value)
		templateBody = strings.ReplaceAll(templateBody, placeholder, aValue)
		templateTitle = strings.ReplaceAll(templateTitle, placeholder, aValue)
	}

	msgBody := templateBody
	msgTitle := templateTitle

	err = push_notification.ModulePushNotification.FCM.AllApplicationSendToUser(l, userId, msgTitle, msgBody, attachedData,
		func(dtx *database.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err2 error) {
			_, err2 = um.UserMessage.TxInsert(dtx, utils.JSON{
				"fcm_message_id":     fcmMessageId,
				"fcm_application_id": fcmApplicationId,
				"user_id":            userId,
				"title":              msgTitle,
				"body":               msgBody,
				"data":               attachedData,
			})
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
