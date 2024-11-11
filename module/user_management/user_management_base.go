package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
)

const (
	UserStatusActive = "ACTIVE"
	UserStatusLocked = "LOCKED"
)

type DxmUserManagement struct {
	dxlibModule.DXModule
	SessionRedis                         *redis.DXRedis
	PreKeyRedis                          *redis.DXRedis
	User                                 *table.DXTable
	UserPassword                         *table.DXTable
	Role                                 *table.DXTable
	Organization                         *table.DXTable
	OrganizationRoles                    *table.DXTable
	UserOrganizationMembership           *table.DXTable
	Privilege                            *table.DXTable
	RolePrivilege                        *table.DXTable
	UserRoleMembership                   *table.DXTable
	MenuItem                             *table.DXTable
	DatabaseNameId                       string
	OnUserAfterCreate                    func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, user utils.JSON) (err error)
	OnUserRoleMembershipAfterCreate      func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON, organizationId int64) (err error)
	OnUserRoleMembershipBeforeSoftDelete func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
	OnUserRoleMembershipBeforeHardDelete func(aepr *api.DXAPIEndPointRequest, dtx *database.DXDatabaseTx, userRoleMembership utils.JSON) (err error)
}

func (um *DxmUserManagement) Init(databaseNameId string) {
	um.DatabaseNameId = databaseNameId
	um.User = table.Manager.NewTable(databaseNameId, "user_management.user",
		"user_management.user",
		"user_management.v_user", `loginid`, `id`)
	um.UserPassword = table.Manager.NewTable(databaseNameId, "user_management.user_password",
		"user_management.user_password",
		"user_management.user_password", `id`, `id`)
	um.Role = table.Manager.NewTable(databaseNameId, "user_management.role",
		"user_management.role",
		"user_management.role", `nameid`, `id`)
	um.Organization = table.Manager.NewTable(databaseNameId, "user_management.organization",
		"user_management.organization",
		"user_management.organization", `code`, `id`)
	um.OrganizationRoles = table.Manager.NewTable(databaseNameId, "user_management.organization_roles",
		"user_management.organization_roles",
		"user_management.organization_roles", `id`, `id`)
	um.UserOrganizationMembership = table.Manager.NewTable(databaseNameId, "user_management.user_organization_membership",
		"user_management.user_organization_membership",
		"user_management.v_user_organization_membership", `id`, `id`)
	um.Privilege = table.Manager.NewTable(databaseNameId, "user_management.privilege",
		"user_management.privilege",
		"user_management.privilege", `nameid`, `id`)
	um.RolePrivilege = table.Manager.NewTable(databaseNameId, "user_management.role_privilege",
		"user_management.role_privilege",
		"user_management.v_role_privilege", `id`, `id`)
	um.UserRoleMembership = table.Manager.NewTable(databaseNameId, "user_management.user_role_membership",
		"user_management.user_role_membership",
		"user_management.v_user_role_membership", `id`, `id`)
	um.MenuItem = table.Manager.NewTable(databaseNameId, "user_management.menu_item",
		"user_management.menu_item",
		"user_management.v_menu_item", `composite_nameid`, `id`)
}

func (um *DxmUserManagement) RolePrivilegeTxInsert(dtx *database.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64, err error) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		return 0, err
	}
	privilegeId := privilege[`id`].(int64)
	id, err = um.RolePrivilege.TxInsert(dtx, utils.JSON{
		`role_id`:      roleId,
		`privilege_id`: privilegeId,
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (um *DxmUserManagement) RolePrivilegeTxMustInsert(dtx *database.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		dtx.Log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.Privilege.TxShouldGetByNameId`, err)
		return 0
	}
	privilegeId := privilege[`id`].(int64)
	id, err = um.RolePrivilege.TxInsert(dtx, utils.JSON{
		`role_id`:      roleId,
		`privilege_id`: privilegeId,
	})
	if err != nil {
		dtx.Log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.TxInsert`, err)
		return 0
	}
	return id
}

var ModuleUserManagement DxmUserManagement

func init() {
	ModuleUserManagement = DxmUserManagement{}
}
