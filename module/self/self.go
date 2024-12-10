package self

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/donnyhardyanto/dxlib/captcha"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib_module/module/push_notification"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/donnyhardyanto/dxlib/api"
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/crypto/datablock"
	"github.com/donnyhardyanto/dxlib/utils/crypto/x25519"
	utilsJSON "github.com/donnyhardyanto/dxlib/utils/json"
	"github.com/donnyhardyanto/dxlib/utils/lv"
	"github.com/donnyhardyanto/dxlib_module/lib"
	"github.com/donnyhardyanto/dxlib_module/module/general"
	"github.com/donnyhardyanto/dxlib_module/module/user_management"
	"github.com/google/uuid"
	"golang.org/x/crypto/ed25519"
)

type DxmSelf struct {
	dxlibModule.DXModule
	UserOrganizationMembershipType user_management.UserOrganizationMembershipType
	Avatar                         *lib.ImageObjectStorage
	OnAuthenticateUser             func(aepr *api.DXAPIEndPointRequest, loginId string, password string, organizationUid string) (isSuccess bool, user utils.JSON, organizations []utils.JSON, err error)
	OnCreateSessionObject          func(aepr *api.DXAPIEndPointRequest, user utils.JSON, originalSessionObject utils.JSON) (newSessionObject utils.JSON, err error)
}

func (s *DxmSelf) Init(databaseNameId string) {
	s.DatabaseNameId = databaseNameId
}

/*
  - Hash password
    Stored Data Format:
    	LV(LV(SALT),LV(SALT_METHOD),LV(HASHED_PASSWORD_BLOCK)).HEX_STRING
    	HASHED_PASSWORD_BLOCK = HASH(SALT_METHOD,PASSWORD_BLOCK)
    	PASSWORD_BLOCK=APPEND(SALT,SALT_METHOD,PASSWORD)
    	SALT_METHOD=1:SHA512,2:bcrypt
*/

func (s *DxmSelf) SelfPrelogin(aepr *api.DXAPIEndPointRequest) (err error) {
	_, edA0PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a0`)
	if err != nil {
		return err
	}

	_, ecdhA1PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a1`)
	if err != nil {
		return err
	}
	_, ecdhA2PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a2`)
	if err != nil {
		return err
	}

	if edA0PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ED_A0_PUBLIC_KEY`)
	}
	if ecdhA1PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ECDH_A1_PUBLIC_KEY`)
	}
	if ecdhA2PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ECDH_A2_PUBLIC_KEY`)
	}

	ecdhA1PublicKeyAsBytes, err := hex.DecodeString(ecdhA1PublicKeyAsHexString)
	if err != nil {
		return err
	}
	ecdhA2PublicKeyAsByes, err := hex.DecodeString(ecdhA2PublicKeyAsHexString)
	if err != nil {
		return err
	}

	edB0PublicKeyAsBytes, edB0PrivateKeyAsBytes, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	ecdhB1PublicKeyAsBytes, ecdhB1PrivateKeyAsBytes, err := x25519.GenerateKeyPair()
	if err != nil {
		return err
	}
	ecdhB2PublicKeyAsBytes, ecdhB2PrivateKeyAsBytes, err := x25519.GenerateKeyPair()
	if err != nil {
		return err
	}
	edB0PublicKeyAsHexString := hex.EncodeToString(edB0PublicKeyAsBytes[:])
	edB0PrivateKeyAsHexString := hex.EncodeToString(edB0PrivateKeyAsBytes[:])
	ecdhB1PublicKeyAsHexString := hex.EncodeToString(ecdhB1PublicKeyAsBytes[:])
	ecdhB1PrivateKeyAsHexString := hex.EncodeToString(ecdhB1PrivateKeyAsBytes[:])
	ecdhB2PublicKeyAsHexString := hex.EncodeToString(ecdhB2PublicKeyAsBytes[:])
	ecdhB2PrivateKeyAsHexString := hex.EncodeToString(ecdhB2PrivateKeyAsBytes[:])

	sharedKey1AsBytes, err := x25519.ComputeSharedSecret(ecdhB1PrivateKeyAsBytes[:], ecdhA1PublicKeyAsBytes)
	if err != nil {
		return err
	}
	sharedKey1AsHexString := hex.EncodeToString(sharedKey1AsBytes)

	sharedKey2AsBytes, err := x25519.ComputeSharedSecret(ecdhB2PrivateKeyAsBytes[:], ecdhA2PublicKeyAsByes)
	if err != nil {
		return err
	}
	sharedKey2AsHexString := hex.EncodeToString(sharedKey2AsBytes)

	uuidA, err := uuid.NewV7()
	if err != nil {
		return err
	}
	preKeyString := `PREKEY_` + uuidA.String()

	preKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `PREKEY_TTL_SECOND`)
	if err != nil {
		return err
	}
	preKeyTTLAsDuration := time.Duration(preKeyTTLAsInt) * time.Second
	err = user_management.ModuleUserManagement.PreKeyRedis.Set(preKeyString, utils.JSON{
		"shared_key_1":   sharedKey1AsHexString,
		"shared_key_2":   sharedKey2AsHexString,
		"a0_public_key":  edA0PublicKeyAsHexString,
		"a1_public_key":  ecdhA1PublicKeyAsHexString,
		"a2_public_key":  ecdhA2PublicKeyAsHexString,
		"b0_public_key":  edB0PublicKeyAsHexString,
		"b0_private_key": edB0PrivateKeyAsHexString,
		"b1_public_key":  ecdhB1PublicKeyAsHexString,
		"b1_private_key": ecdhB1PrivateKeyAsHexString,
		"b2_public_key":  ecdhB2PublicKeyAsHexString,
		"b2_private_key": ecdhB2PrivateKeyAsHexString,
	}, preKeyTTLAsDuration)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"i":  preKeyString,
		"b0": edB0PublicKeyAsHexString,
		"b1": ecdhB1PublicKeyAsHexString,
		"b2": ecdhB2PublicKeyAsHexString,
	})
	return nil
}

