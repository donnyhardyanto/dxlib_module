package user_management

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/crypto/datablock"
	"github.com/donnyhardyanto/dxlib/utils/lv"
	security "github.com/donnyhardyanto/dxlib/utils/security"
	"github.com/teris-io/shortid"
	"net/http"
)

func (um *DxmUserManagement) UserList(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.List(aepr)
}

func (um *DxmUserManagement) UserCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	organizationId, ok := aepr.ParameterValues["organization_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ORGANIZATION_ID_MISSING")
	}

	_, _, err = um.Organization.ShouldGetById(&aepr.Log, organizationId)
	if err != nil {
		return err
	}

	roleId, ok := aepr.ParameterValues["role_id"].Value.(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "ROLE_ID_MISSING")
	}

	_, _, err = um.Role.ShouldGetById(&aepr.Log, roleId)
	if err != nil {
		return err
	}

	attribute, ok := aepr.ParameterValues[`attribute`].Value.(string)
	if !ok {
		attribute = ""
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

	loginId := aepr.ParameterValues["loginid"].Value.(string)

	var userId int64
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
		userId, err2 = um.User.TxInsert(tx, map[string]any{
			`loginid`:     loginId,
			`email`:       aepr.ParameterValues[`email`].Value.(string),
			`fullname`:    aepr.ParameterValues[`fullname`].Value.(string),
			`phonenumber`: aepr.ParameterValues[`phonenumber`].Value.(string),
			`status`:      aepr.ParameterValues[`status`].Value.(string),
			`attribute`:   attribute,
		})
		if err2 != nil {
			return err2
		}

		_, err2 = um.UserOrganizationMembership.TxInsert(tx, map[string]any{
			"user_id":         userId,
			"organization_id": organizationId,
		})
		if err2 != nil {
			return err2
		}

		_, err2 = um.UserRoleMembership.TxInsert(tx, map[string]any{
			"user_id": userId,
			"role_id": roleId,
		})
		if err2 != nil {
			return err2
		}

		err2 = um.TxUserPasswordCreate(tx, userId, userPassword)
		if err2 != nil {
			return err2
		}

		return nil
	})

	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		um.User.FieldNameForRowId: userId,
	})

	fmt.Println(err)

	return nil

}

func (um *DxmUserManagement) UserRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.Read(aepr)
}

func (um *DxmUserManagement) UserEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.Edit(aepr)
}

func (um *DxmUserManagement) UserDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return um.User.SoftDelete(aepr)
}

func (um *DxmUserManagement) UserPasswordCreate(userId int64, password string) (err error) {
	l := dxlibLog.Log

	hashedPasswordAsHexString, err := um.passwordHashCreate(password)
	if err != nil {
		return err
	}
	_, err = um.UserPassword.Insert(&l, utils.JSON{
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
