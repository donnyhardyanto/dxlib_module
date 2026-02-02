package user_management

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) RolePrivilegeList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestPagingList(aepr)
}

func (um *DxmUserManagement) RolePrivilegeCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, roleId, err := aepr.GetParameterValueAsString("role_id")
	if err != nil {
		return err
	}
	_, privilegeId, err := aepr.GetParameterValueAsString("privilege_id")
	if err != nil {
		return err
	}
	_, err = um.RolePrivilege.DoCreate(aepr, map[string]any{
		"role_id":      roleId,
		"privilege_id": privilegeId,
	})
	return err
}

func (um *DxmUserManagement) RolePrivilegeDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.RolePrivilege.RequestHardDelete(aepr)
}

func (um *DxmUserManagement) RolePrivilegeTxInsert(dtx *databases.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64, err error) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		return 0, err
	}
	privilegeId, ok := privilege["id"].(int64)
	if !ok {
		return 0, fmt.Errorf("privilege 'id' is missing or not an int64 for privilege_name_id=%s", privilegeNameId)
	}
	id, err = um.RolePrivilege.TxInsertReturningId(dtx, utils.JSON{
		"role_id":      roleId,
		"privilege_id": privilegeId,
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (um *DxmUserManagement) RolePrivilegeTxMustInsert(dtx *databases.DXDatabaseTx, roleId int64, privilegeNameId string) (id int64) {
	_, privilege, err := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
	if err != nil {
		dtx.Log.Panic("RolePrivilegeTxMustInsert | DxmUserManagement.Privilege.TxShouldGetByNameId", err)
		return 0
	}
	privilegeId, ok := privilege["id"].(int64)
	if !ok {
		dtx.Log.Panic("RolePrivilegeTxMustInsert | privilege 'id' is missing or not an int64", fmt.Errorf("privilege_name_id=%s", privilegeNameId))
		return 0
	}
	id, err = um.RolePrivilege.TxInsertReturningId(dtx, utils.JSON{
		"role_id":      roleId,
		"privilege_id": privilegeId,
	})
	if err != nil {
		dtx.Log.Panic("RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.TxInsert", err)
		return 0
	}
	return id
}

func (um *DxmUserManagement) RolePrivilegeSxMustInsert(log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	err := databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) (err2 error) {
		_, privilege, err2 := um.Privilege.TxShouldGetByNameId(dtx, privilegeNameId)
		if err2 != nil {
			return err2
		}
		privilegeId, ok := privilege["id"].(int64)
		if !ok {
			return fmt.Errorf("privilege 'id' is missing or not an int64 for privilege_name_id=%s", privilegeNameId)
		}
		id, err2 = um.RolePrivilege.TxInsertReturningId(dtx, utils.JSON{
			"role_id":      roleId,
			"privilege_id": privilegeId,
		})
		if err2 != nil {
			return err2
		}

		return nil
	})
	if err != nil {
		log.Panic("RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert", err)
	}

	return id
}

func (um *DxmUserManagement) RolePrivilegeMustInsert(log *log.DXLog, roleId int64, privilegeNameId string) (id int64) {
	var err error
	defer func() {
		if err != nil {
			log.Panic("RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert", err)
		}
	}()

	_, privilege, err := um.Privilege.ShouldGetByNameId(log, privilegeNameId)
	if err != nil {
		return 0
	}

	privilegeId, ok := privilege["id"].(int64)
	if !ok {
		err = fmt.Errorf("privilege 'id' is missing or not an int64 for privilege_name_id=%s", privilegeNameId)
		return 0
	}

	id, err = um.RolePrivilege.InsertReturningId(log, utils.JSON{
		"role_id":      roleId,
		"privilege_id": privilegeId,
	})
	if err != nil {
		log.Error(err.Error(), err)

		return 0
	}

	log.Debugf(
		"RolePrivilegeMustInsert | role_id:%d, privilege_id:%d, privilege_name_id:%s",
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
	db := databases.Manager.GetOrCreate(um.DatabaseNameId)

	go func(aroleId int64, aprivilegeNameId string) {
		var err error

		db.ConcurrencySemaphore <- struct{}{}
		defer func() {
			// Release semaphore
			<-db.ConcurrencySemaphore
			wg.Done()
			if err != nil {
				alog.Panic("RolePrivilegeTxMustInsert | DxmUserManagement.RolePrivilege.RolePrivilegeSxMustInsert", err)
			}
		}()

		um.RolePrivilegeMustInsert(alog, roleId, privilegeNameId)
	}(roleId, privilegeNameId)
	return 0
}