func (s *DxmSelf) SelfPreloginCaptcha(aepr *api.DXAPIEndPointRequest) (err error) {
	_, edA0PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a0`)
	if err != nil {
		return err
	}

	_, ecdhA1PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a1`)
	if err != nil {
		return err
	}
	_, ecdhA2PublicKeyAsHexString, err := aepr.GetParameterValueAsString(`a2`)
	if err != nil {
		return err
	}

	if edA0PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ED_A0_PUBLIC_KEY`)
	}
	if ecdhA1PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ECDH_A1_PUBLIC_KEY`)
	}
	if ecdhA2PublicKeyAsHexString == `` {
		return aepr.WriteResponseAndNewErrorf(400, `PARAMETER_IS_EMPTY:ECDH_A2_PUBLIC_KEY`)
	}

	ecdhA1PublicKeyAsBytes, err := hex.DecodeString(ecdhA1PublicKeyAsHexString)
	if err != nil {
		return err
	}
	ecdhA2PublicKeyAsByes, err := hex.DecodeString(ecdhA2PublicKeyAsHexString)
	if err != nil {
		return err
	}

	edB0PublicKeyAsBytes, edB0PrivateKeyAsBytes, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	ecdhB1PublicKeyAsBytes, ecdhB1PrivateKeyAsBytes, err := x25519.GenerateKeyPair()
	if err != nil {
		return err
	}
	ecdhB2PublicKeyAsBytes, ecdhB2PrivateKeyAsBytes, err := x25519.GenerateKeyPair()
	if err != nil {
		return err
	}
	edB0PublicKeyAsHexString := hex.EncodeToString(edB0PublicKeyAsBytes[:])
	edB0PrivateKeyAsHexString := hex.EncodeToString(edB0PrivateKeyAsBytes[:])
	ecdhB1PublicKeyAsHexString := hex.EncodeToString(ecdhB1PublicKeyAsBytes[:])
	ecdhB1PrivateKeyAsHexString := hex.EncodeToString(ecdhB1PrivateKeyAsBytes[:])
	ecdhB2PublicKeyAsHexString := hex.EncodeToString(ecdhB2PublicKeyAsBytes[:])
	ecdhB2PrivateKeyAsHexString := hex.EncodeToString(ecdhB2PrivateKeyAsBytes[:])

	sharedKey1AsBytes, err := x25519.ComputeSharedSecret(ecdhB1PrivateKeyAsBytes[:], ecdhA1PublicKeyAsBytes)
	if err != nil {
		return err
	}
	sharedKey1AsHexString := hex.EncodeToString(sharedKey1AsBytes)

	sharedKey2AsBytes, err := x25519.ComputeSharedSecret(ecdhB2PrivateKeyAsBytes[:], ecdhA2PublicKeyAsByes)
	if err != nil {
		return err
	}
	sharedKey2AsHexString := hex.EncodeToString(sharedKey2AsBytes)

	captcha := captcha.NewCaptcha()
	captchaID, captchaText := captcha.GenerateID()

	uuidA, err := uuid.NewV7()
	if err != nil {
		return err
	}
	preKeyString := `PREKEY_` + uuidA.String()

	preKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `PREKEY_TTL_SECOND`)
	if err != nil {
		return err
	}
	preKeyTTLAsDuration := time.Duration(preKeyTTLAsInt) * time.Second
	err = user_management.ModuleUserManagement.PreKeyRedis.Set(preKeyString, utils.JSON{
		"shared_key_1":   sharedKey1AsHexString,
		"shared_key_2":   sharedKey2AsHexString,
		"a0_public_key":  edA0PublicKeyAsHexString,
		"a1_public_key":  ecdhA1PublicKeyAsHexString,
		"a2_public_key":  ecdhA2PublicKeyAsHexString,
		"b0_public_key":  edB0PublicKeyAsHexString,
		"b0_private_key": edB0PrivateKeyAsHexString,
		"b1_public_key":  ecdhB1PublicKeyAsHexString,
		"b1_private_key": ecdhB1PrivateKeyAsHexString,
		"b2_public_key":  ecdhB2PublicKeyAsHexString,
		"b2_private_key": ecdhB2PrivateKeyAsHexString,
		"captcha_id":     captchaID,
		"captcha_text":   captchaText,
	}, preKeyTTLAsDuration)
	if err != nil {
		return err
	}

	r := utils.JSON{
		"i":  preKeyString,
		"b0": edB0PublicKeyAsHexString,
		"b1": ecdhB1PublicKeyAsHexString,
		"b2": ecdhB2PublicKeyAsHexString,
		"c1": captchaID,
		"d1": preKeyTTLAsInt,
	}
	rAsBytes, err := json.Marshal(r)
	if err != nil {
		return err
	}
	xVarHeaderValue := string(rAsBytes)

	img, err := captcha.GenerateImage(captchaText)

	aepr.WriteResponseAsBytes(http.StatusOK, map[string]string{
		"X-Var":               xVarHeaderValue,
		"Content-Type":        "image/png",
		"Content-Length":      strconv.Itoa(len(img)),
		"Content-Disposition": `attachment; filename="captcha.png"`,
	}, img)
	return nil
}

