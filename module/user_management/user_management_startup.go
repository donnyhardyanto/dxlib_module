package user_management

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/databases"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (um *DxmUserManagement) AutoCreateUserSuperAdminPasswordIfNotExist(l *dxlibLog.DXLog) (err error) {
	err = databases.Manager.GetOrCreate(um.DatabaseNameId).Tx(context.Background(), l, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err error) {

		_, userSuperAdmin, err := um.User.TxSelectOne(tx, nil, utils.JSON{
			"loginid": "superadmin",
		}, nil, nil, nil)
		if err != nil {
			l.Errorf(err, "Failed to check superadmin user: %s", err.Error())
			return err
		}
		if userSuperAdmin == nil {
			err = l.ErrorAndCreateErrorf("Superadmin user not found")
			return err
		}
		userSuperAdminId, ok := userSuperAdmin["id"].(int64)
		if !ok {
			return fmt.Errorf("superadmin user 'id' is missing or not an int64")
		}
		_, userPassword, err := um.UserPassword.TxSelectOne(tx, nil, utils.JSON{
			"user_id": userSuperAdmin["id"],
		}, nil, nil, nil)
		if err != nil {
			l.Errorf(err, "Failed to check superadmin user password: %s", err.Error())
			return err
		}
		if userPassword != nil {
			return nil
		}

		// if define in vault, use it
		s := app.App.InitVault.GetStringOrDefault(context.Background(), "SUPERADMIN_INITIAL_PASSWORD", "")
		if s != "" {
			err = um.TxUserPasswordCreate(tx, userSuperAdminId, s)
			if err != nil {
				l.Errorf(err, "Failed to insert superadmin user password: %s", err.Error())
				return err
			}
			l.Warn("Superadmin password has been set")
			return nil
		}

		// if not define in vault, input from user
		var userInputPassword1 string
		var userInputPassword2 string
		l.Warnf("No superadmin password found. Regenerating new one, input new password:")
		_, err = fmt.Scanln(&userInputPassword1)
		if err != nil {
			l.Errorf(err, "Failed to input password: %s", err.Error())
			return err
		}
		l.Warnf("Input the password again to confirm:")
		_, err = fmt.Scanln(&userInputPassword2)
		if err != nil {
			l.Errorf(err, "Failed to input password again: %s", err.Error())
			return err
		}
		if userInputPassword1 != userInputPassword2 {
			err := l.ErrorAndCreateErrorf("Password mismatch")
			return err
		}

		err = um.TxUserPasswordCreate(tx, userSuperAdminId, userInputPassword1)

		if err != nil {
			l.Errorf(err, "Failed to insert superadmin user password: %s", err.Error())
			return err
		}
		l.Warn("Superadmin password has been set")
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
