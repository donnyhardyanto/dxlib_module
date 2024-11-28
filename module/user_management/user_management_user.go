package user_management

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/crypto/datablock"
	"github.com/donnyhardyanto/dxlib/utils/lv"
	security "github.com/donnyhardyanto/dxlib/utils/security"
	"github.com/teris-io/shortid"
	"math/rand"
	"net/http"
	"time"
)

func (um *DxmUserManagement) UserList(aepr *api.DXAPIEndPointRequest) (err error) {
	isExistFilterWhere, filterWhere, err := aepr.GetParameterValueAsString("filter_where")
	if err != nil {
		return err
	}
	if !isExistFilterWhere {
		filterWhere = ""
	}
	isExistFilterOrderBy, filterOrderBy, err := aepr.GetParameterValueAsString("filter_order_by")
	if err != nil {
		return err
	}
	if !isExistFilterOrderBy {
		filterOrderBy = ""
	}

	isExistFilterKeyValues, filterKeyValues, err := aepr.GetParameterValueAsJSON("filter_key_values")
	if err != nil {
		return err
	}
	if !isExistFilterKeyValues {
		filterKeyValues = nil
	}

	_, rowPerPage, err := aepr.GetParameterValueAsInt64("row_per_page")
	if err != nil {
		return err
	}

	_, pageIndex, err := aepr.GetParameterValueAsInt64("page_index")
	if err != nil {
		return err
	}

	_, isDeletedIncluded, err := aepr.GetParameterValueAsBool("is_deleted", false)
	if err != nil {
		return err
	}

	t := um.User
	if !isDeletedIncluded {
		if filterWhere != "" {
			filterWhere = fmt.Sprintf("(%s) and ", filterWhere)
		}

		switch t.Database.DatabaseType.String() {
		case "sqlserver":
			filterWhere = filterWhere + "(is_deleted=0)"
		case "postgres":
			filterWhere = filterWhere + "(is_deleted=false)"
		default:
			filterWhere = filterWhere + "(is_deleted=0)"
		}
	}

	if t.Database == nil {
		t.Database = database.Manager.Databases[t.DatabaseNameId]
	}

	if !t.Database.Connected {
		err := t.Database.Connect()
		if err != nil {
			aepr.Log.Errorf("error at reconnect db at table %s list (%s) ", t.NameId, err.Error())
			return err
		}
	}

	rowsInfo, list, totalRows, totalPage, _, err := db.NamedQueryPaging(t.Database.Connection, "", rowPerPage, pageIndex, "*", t.ListViewNameId,
		filterWhere, "", filterOrderBy, filterKeyValues)
	if err != nil {
		aepr.Log.Errorf("Error at paging table %s (%s) ", t.NameId, err.Error())
		return err
	}

	for i, row := range list {
		userId := row["id"].(int64)
		_, userOrganizationMemberships, err := um.UserOrganizationMembership.Select(&aepr.Log, nil, utils.JSON{
			"user_id": userId,
		}, nil, nil)
		if err != nil {
			return err
		}
		list[i]["organizations"] = userOrganizationMemberships
		_, userRoleMemberships, err := um.UserRoleMembership.Select(&aepr.Log, nil, utils.JSON{
			"user_id": userId,
		}, nil, nil)
		if err != nil {
			return err
		}
		list[i]["roles"] = userRoleMemberships
	}

	data := utils.JSON{
		"list": utils.JSON{
			"rows":       list,
			"total_rows": totalRows,
			"total_page": totalPage,
			"rows_info":  rowsInfo,
		},
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, data)
	return nil
}