func isMenuItemExists(menu []utils.JSON, aMenuItem utils.JSON) bool {
	aMenuItemId := aMenuItem["id"]
	for _, item := range menu {
		if item["id"] == aMenuItemId {
			return true
		}
	}
	return false
}

func (s *DxmSelf) menuItemCheckParentMenuRecursively(l *dxlibLog.DXLog, menuitem utils.JSON, menu *[]utils.JSON) error {
	if menuitem == nil {
		return nil
	}
	parentId := menuitem[`parent_id`]
	if parentId != nil {
		_, parentMenuItem, err := user_management.ModuleUserManagement.MenuItem.SelectOne(l, utils.JSON{
			"id": parentId,
		}, map[string]string{"id": "ASC"})
		if err != nil {
			return err
		}
		if parentMenuItem != nil {
			isMenuItemExists := isMenuItemExists(*menu, parentMenuItem)
			if !isMenuItemExists {
				err = s.menuItemCheckParentMenuRecursively(l, parentMenuItem, menu)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type MenuItem struct {
	ID       int64       // Assuming ID is of type int64
	ParentID *int64      // Assuming ParentID is a pointer to int64 to allow nil
	Data     utils.JSON  // Any additional data for the menu item
	Children []*MenuItem // Children menu items
}

func setParentMenuItemAllowed(allMenuItem *map[int64]utils.JSON, menuItem *utils.JSON) {
	if menuItem == nil {
		return
	}
	parentID, ok := (*menuItem)[`parent_id`].(int64)
	if !ok {
		return
	}
	parentMenuItem, exists := (*allMenuItem)[parentID]
	if !exists {
		return
	}
	parentMenuItem[`allowed`] = true
	setParentMenuItemAllowed(allMenuItem, &parentMenuItem)
}

// pruneMenuItems recursively prunes the menu items that are not allowed
func pruneMenuItems(menuItem *utils.JSON) {
	children := (*menuItem)[`children`].(map[int64]*utils.JSON)
	for id, childMenuItemPtr := range children {
		childMenuItem := *childMenuItemPtr
		if !childMenuItem[`allowed`].(bool) {
			delete(children, id)
		} else {
			pruneMenuItems(&childMenuItem)
		}
	}
}

func (s *DxmSelf) fetchMenuTree(l *dxlibLog.DXLog, userEffectivePrivilegeIds map[string]int64) ([]*utils.JSON, error) {
	// select all menu items available
	allMenuItems := map[int64]utils.JSON{}
	_, menuItems, err := user_management.ModuleUserManagement.MenuItem.Select(l, nil, nil, map[string]string{"id": "ASC"}, nil)
	if err != nil {
		return nil, err
	}
	for _, menuItem := range menuItems {
		allMenuItems[menuItem[`id`].(int64)] = menuItem
	}

	// Build the complete menu tree
	var roots []*utils.JSON
	for _, menuItem := range allMenuItems {
		menuItemIndex := menuItem[`item_index`].(int64)
		if menuItem[`children`] == nil {
			menuItem[`children`] = map[int64]*utils.JSON{}
		}
		if menuItem[`parent_id`] != nil {
			parentId := menuItem[`parent_id`].(int64)
			parentMenuItem := allMenuItems[parentId]
			//			menuItem[`parent_menu_item`] = &parentMenuItem
			menuItem[`allowed`] = false
			if parentMenuItem[`children`] == nil {
				parentMenuItem[`children`] = map[int64]*utils.JSON{}
			}
			parentMenuItem[`children`].(map[int64]*utils.JSON)[menuItemIndex] = &menuItem
		} else {
			roots = append(roots, &menuItem)
		}
	}

	// only keep menu items that the user has access to
	for _, privilegeId := range userEffectivePrivilegeIds {
		for _, menuItem := range allMenuItems {
			if menuItem[`privilege_id`] == privilegeId {
				menuItem[`allowed`] = true
				setParentMenuItemAllowed(&allMenuItems, &menuItem)
				continue
			}
		}
	}

	// prune from allMenuItems the menu items that are not allowed
	for _, menuItemPtr := range roots {
		menuItem := *menuItemPtr
		pruneMenuItems(&menuItem)
	}

	// sort the children of each menu item by menuItem[item_index]
	for _, menuItemPtr := range roots {
		menuItem := *menuItemPtr
		children := menuItem[`children`].(map[int64]*utils.JSON)
		sortedChildren := make([]*utils.JSON, 0, len(children))
		for _, childMenuItemPtr := range children {
			childMenuItem := *childMenuItemPtr
			//			delete(childMenuItem, `parent_menu_item`)
			sortedChildren = append(sortedChildren, &childMenuItem)
		}
		// Sort the slice based on item_index
		sort.Slice(sortedChildren, func(i, j int) bool {
			return (*sortedChildren[i])[`item_index`].(int64) < (*sortedChildren[j])[`item_index`].(int64)
		})
		menuItem[`children`] = sortedChildren
	}
	return roots, nil
}

func (s *DxmSelf) SelfLogin(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString(`i`)
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString(`d`)
	if err != nil {
		return err
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, err := user_management.ModuleUserManagement.PreKeyUnpack(preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `UNPACK_ERROR:%v`, err.Error())
	}

	lvPayloadLoginId := lvPayloadElements[0]
	lvPayloadPassword := lvPayloadElements[1]

	organizationUId := ``
	userLoginId := string(lvPayloadLoginId.Value)
	userPassword := string(lvPayloadPassword.Value)
	if len(lvPayloadElements) > 2 {
		lvPayloadOrganizationUId := lvPayloadElements[2]
		organizationUId = string(lvPayloadOrganizationUId.Value)
	}

	var user utils.JSON
	var userOrganizationMemberships []utils.JSON
	var userLoggedOrganizationId int64
	var userLoggedOrganizationUid string
	var userLoggedOrganization utils.JSON
	var verificationResult bool
	if s.OnAuthenticateUser != nil {
		verificationResult, user, userOrganizationMemberships, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}
	} else {
		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, utils.JSON{
			`loginid`: userLoginId,
		}, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}

		userId := user[`id`].(int64)

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != `` {
			us[`organization_uid`] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us,
			map[string]string{"order_index": "asc"}, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}

		userLoggedOrganizationId = userOrganizationMemberships[0][`organization_id`].(int64)
		userLoggedOrganizationUid = userOrganizationMemberships[0][`organization_uid`].(string)

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}
	}

	a, err := uuid.NewV7()
	if err != nil {
		return err
	}
	b, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	c := a.String() + b.String()
	sessionKey := strings.ReplaceAll(c, "-", "")

	userId, ok := user[`id`].(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(500, `SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER`)
	}
	_, userRoleMemberships, err := user_management.ModuleUserManagement.UserRoleMembership.Select(&aepr.Log, nil, utils.JSON{
		"user_id": userId,
	}, map[string]string{"id": "ASC"}, nil)
	if err != nil {
		return err
	}

	userEffectivePrivilegeIds := map[string]int64{}
	for _, roleMembership := range userRoleMemberships {
		_, rolePrivileges, err := user_management.ModuleUserManagement.RolePrivilege.Select(&aepr.Log, nil, utils.JSON{
			"role_id": roleMembership[`role_id`],
		}, nil, nil)
		if err != nil {
			return err
		}
		for _, v1 := range rolePrivileges {
			privilegeNameId := v1[`privilege_nameid`].(string)
			privilegeId := v1[`privilege_id`].(int64)
			if privilegeNameId == `EVERYTHING` {
				_, rolePrivileges, err := user_management.ModuleUserManagement.Privilege.Select(&aepr.Log, nil, nil, nil, nil)
				if err != nil {
					return err
				}
				for _, v2 := range rolePrivileges {
					privilegeNameId := v2[`nameid`].(string)
					privilegeId := v2[`id`].(int64)
					if privilegeNameId != `EVERYTHING` {
						_, exists := userEffectivePrivilegeIds[privilegeNameId]
						if !exists {
							userEffectivePrivilegeIds[privilegeNameId] = privilegeId
						}
					}

				}
			} else {
				_, exists := userEffectivePrivilegeIds[privilegeNameId]
				if !exists {
					userEffectivePrivilegeIds[privilegeNameId] = privilegeId
				}
			}
		}
	}

	menuTreeRoot, err := s.fetchMenuTree(&aepr.Log, userEffectivePrivilegeIds)
	if err != nil {
		return err
	}

	sessionObject := utils.JSON{
		`session_key`:                   sessionKey,
		`user_id`:                       user[`id`],
		`user`:                          user,
		"organization_id":               userLoggedOrganizationId,
		"organization_uid":              userLoggedOrganizationUid,
		"organization":                  userLoggedOrganization,
		`user_organization_memberships`: userOrganizationMemberships,
		`user_role_memberships`:         userRoleMemberships,
		`user_effective_privilege_ids`:  userEffectivePrivilegeIds,
		`menu_tree_root`:                menuTreeRoot,
	}

	if s.OnCreateSessionObject != nil {
		sessionObject, err = s.OnCreateSessionObject(aepr, user, sessionObject)
		if err != nil {
			return err
		}
	}
	sessionKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `SESSION_TTL_SECOND`)
	if err != nil {
		return err
	}
	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	err = user_management.ModuleUserManagement.SessionRedis.Set(sessionKey, sessionObject, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}

	sessionObjectJSON, err := json.Marshal(sessionObject)
	if err != nil {
		return err
	}

	sessionObjectJSONString := string(sessionObjectJSON)

	lvSessionObject, err := lv.NewLV([]byte(sessionObjectJSONString))
	if err != nil {
		return err
	}
	dataBlockEnvelopeAsHexString, err := datablock.PackLVPayload(preKeyIndex, edB0PrivateKeyAsBytes, sharedKey2AsBytes, lvSessionObject)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})
	return err
}

