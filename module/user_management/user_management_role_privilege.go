package user_management

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"sync"
)

func (um *DxmUserManagement) RolePrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestPagingList(aepr)
}

func (um *DxmUserManagement) RolePrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = um.RolePrivilege.DoCreate(aepr, map[string]any{
		`role_id`:      aepr.ParameterValues[`role_id`].Value.(string),
		`privilege_id`: aepr.ParameterValues[`privilege_id`].Value.(string),
	})
	return err
}

func (um *DxmUserManagement) RolePrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestSoftDelete(aepr)
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

func (um *DxmUserManagement) RolePrivilegeSxMustInsert(log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	d := database.Manager.Databases[um.DatabaseNameId]
	err := d.Tx(log, database.LevelReadCommitted, func(dtx *database.DXDatabaseTx) (err2 error) {
		_, privilege, err2 := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
		if err2 != nil {
			return err2
		}
		privilegeId := privilege[`id`].(int64)
		id, err2 = um.RolePrivilege.TxInsert(dtx, utils.JSON{
			`role_id`:      roleId,
			`privilege_id`: privilegeId,
		})
		if err2 != nil {
			return err2
		}

		return nil
	})
	if err != nil {
		log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert`, err)
	}

	return id
}

func (um *DxmUserManagement) RolePrivilegeMustInsert(log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	var err error
	defer func() {
		if err != nil {
			log.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert`, err)
		}
	}()

	_, privilege, err := um.Privilege.ShouldGetByNameId(log, privilegeNameId)
	if err != nil {
		return 0
	}

	privilegeId := privilege[`id`].(int64)

	id, err = um.RolePrivilege.Insert(log, utils.JSON{
		`role_id`:      roleId,
		`privilege_id`: privilegeId,
	})
	if err != nil {
		return 0
	}

	log.Debugf(
		`RolePrivilegeMustInsert | role_id:%d, privilege_id:%d, privilege_name_id:%s`,
		roleId,
		privilegeId,
		privilegeNameId)
	return id
}

func (um *DxmUserManagement) RolePrivilegeWgMustInsert(wg *sync.WaitGroup, log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	wg.Add(1)
	aLog := log
	go func() {
		um.RolePrivilegeMustInsert(aLog, roleId, privilegeNameId)
		wg.Done()
	}()
	return 0
}

func (um *DxmUserManagement) RolePrivilegeSWgMustInsert(wg *sync.WaitGroup, log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	wg.Add(1)
	alog := log
	d := database.Manager.Databases[um.DatabaseNameId]

	go func(aroleId int64, aprivilegeNameId string) {
		var err error

		d.ConcurrencySemaphore <- struct{}{}
		defer func() {
			// Release semaphore
			<-d.ConcurrencySemaphore
			wg.Done()
			if err != nil {
				alog.Panic(`RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert`, err)
			}
		}()

		um.RolePrivilegeMustInsert(alog, roleId, privilegeNameId)
	}(roleId, privilegeNameId)
	return 0
}