func (um *DxmUserManagement) UserCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	organizationId, ok := aepr.ParameterValues["organization_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ORGANIZATION_ID_MISSING")
	}
	_, _, err = um.Organization.ShouldGetById(&aepr.Log, organizationId)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
	}

	roleId, ok := aepr.ParameterValues["role_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ROLE_ID_MISSING")
	}
	_, _, err = um.Role.ShouldGetById(&aepr.Log, roleId)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ROLE_NOT_FOUND")
	}

	passwordI, ok := aepr.ParameterValues["password_i"].Value.(string)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PASSWORD_PREKEY_INDEX_MISSING")
	}

	passwordD, ok := aepr.ParameterValues["password_d"].Value.(string)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "PASSWORD_DATA_BLOCK_MISSING")
	}

	lvPayloadElements, _, _, err := um.PreKeyUnpack(passwordI, passwordD)
	if err != nil {
		return err
	}

	lvPayloadPassword := lvPayloadElements[0]
	userPassword := string(lvPayloadPassword.Value)

	attribute, ok := aepr.ParameterValues[`attribute`].Value.(string)
	if !ok {
		attribute = ""
	}

	loginId := aepr.ParameterValues["loginid"].Value.(string)
	email := aepr.ParameterValues["email"].Value.(string)
	fullname := aepr.ParameterValues["fullname"].Value.(string)
	phonenumber := aepr.ParameterValues["phonenumber"].Value.(string)
	status := UserStatusActive

	p := utils.JSON{
		`loginid`:     loginId,
		`email`:       email,
		`fullname`:    fullname,
		`phonenumber`: phonenumber,
		`status`:      status,
		`attribute`:   attribute,
	}

	identityNumber, ok := aepr.ParameterValues[`identity_number`].Value.(string)
	if ok {
		p[`identity_number`] = identityNumber
	}

	identityType, ok := aepr.ParameterValues[`identity_type`].Value.(string)
	if ok {
		p[`identity_type`] = identityType
	}

	gender, ok := aepr.ParameterValues[`gender`].Value.(string)
	if ok {
		p[`gender`] = gender
	}

	addressOnIdentityCard, ok := aepr.ParameterValues[`gender`].Value.(string)
	if ok {
		p[`address_on_identity_card`] = addressOnIdentityCard
	}

	membershipNumber, ok := aepr.ParameterValues[`membership_number`].Value.(string)
	if !ok {
		membershipNumber = ""
	}

	var userId int64
	var userOrganizationMembershipId int64
	var userRoleMembershipId int64

	err = um.User.Database.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err2 error) {
		_, user, err2 := um.User.TxSelectOne(tx, utils.JSON{
			"loginid": loginId,
		}, nil)
		if err2 != nil {
			return err2
		}
		if user != nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "USER_ALREADY_EXISTS:%v", loginId)
		}
		userId, err2 = um.User.TxInsert(tx, p)
		if err2 != nil {
			return err2
		}

		userOrganizationMembershipId, err2 = um.UserOrganizationMembership.TxInsert(tx, map[string]any{
			"user_id":           userId,
			"organization_id":   organizationId,
			"membership_number": membershipNumber,
		})
		if err2 != nil {
			return err2
		}

		userRoleMembershipId, err2 = um.UserRoleMembership.TxInsert(tx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
			"role_id":         roleId,
		})
		if err2 != nil {
			return err2
		}

		err2 = um.TxUserPasswordCreate(tx, userId, userPassword)
		if err2 != nil {
			return err2
		}

		if um.OnUserAfterCreate != nil {
			_, user, err2 = um.User.TxSelectOne(tx, utils.JSON{
				"id": userId,
			}, nil)
			if err2 != nil {
				return err2
			}
			err2 = um.OnUserAfterCreate(aepr, tx, user, userPassword)
		}

		_, userRoleMembership, err := um.UserRoleMembership.TxSelectOne(tx, utils.JSON{
			`id`: userRoleMembershipId,
		}, nil)
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
		"id":                              userId,
		"user_organization_membership_id": userOrganizationMembershipId,
		"user_role_membership_id":         userRoleMembershipId,
	})

	return nil
}

func (um *DxmUserManagement) UserRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.RequestRead(aepr)
}

func (um *DxmUserManagement) UserEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	t := um.User
	_, id, err := aepr.GetParameterValueAsInt64(t.FieldNameForRowId)
	if err != nil {
		return err
	}

	_, newKeyValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return err
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

	err = t.Database.Tx(&aepr.Log, sql.LevelReadCommitted, func(dtx *database.DXDatabaseTx) (err2 error) {
		if len(newKeyValues) > 0 {
			_, err2 = um.User.TxUpdate(dtx, newKeyValues, utils.JSON{
				t.FieldNameForRowId: id,
			})
			if err2 != nil {
				return err2
			}
		}
		if len(p1) > 0 {
			_, err2 = um.UserOrganizationMembership.TxUpdate(dtx, p1, utils.JSON{
				"user_id": id,
			})
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
		t.FieldNameForRowId: id,
	})

	return nil

}