func (s *DxmSelf) SelfLoginCaptcha(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString(`i`)
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString(`d`)
	if err != nil {
		return err
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, storedCaptchaId, storedCapchaText, err := user_management.ModuleUserManagement.PreKeyUnpackCaptcha(preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `UNPACK_ERROR:%v`, err.Error())
	}

	lvPayloadLoginId := lvPayloadElements[0]
	lvPayloadPassword := lvPayloadElements[1]
	lvPayloadOrganizationUId := lvPayloadElements[2]
	lvPayloadCaptchaId := lvPayloadElements[3]
	lvPayloadCaptchaText := lvPayloadElements[4]

	userLoginId := string(lvPayloadLoginId.Value)
	userPassword := string(lvPayloadPassword.Value)
	organizationUId := string(lvPayloadOrganizationUId.Value)
	captchaId := string(lvPayloadCaptchaId.Value)
	captchaText := string(lvPayloadCaptchaText.Value)

	if captchaId != storedCaptchaId {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `INVALID_CAPTCHA`)
	}
	if captchaText != storedCapchaText {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `INVALID_CAPTCHA`)
	}

	var user utils.JSON
	var userOrganizationMemberships []utils.JSON
	var userLoggedOrganizationId int64
	var userLoggedOrganizationUid string
	var userLoggedOrganization utils.JSON
	var verificationResult bool
	if s.OnAuthenticateUser != nil {
		verificationResult, user, userOrganizationMemberships, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}
	} else {
		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, utils.JSON{
			`loginid`: userLoginId,
		}, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}

		userId := user[`id`].(int64)

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != `` {
			us[`organization_uid`] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us,
			map[string]string{"order_index": "asc"}, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}

		userLoggedOrganizationId = userOrganizationMemberships[0][`organization_id`].(int64)
		userLoggedOrganizationUid = userOrganizationMemberships[0][`organization_uid`].(string)

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}
	}

	a, err := uuid.NewV7()
	if err != nil {
		return err
	}
	b, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	c := a.String() + b.String()
	sessionKey := strings.ReplaceAll(c, "-", "")

	userId, ok := user[`id`].(int64)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(500, `SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER`)
	}
	_, userRoleMemberships, err := user_management.ModuleUserManagement.UserRoleMembership.Select(&aepr.Log, nil, utils.JSON{
		"user_id": userId,
	}, map[string]string{"id": "ASC"}, nil)
	if err != nil {
		return err
	}

	userEffectivePrivilegeIds := map[string]int64{}
	for _, roleMembership := range userRoleMemberships {
		_, rolePrivileges, err := user_management.ModuleUserManagement.RolePrivilege.Select(&aepr.Log, nil, utils.JSON{
			"role_id": roleMembership[`role_id`],
		}, nil, nil)
		if err != nil {
			return err
		}
		for _, v1 := range rolePrivileges {
			privilegeNameId := v1[`privilege_nameid`].(string)
			privilegeId := v1[`privilege_id`].(int64)
			if privilegeNameId == `EVERYTHING` {
				_, rolePrivileges, err := user_management.ModuleUserManagement.Privilege.Select(&aepr.Log, nil, nil, nil, nil)
				if err != nil {
					return err
				}
				for _, v2 := range rolePrivileges {
					privilegeNameId := v2[`nameid`].(string)
					privilegeId := v2[`id`].(int64)
					if privilegeNameId != `EVERYTHING` {
						_, exists := userEffectivePrivilegeIds[privilegeNameId]
						if !exists {
							userEffectivePrivilegeIds[privilegeNameId] = privilegeId
						}
					}

				}
			} else {
				_, exists := userEffectivePrivilegeIds[privilegeNameId]
				if !exists {
					userEffectivePrivilegeIds[privilegeNameId] = privilegeId
				}
			}
		}
	}

	menuTreeRoot, err := s.fetchMenuTree(&aepr.Log, userEffectivePrivilegeIds)
	if err != nil {
		return err
	}

	sessionObject := utils.JSON{
		`session_key`:                   sessionKey,
		`user_id`:                       user[`id`],
		`user`:                          user,
		"organization_id":               userLoggedOrganizationId,
		"organization_uid":              userLoggedOrganizationUid,
		"organization":                  userLoggedOrganization,
		`user_organization_memberships`: userOrganizationMemberships,
		`user_role_memberships`:         userRoleMemberships,
		`user_effective_privilege_ids`:  userEffectivePrivilegeIds,
		`menu_tree_root`:                menuTreeRoot,
	}

	if s.OnCreateSessionObject != nil {
		sessionObject, err = s.OnCreateSessionObject(aepr, user, sessionObject)
		if err != nil {
			return err
		}
	}
	sessionKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `SESSION_TTL_SECOND`)
	if err != nil {
		return err
	}
	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	err = user_management.ModuleUserManagement.SessionRedis.Set(sessionKey, sessionObject, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}

	sessionObjectJSON, err := json.Marshal(sessionObject)
	if err != nil {
		return err
	}

	sessionObjectJSONString := string(sessionObjectJSON)

	lvSessionObject, err := lv.NewLV([]byte(sessionObjectJSONString))
	if err != nil {
		return err
	}
	dataBlockEnvelopeAsHexString, err := datablock.PackLVPayload(preKeyIndex, edB0PrivateKeyAsBytes, sharedKey2AsBytes, lvSessionObject)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})
	return err
}

func (s *DxmSelf) SelfLoginToken(aepr *api.DXAPIEndPointRequest) (err error) {
	sessionObject := aepr.LocalData[`session_object`].(utils.JSON)
	userId := aepr.LocalData["user_id"].(int64)

	_, user, err := user_management.ModuleUserManagement.User.ShouldGetById(&aepr.Log, userId)
	if err != nil {
		return err
	}
	if s.OnCreateSessionObject != nil {
		sessionObject, err = s.OnCreateSessionObject(aepr, user, sessionObject)
		if err != nil {
			return err
		}
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"session_object": sessionObject,
	})
	return err
}

func (s *DxmSelf) MiddlewareUserLogged(aepr *api.DXAPIEndPointRequest) (err error) {
	aepr.Log.Debugf("Middleware Start: %s", aepr.EndPoint.Uri)
	defer aepr.Log.Debugf("Middleware Done: %s", aepr.EndPoint.Uri)

	authHeader := aepr.Request.Header.Get("Authorization")
	if authHeader == "" {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `AUTHORIZATION_HEADER_NOT_FOUND`)
	}

	const bearerSchema = "Bearer "
	if !strings.HasPrefix(authHeader, bearerSchema) {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_AUTHORIZATION_HEADER`)
	}

	sessionKey := authHeader[len(bearerSchema):]

	sessionKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `SESSION_TTL_SECOND`)
	if err != nil {
		return err
	}
	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	sessionObject, err := user_management.ModuleUserManagement.SessionRedis.GetEx(sessionKey, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}
	if sessionObject == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `SESSION_NOT_FOUND`)
	}
	userId := utilsJSON.MustGetInt64(sessionObject, `user_id`)
	user := sessionObject[`user`].(utils.JSON)
	userUid, err := utilsJSON.GetString(user, `uid`)
	if err != nil {
		return err
	}
	userLoginId, err := utilsJSON.GetString(user, `loginid`)
	if err != nil {
		return err
	}
	userFullName, err := utilsJSON.GetString(user, `fullname`)
	if err != nil {
		return err
	}

	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `USER_NOT_FOUND`)
	}
	aepr.LocalData[`session_object`] = sessionObject
	aepr.LocalData[`session_key`] = sessionKey
	aepr.LocalData[`user_id`] = userId
	aepr.LocalData[`user`] = user
	aepr.CurrentUser.Id = utils.Int64ToString(userId)
	aepr.CurrentUser.Uid = userUid
	aepr.CurrentUser.LoginId = userLoginId
	aepr.CurrentUser.FullName = userFullName

	return nil
}

func (s *DxmSelf) MiddlewareUserPrivilegeCheck(aepr *api.DXAPIEndPointRequest) (err error) {
	authHeader := aepr.Request.Header.Get("Authorization")
	if authHeader == "" {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `AUTHORIZATION_HEADER_NOT_FOUND`)
	}

	const bearerSchema = "Bearer "
	if !strings.HasPrefix(authHeader, bearerSchema) {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_AUTHORIZATION_HEADER`)
	}

	sessionKey := authHeader[len(bearerSchema):]

	sessionKeyTTLAsInt, err := general.ModuleGeneral.PropertyGetAsInteger(&aepr.Log, `SESSION_TTL_SECOND`)
	if err != nil {
		return err
	}
	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	sessionObject, err := user_management.ModuleUserManagement.SessionRedis.GetEx(sessionKey, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}
	if sessionObject == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `SESSION_NOT_FOUND`)
	}
	userId := utilsJSON.MustGetInt64(sessionObject, `user_id`)
	user := sessionObject[`user`].(utils.JSON)

	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `USER_NOT_FOUND`)
	}

	userUid, err := utilsJSON.GetString(user, `uid`)
	if err != nil {
		return err
	}
	userLoginId, err := utilsJSON.GetString(user, `loginid`)
	if err != nil {
		return err
	}
	userFullName, err := utilsJSON.GetString(user, `fullname`)
	if err != nil {
		return err
	}

	if user == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `USER_NOT_FOUND`)
	}
	aepr.LocalData[`session_object`] = sessionObject
	aepr.LocalData[`session_key`] = sessionKey
	aepr.LocalData[`user_id`] = userId
	aepr.LocalData[`user`] = user
	aepr.CurrentUser.Id = utils.Int64ToString(userId)
	aepr.CurrentUser.Uid = userUid
	aepr.CurrentUser.LoginId = userLoginId
	aepr.CurrentUser.FullName = userFullName

	allowed := false
	userEffectivePrivilegeIds := sessionObject[`user_effective_privilege_ids`].(map[string]int64)
	for k := range userEffectivePrivilegeIds {
		if slices.Contains(aepr.EndPoint.Privileges, k) {
			allowed = true
		}
	}
	if !allowed {
		return aepr.WriteResponseAndNewErrorf(http.StatusForbidden, `USER_ROLE_PRIVILEGE_FORBIDDEN`)
	}
	return nil
}