func (um *DxmUserManagement) UserDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")

	d := database.Manager.Databases[um.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {
		_, err = um.User.TxUpdate(tx, utils.JSON{
			"is_deleted": true,
			"status":     UserStatusDeleted,
		}, utils.JSON{
			"id": userId,
		})

		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (um *DxmUserManagement) UserSuspend(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")

	d := database.Manager.Databases[um.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {
		_, err = um.User.TxUpdate(tx, utils.JSON{
			"status": UserStatusSuspend,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (um *DxmUserManagement) UserActivate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")

	d := database.Manager.Databases[um.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {
		_, err = um.User.TxUpdate(tx, utils.JSON{
			"status": UserStatusActive,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (um *DxmUserManagement) UserUndelete(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")

	d := database.Manager.Databases[um.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {
		_, err = um.User.TxUpdate(tx, utils.JSON{
			"status": UserStatusActive,
		}, utils.JSON{
			"id":         userId,
			"is_deleted": false,
		})

		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (um *DxmUserManagement) UserPasswordTxCreate(tx *database.DXDatabaseTx, userId int64, password string) (err error) {
	hashedPasswordAsHexString, err := um.passwordHashCreate(password)
	if err != nil {
		return err
	}
	_, err = um.UserPassword.TxInsert(tx, utils.JSON{
		"user_id": userId,
		"value":   hashedPasswordAsHexString,
	})
	if err != nil {
		return err
	}
	return nil
}

func (um *DxmUserManagement) TxUserPasswordCreate(tx *database.DXDatabaseTx, userId int64, password string) (err error) {
	hashedPasswordAsHexString, err := um.passwordHashCreate(password)
	if err != nil {
		return err
	}
	_, err = um.UserPassword.TxInsert(tx, utils.JSON{
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
		return false, errors.New(`lvSeparateElements.IS_NIL`)
	}

	if len(lvSeparateElements) < 3 {
		return false, errors.New(`lvSeparateElements.IS_NOT_3`)
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
	_, userPasswordRow, err := um.UserPassword.SelectOne(l, utils.JSON{
		`user_id`: userId,
	}, map[string]string{"id": "DESC"})
	if err != nil {
		return false, err
	}
	if userPasswordRow == nil {
		return false, errors.New("userPasswordVerify:USER_PASSWORD_NOT_FOUND")
	}
	verificationResult, err = um.passwordHashVerify(tryPassword, userPasswordRow["value"].(string))
	if err != nil {
		return false, err
	}
	return verificationResult, nil
}

func (um *DxmUserManagement) PreKeyUnpack(preKeyIndex string, datablockAsString string) (lvPayloadElements []*lv.LV, sharedKey2AsBytes []byte, edB0PrivateKeyAsBytes []byte, err error) {
	if preKeyIndex == `` || datablockAsString == `` {
		return nil, nil, nil, errors.New(`PARAMETER_IS_EMPTY`)
	}

	preKeyData, err := um.PreKeyRedis.Get(preKeyIndex)
	if err != nil {
		return nil, nil, nil, err
	}
	if preKeyData == nil {
		return nil, nil, nil, errors.New(`PREKEY_NOT_FOUND`)
	}

	sharedKey1AsHexString := preKeyData[`shared_key_1`].(string)
	sharedKey2AsHexString := preKeyData[`shared_key_2`].(string)
	edA0PublicKeyAsHexString := preKeyData[`a0_public_key`].(string)
	edB0PrivateKeyAsHexString := preKeyData[`b0_private_key`].(string)

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

func generateRandomString(n int) string {
	const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[r.Intn(len(letterBytes))]
	}
	return string(b)
}

func (um *DxmUserManagement) UserResetPassword(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userId, err := aepr.GetParameterValueAsInt64("user_id")
	_, user, err := um.User.SelectOne(&aepr.Log, utils.JSON{
		`id`: userId,
	}, nil)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("USER_NOT_FOUND")
	}

	userPasswordNew := generateRandomString(10)

	d := database.Manager.Databases[um.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {

		err = um.UserPasswordTxCreate(tx, userId, userPasswordNew)
		if err != nil {
			return err
		}
		aepr.Log.Infof("User password changed")

		_, err = um.User.TxUpdate(tx, utils.JSON{
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