func (s *DxmSelf) SelfLogout(aepr *api.DXAPIEndPointRequest) (err error) {
	sessionKey, ok := aepr.LocalData[`session_key`].(string)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `SESSION_KEY_IS_NOT_IN_REQUEST_PARAMETER`)
	}
	if sessionKey == `` {
		return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, `SESSION_KEY_IS_EMPTY`)
	}
	err = user_management.ModuleUserManagement.SessionRedis.Delete(sessionKey)
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SelfPasswordChange(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString(`i`)
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString(`d`)
	if err != nil {
		return err
	}

	lvPayloadElements, _, _, err := user_management.ModuleUserManagement.PreKeyUnpack(preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `UNPACK_ERROR:%v`, err.Error())
	}

	lvPayloadNewPassword := lvPayloadElements[0]
	lvPayloadOldPassword := lvPayloadElements[1]

	userPasswordNew := string(lvPayloadNewPassword.Value)
	userPasswordOld := string(lvPayloadOldPassword.Value)

	userId := aepr.LocalData[`user_id`].(int64)
	var verificationResult bool

	d := database.Manager.Databases[s.DatabaseNameId]
	err = d.Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *database.DXDatabaseTx) (err error) {

		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, utils.JSON{
			`id`: userId,
		}, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, `USER_NOT_FOUND`)
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPasswordOld)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, `INVALID_CREDENTIAL`)
		}

		err = user_management.ModuleUserManagement.UserPasswordTxCreate(tx, userId, userPasswordNew)
		if err != nil {
			return err
		}
		aepr.Log.Infof("User password changed")

		_, err = user_management.ModuleUserManagement.User.Update(utils.JSON{
			`must_change_password`: false,
		}, utils.JSON{
			`id`: userId,
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

func (s *DxmSelf) SelfAvatarUpdate(aepr *api.DXAPIEndPointRequest) (err error) {
	user := aepr.LocalData[`user`].(utils.JSON)
	userId := user["id"].(int64)
	userUid := user[`uid`].(string)
	filename := userUid + ".png"

	err = s.Avatar.Update(aepr, filename)
	if err != nil {
		return err
	}

	_, err = user_management.ModuleUserManagement.User.UpdateOne(&aepr.Log, userId, utils.JSON{
		"is_avatar_exist": true,
	})
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadSource(aepr *api.DXAPIEndPointRequest) (err error) {
	user := aepr.LocalData[`user`].(utils.JSON)
	userUid := user[`uid`].(string)
	filename := userUid + ".png"
	err = s.Avatar.DownloadSource(aepr, filename)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, `SELF_AVATAR_NOT_FOUND`)
	}

	return nil
}

func (s *DxmSelf) SelfAvatarDownloadSmall(aepr *api.DXAPIEndPointRequest) (err error) {
	user := aepr.LocalData[`user`].(utils.JSON)
	userUid := user[`uid`].(string)
	filename := userUid + ".png"
	err = s.Avatar.DownloadProcessedImage(aepr, `small`, filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, `SELF_AVATAR_NOT_FOUND`)
	}
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadMedium(aepr *api.DXAPIEndPointRequest) (err error) {
	user := aepr.LocalData[`user`].(utils.JSON)
	userUid := user[`uid`].(string)
	filename := userUid + ".png"
	err = s.Avatar.DownloadProcessedImage(aepr, `medium`, filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, `SELF_AVATAR_NOT_FOUND`)
	}
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	user := aepr.LocalData[`user`].(utils.JSON)
	userUid := user[`uid`].(string)
	filename := userUid + ".png"
	err = s.Avatar.DownloadProcessedImage(aepr, `big`, filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return err
	}
	return nil
}

func (s *DxmSelf) SelfProfile(aepr *api.DXAPIEndPointRequest) (err error) {
	userId := aepr.LocalData[`user_id`].(int64)
	_, user, err := user_management.ModuleUserManagement.User.ShouldGetById(&aepr.Log, userId)
	if err != nil {
		return err
	}
	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"user": user,
	})
	return nil
}

func (s *DxmSelf) SelfProfileEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	userId := aepr.LocalData[`user_id`].(int64)
	_, newValues, err := aepr.GetParameterValueAsJSON("new")
	if err != nil {
		return err
	}
	err = user_management.ModuleUserManagement.User.DoEdit(aepr, userId, newValues)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

func (s *DxmSelf) RegisterFCMToken(aepr *api.DXAPIEndPointRequest) (err error) {
	_, applicationNameId, err := aepr.GetParameterValueAsString(`application_nameid`)
	if err != nil {
		return err
	}
	_, fcmToken, err := aepr.GetParameterValueAsString(`fcm_token`)
	if err != nil {
		return err
	}
	_, deviceType, err := aepr.GetParameterValueAsString("device_type")
	if err != nil {
		return err
	}
	userId := aepr.LocalData[`user_id`].(int64)
	err = push_notification.ModulePushNotification.FCM.RegisterUserToken(aepr, applicationNameId, deviceType, userId, fcmToken)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{})
	return nil

}

var ModuleSelf DxmSelf

func init() {
	ModuleSelf = DxmSelf{
		UserOrganizationMembershipType: user_management.UserOrganizationMembershipTypeMultipleOrganizationPerUser,
	}
}
