package self

import (
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/donnyhardyanto/dxlib/captcha"
	"github.com/donnyhardyanto/dxlib/configuration"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/endpoint_rate_limiter"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib_module/module/push_notification"

	"github.com/donnyhardyanto/dxlib/api"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/utils/crypto/datablock"
	"github.com/donnyhardyanto/dxlib/utils/crypto/x25519"
	utilsJSON "github.com/donnyhardyanto/dxlib/utils/json"
	"github.com/donnyhardyanto/dxlib/utils/lv"
	"github.com/donnyhardyanto/dxlib_module/base"
	"github.com/donnyhardyanto/dxlib_module/lib"
	"github.com/donnyhardyanto/dxlib_module/module/user_management"
	"github.com/google/uuid"
	"golang.org/x/crypto/ed25519"
)

type DxmSelf struct {
	dxlibModule.DXModule
	UserOrganizationMembershipType        user_management.UserOrganizationMembershipType
	Avatar                                *lib.ImageObjectStorage
	GlobalStoreRedis                      *redis.DXRedis
	KeyGlobalStoreSystem                  string
	KeyGlobalStoreSystemMode              string
	ValueGlobalStoreSystemModeMaintenance string
	ValueGlobalStoreSystemModeNormal      string
	OnSystemSetToModeMaintenance          func(l *log.DXLog) (err error)
	OnSystemSetToModeNormal               func(l *log.DXLog) (err error)
	OnInitialize                          func(s *DxmSelf) (err error)
	OnAuthenticateUser                    func(aepr *api.DXAPIEndPointRequest, loginId string, password string, organizationUid string) (isSuccess bool, user utils.JSON, organization utils.JSON, err error)
	OnCreateSessionObject                 func(aepr *api.DXAPIEndPointRequest, user utils.JSON, organization utils.JSON, originalSessionObject utils.JSON) (newSessionObject utils.JSON, err error)
}

func (s *DxmSelf) Init(databaseNameId string) {
	s.DatabaseNameId = databaseNameId
	// Initialize rate limiter with a Redis client from your existing ModuleUserManagement
	if s.OnInitialize != nil {
		err := s.OnInitialize(s)
		if err != nil {
			slog.Error("initialization failed", slog.Any("error", err))
			os.Exit(1)
		}
	}
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
	_, edA0PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a0")
	if err != nil {
		return err
	}

	_, ecdhA1PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a1")
	if err != nil {
		return err
	}
	_, ecdhA2PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a2")
	if err != nil {
		return err
	}

	if edA0PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ED_A0_PUBLIC_KEY")
	}
	if ecdhA1PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ECDH_A1_PUBLIC_KEY")
	}
	if ecdhA2PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ECDH_A2_PUBLIC_KEY")
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
	preKeyString := "PREKEY_" + uuidA.String()

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	preKeyTTLAsInt, ok := configSystemSession["prekey_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_PREKEY_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
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
	_, edA0PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a0")
	if err != nil {
		return err
	}

	_, ecdhA1PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a1")
	if err != nil {
		return err
	}
	_, ecdhA2PublicKeyAsHexString, err := aepr.GetParameterValueAsString("a2")
	if err != nil {
		return err
	}

	if edA0PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ED_A0_PUBLIC_KEY")
	}
	if ecdhA1PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ECDH_A1_PUBLIC_KEY")
	}
	if ecdhA2PublicKeyAsHexString == "" {
		return aepr.WriteResponseAndLogAsErrorf(400, "", "PARAMETER_IS_EMPTY:ECDH_A2_PUBLIC_KEY")
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

	c := captcha.NewCaptcha()
	captchaID, captchaText := c.GenerateID()

	uuidA, err := uuid.NewV7()
	if err != nil {
		return err
	}
	preKeyString := "PREKEY_" + uuidA.String()

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	preKeyTTLAsInt, ok := configSystemSession["prekey_ttl_captcha_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_PREKEY_TTL_SECOND_CAPTCHA_NOT_FOUND_OR_NOT_INT")
	}

	preKeyTTLAsDuration := time.Duration(preKeyTTLAsInt) * time.Second
	err = user_management.ModuleUserManagement.PreKeyRedis.Set(preKeyString, utils.JSON{
		"captcha_id":     captchaID,
		"captcha_text":   captchaText,
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

	img, err := c.GenerateImage(captchaText)
	if err != nil {
		return err
	}
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

func (s *DxmSelf) menuItemCheckParentMenuRecursively(l *log.DXLog, menuitem utils.JSON, menu *[]utils.JSON) error {
	if menuitem == nil {
		return nil
	}
	parentId := menuitem["parent_id"]
	if parentId != nil {
		_, parentMenuItem, err := user_management.ModuleUserManagement.MenuItem.SelectOne(l, nil, utils.JSON{
			"id": parentId,
		}, nil, map[string]string{"id": "ASC"})
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
	parentID, ok := (*menuItem)["parent_id"].(int64)
	if !ok {
		return
	}
	parentMenuItem, exists := (*allMenuItem)[parentID]
	if !exists {
		return
	}
	parentMenuItem["allowed"] = true
	setParentMenuItemAllowed(allMenuItem, &parentMenuItem)
}

// pruneMenuItems recursively prunes the menu items that are not allowed
func pruneMenuItems(menuItem *utils.JSON) {
	children, ok := (*menuItem)["children"].(map[int64]*utils.JSON)
	if !ok {
		return
	}
	for id, childMenuItemPtr := range children {
		childMenuItem := *childMenuItemPtr
		allowed, ok := childMenuItem["allowed"].(bool)
		if !ok || !allowed {
			delete(children, id)
		} else {
			pruneMenuItems(&childMenuItem)
		}
	}
}

func (s *DxmSelf) fetchMenuTree(l *log.DXLog, userEffectivePrivilegeIds map[string]int64) ([]*utils.JSON, error) {
	// select all menu items available
	allMenuItems := map[int64]utils.JSON{}
	_, menuItems, err := user_management.ModuleUserManagement.MenuItem.Select(l, nil, nil, nil, map[string]string{"id": "ASC"}, nil, nil)
	if err != nil {
		return nil, err
	}
	for _, menuItem := range menuItems {
		menuItemId, ok := menuItem["id"].(int64)
		if !ok {
			continue
		}
		allMenuItems[menuItemId] = menuItem
	}

	// Build the complete menu tree
	var roots []*utils.JSON
	for _, menuItem := range allMenuItems {
		menuItemIndex, ok := menuItem["item_index"].(int64)
		if !ok {
			continue
		}
		if menuItem["children"] == nil {
			menuItem["children"] = map[int64]*utils.JSON{}
		}
		if menuItem["parent_id"] != nil {
			parentId, ok := menuItem["parent_id"].(int64)
			if !ok {
				continue
			}
			parentMenuItem := allMenuItems[parentId]
			//			menuItem["parent_menu_item"] = &parentMenuItem
			menuItem["allowed"] = false
			if parentMenuItem["children"] == nil {
				parentMenuItem["children"] = map[int64]*utils.JSON{}
			}
			parentChildren, ok := parentMenuItem["children"].(map[int64]*utils.JSON)
			if ok {
				parentChildren[menuItemIndex] = &menuItem
			}
		} else {
			roots = append(roots, &menuItem)
		}
	}

	// only keep menu items that the user has access to
	for _, privilegeId := range userEffectivePrivilegeIds {
		for _, menuItem := range allMenuItems {
			if menuItem["privilege_id"] == privilegeId {
				menuItem["allowed"] = true
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
		children, ok := menuItem["children"].(map[int64]*utils.JSON)
		if !ok {
			continue
		}
		sortedChildren := make([]*utils.JSON, 0, len(children))
		for _, childMenuItemPtr := range children {
			childMenuItem := *childMenuItemPtr
			//			delete(childMenuItem, "parent_menu_item")
			sortedChildren = append(sortedChildren, &childMenuItem)
		}
		// Sort the slice based on item_index
		sort.Slice(sortedChildren, func(i, j int) bool {
			iIdx, _ := (*sortedChildren[i])["item_index"].(int64)
			jIdx, _ := (*sortedChildren[j])["item_index"].(int64)
			return iIdx < jIdx
		})
		menuItem["children"] = sortedChildren
	}
	return roots, nil
}

func (s *DxmSelf) SelfConfiguration(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString("i")
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString("d")
	if err != nil {
		return err
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, err := user_management.ModuleUserManagement.PreKeyUnpack(preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "", "UNPACK_ERROR:%v", err.Error())
	}
	if len(lvPayloadElements) < 1 {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "", "PAYLOAD_LESS_THAN_ONE")
	}

	lvMobileAppNameId := lvPayloadElements[0]

	configExternalSystem := *configuration.Manager.Configurations["external_system"].Data
	mobileAppConfiguration, ok := configExternalSystem["MOBILE_APP1"].(utils.JSON)
	if !ok {
		return errors.Errorf("GET_CONFIGURATION:MOBILE_APP_CONFIG_NOT_FOUND")
	}
	apiKeyGoogleMap, ok := mobileAppConfiguration["api_key_google_map"].(string)
	if !ok {
		return errors.Errorf("GET_CONFIGURATION:MOBILE_APP_API_KEY_GOOGLE_MAP_CONFIG_NOT_FOUND")
	}
	apiKeyFirebase, ok := mobileAppConfiguration["api_key_firebase"].(string)
	if !ok {
		return errors.Errorf("GET_CONFIGURATION:MOBILE_APP_API_KEY_FIREBASE_CONFIG_NOT_FOUND")
	}
	lvAPIKeyGoogleMap, err := lv.NewLV([]byte(apiKeyGoogleMap))
	if err != nil {
		return err
	}
	lvAPIKeyFirebase, err := lv.NewLV([]byte(apiKeyFirebase))
	if err != nil {
		return err
	}

	dataBlockEnvelopeAsHexString, err := datablock.PackLVPayload(preKeyIndex, edB0PrivateKeyAsBytes,
		sharedKey2AsBytes, lvMobileAppNameId, lvAPIKeyGoogleMap, lvAPIKeyFirebase)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})
	return err
}

func (s *DxmSelf) SelfLogin(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString("i")
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString("d")
	if err != nil {
		return err
	}

	if api.OnE2EEPrekeyUnPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyUnPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, _, err := api.OnE2EEPrekeyUnPack(aepr, preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PREKEY", "NOT_ERROR:UNPACK_ERROR:%v", err.Error())
	}

	lvPayloadLoginId := lvPayloadElements[0]
	lvPayloadPassword := lvPayloadElements[1]

	organizationUId := ""
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
		verificationResult, user, userLoggedOrganization, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, ok := user["id"].(int64)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_INT64")
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, ok = userLoggedOrganization["id"].(int64)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:ORGANIZATION_ID_NOT_INT64")
		}
		userLoggedOrganizationUid, ok = userLoggedOrganization["uid"].(string)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:ORGANIZATION_UID_NOT_STRING")
		}
	} else {
		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"loginid": userLoginId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, ok := user["id"].(int64)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_INT64")
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, ok = userOrganizationMemberships[0]["organization_id"].(int64)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:ORGANIZATION_ID_NOT_INT64")
		}
		userLoggedOrganizationUid, ok = userOrganizationMemberships[0]["organization_uid"].(string)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:ORGANIZATION_UID_NOT_STRING")
		}

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	}

	sessionKey, err := GenerateSessionKey()
	if err != nil {
		return err
	}

	userId, ok := user["id"].(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(500, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER")
	}
	a := []any{userOrganizationMemberships}
	sessionObject, allowed, err2 := s.RegenerateSessionObject(aepr, userId, sessionKey, user, userLoggedOrganizationId, userLoggedOrganizationUid, userLoggedOrganization, a)
	if err2 != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:SESSION_KEY_EXPIRED_%s", err2.Error())
	}

	if !allowed {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusForbidden, "USER_ROLE_PRIVILEGE_FORBIDDEN", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
	}

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
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

	if api.OnE2EEPrekeyPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	dataBlockEnvelopeAsHexString, err := api.OnE2EEPrekeyPack(aepr, preKeyIndex, edB0PrivateKeyAsBytes, sharedKey2AsBytes, lvSessionObject)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})
	return err
}

func (s *DxmSelf) SelfLoginV2(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userLoginId, err := aepr.GetParameterValueAsString("user_login_id")
	if err != nil {
		return err
	}
	_, userPassword, err := aepr.GetParameterValueAsString("user_login_password")
	if err != nil {
		return err
	}
	_, organizationUId, err := aepr.GetParameterValueAsString("organization_uid", "")
	if err != nil {
		return err
	}

	var user utils.JSON
	var userOrganizationMemberships []utils.JSON
	var userLoggedOrganizationId int64
	var userLoggedOrganizationUid string
	var userLoggedOrganization utils.JSON
	var verificationResult bool
	if s.OnAuthenticateUser != nil {
		verificationResult, user, userLoggedOrganization, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, err := utils.GetInt64FromKV(user, "id")
		if err != nil {
			return err
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, err = utils.GetInt64FromKV(userLoggedOrganization, "id")
		if err != nil {
			return err
		}
		userLoggedOrganizationUid, err = utils.GetStringFromKV(userLoggedOrganization, "uid")
		if err != nil {
			return err
		}
	} else {
		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"loginid": userLoginId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, err := utils.GetInt64FromKV(user, "id")
		if err != nil {
			return err
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, err = utils.GetInt64FromKV(userOrganizationMemberships[0], "organization_id")
		if err != nil {
			return err
		}
		userLoggedOrganizationUid, err = utils.GetStringFromKV(userOrganizationMemberships[0], "organization_uid")
		if err != nil {
			return err
		}

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	}

	sessionKey, err := GenerateSessionKey()
	if err != nil {
		return err
	}

	userId, ok := user["id"].(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(500, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER")
	}
	a := []any{userOrganizationMemberships}
	sessionObject, allowed, err2 := s.RegenerateSessionObject(aepr, userId, sessionKey, user, userLoggedOrganizationId, userLoggedOrganizationUid, userLoggedOrganization, a)
	if err2 != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:SESSION_KEY_EXPIRED_%s", err2.Error())
	}

	if !allowed {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusForbidden, "USER_ROLE_PRIVILEGE_FORBIDDEN", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
	}

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
	}

	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	err = user_management.ModuleUserManagement.SessionRedis.Set(sessionKey, sessionObject, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"session_object": sessionObject,
	})
	return err
}

func (s *DxmSelf) RegenerateSessionObject(aepr *api.DXAPIEndPointRequest, userId int64, sessionKey string, user utils.JSON, userLoggedOrganizationId int64,
	userLoggedOrganizationUid string, userLoggedOrganization utils.JSON, userOrganizationMemberships []any) (sessionObject utils.JSON, allowed bool, err error) {
	var userEffectivePrivilegeIds map[string]int64

	_, userRoleMemberships, err := user_management.ModuleUserManagement.UserRoleMembership.Select(&aepr.Log, nil, utils.JSON{
		"user_id": userId,
	}, nil, map[string]string{"id": "ASC"}, nil, nil)
	if err != nil {
		return nil, false, err
	}

	userEffectivePrivilegeIds = map[string]int64{}
	for _, roleMembership := range userRoleMemberships {
		_, rolePrivileges, err := user_management.ModuleUserManagement.RolePrivilege.Select(&aepr.Log, nil, utils.JSON{
			"role_id": roleMembership["role_id"],
		}, nil, nil, nil, nil)
		if err != nil {
			return nil, false, err
		}
		for _, v1 := range rolePrivileges {
			privilegeNameId, err := utils.GetStringFromKV(v1, "privilege_nameid")
			if err != nil {
				return nil, false, err
			}

			privilegeId, err := utils.GetInt64FromKV(v1, "privilege_id")
			if err != nil {
				return nil, false, err
			}
			if privilegeNameId == "EVERYTHING" {
				_, rolePrivileges, err := user_management.ModuleUserManagement.Privilege.Select(&aepr.Log, nil, nil, nil, nil, nil, nil)
				if err != nil {
					return nil, false, err
				}
				for _, v2 := range rolePrivileges {
					privilegeNameId, err := utils.GetStringFromKV(v2, "nameid")
					if err != nil {
						return nil, false, err
					}
					privilegeId, err := utils.GetInt64FromKV(v2, "id")
					if err != nil {
						return nil, false, err
					}
					if privilegeNameId != "EVERYTHING" {
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
		return nil, false, err
	}

	sessionObject = utils.JSON{
		"session_key":                   sessionKey,
		"user_id":                       userId,
		"user":                          user,
		"organization_id":               userLoggedOrganizationId,
		"organization_uid":              userLoggedOrganizationUid,
		"organization":                  userLoggedOrganization,
		"user_organization_memberships": userOrganizationMemberships,
		"user_role_memberships":         userRoleMemberships,
		"user_effective_privilege_ids":  userEffectivePrivilegeIds,
		"menu_tree_root":                menuTreeRoot,
	}

	if len(aepr.EndPoint.Privileges) > 0 {
		allowed = false
		for k := range userEffectivePrivilegeIds {
			if slices.Contains(aepr.EndPoint.Privileges, k) {
				allowed = true
			}
		}
	} else {
		allowed = true
	}
	if !allowed {
		return sessionObject, false, err
	}

	if s.OnCreateSessionObject != nil {
		sessionObject, err = s.OnCreateSessionObject(aepr, user, userLoggedOrganization, sessionObject)
		if err != nil {
			return sessionObject, true, err
		}
	}

	return sessionObject, true, nil
}

func GenerateSessionKey() (string, error) {
	a, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	b, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}

	c, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	d, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}

	z := a.String() + b.String() + c.String() + d.String()

	sessionKey := strings.ReplaceAll(z, "-", "")
	return sessionKey, nil
}

func (s *DxmSelf) SelfLoginCaptcha(aepr *api.DXAPIEndPointRequest) (err error) {

	_, preKeyIndex, err := aepr.GetParameterValueAsString("i")
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString("d")
	if err != nil {
		return err
	}

	if api.OnE2EEPrekeyUnPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyUnPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, preKeyData, err := api.OnE2EEPrekeyUnPack(aepr, preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "REFRESH_CAPTCHA", "NOT_ERROR:UNPACK_ERROR:%v", err.Error())
	}

	if preKeyData == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "REFRESH_CAPTCHA", "NOT_ERROR:UNPACK_ERROR:PREKEY_NOT_FOUND")
	}

	storedCaptchaId, err := utils.GetStringFromKV(preKeyData, "captcha_id")
	if err != nil {
		return err
	}
	storedCaptchaText, err := utils.GetStringFromKV(preKeyData, "captcha_text")
	if err != nil {
		return err
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
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnprocessableEntity, "INVALID_CAPTCHA", "NOT_ERROR:INVALID_CAPTCHA")
		return
	}
	if captchaText != storedCaptchaText {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnprocessableEntity, "INVALID_CAPTCHA", "NOT_ERROR:INVALID_CAPTCHA")
		return
	}

	var user utils.JSON
	var userOrganizationMemberships []utils.JSON
	var userLoggedOrganizationId int64
	var userLoggedOrganizationUid string
	var userLoggedOrganization utils.JSON
	var verificationResult bool
	if s.OnAuthenticateUser != nil {
		verificationResult, user, userLoggedOrganization, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	} else {
		_, user, err = user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"loginid": userLoginId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, err := utils.GetInt64FromKV(user, "id")
		if err != nil {
			return err
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, err = utils.GetInt64FromKV(userOrganizationMemberships[0], "organization_id")
		if err != nil {
			return err
		}
		userLoggedOrganizationUid, err = utils.GetStringFromKV(userOrganizationMemberships[0], "organization_uid")
		if err != nil {
			return err
		}

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	}

	sessionKey, err := GenerateSessionKey()
	if err != nil {
		return err
	}

	userId, err := utils.GetInt64FromKV(user, "id")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(500, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER")
	}
	a := []any{userOrganizationMemberships}
	sessionObject, allowed, err2 := s.RegenerateSessionObject(aepr, userId, sessionKey, user, userLoggedOrganizationId, userLoggedOrganizationUid, userLoggedOrganization, a)
	if err2 != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:SESSION_KEY_EXPIRED_%s", err2.Error())
	}

	if !allowed {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusForbidden, "USER_ROLE_PRIVILEGE_FORBIDDEN", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
	}

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
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

	if api.OnE2EEPrekeyPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	dataBlockEnvelopeAsHexString, err := api.OnE2EEPrekeyPack(aepr, preKeyIndex, edB0PrivateKeyAsBytes, sharedKey2AsBytes, lvSessionObject)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})
	return err
}

func (s *DxmSelf) SelfLoginCaptchaV2(aepr *api.DXAPIEndPointRequest) (err error) {

	_, preKeyIndex, err := aepr.GetParameterValueAsString("i")
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString("d")
	if err != nil {
		return err
	}

	if api.OnE2EEPrekeyUnPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyUnPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	lvPayloadElements, sharedKey2AsBytes, edB0PrivateKeyAsBytes, preKeyData, err := api.OnE2EEPrekeyUnPack(aepr, preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PREKEY", "NOT_ERROR:UNPACK_ERROR:%v", err.Error())
	}

	lvPayloadHeader := lvPayloadElements[0]
	lvPayloadBody := lvPayloadElements[1]

	payLoadHeaderAsBase64 := lvPayloadHeader.Value
	payLoadHeaderAsBytes, err := base64.StdEncoding.DecodeString(string(payLoadHeaderAsBase64))
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_DECODED_PAYLOAD_HEADER_FROM_BASE64")
	}
	payloadHeader := map[string]string{}
	err = json.Unmarshal(payLoadHeaderAsBytes, &payloadHeader)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_HEADER_BYTES")
	}

	aepr.EncryptionParameters = utils.JSON{
		"PRE_KEY_INDEX":              preKeyIndex,
		"SHARED_KEY_2_AS_BYTES":      sharedKey2AsBytes,
		"ED_B0_PRIVATE_KEY_AS_BYTES": edB0PrivateKeyAsBytes,
		"PRE_KEY_DATA":               preKeyData,
	}
	aepr.EffectiveRequestHeader = payloadHeader

	payLoadBodyAsBase64 := lvPayloadBody.Value
	payLoadBodyAsBytes, err := base64.StdEncoding.DecodeString(string(payLoadBodyAsBase64))
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_DECODED_PAYLOAD_BODY_FROM_BASE64")
	}
	payloadBodyAsJSON := utils.JSON{}
	err = json.Unmarshal(payLoadBodyAsBytes, &payloadBodyAsJSON)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}

	userLoginId, err := utils.GetStringFromKV(payloadBodyAsJSON, "user_login_id")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}
	userPassword, err := utils.GetStringFromKV(payloadBodyAsJSON, "user_login_password")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}
	organizationUId, err := utils.GetStringFromKV(payloadBodyAsJSON, "organization_uid")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}
	captchaId, err := utils.GetStringFromKV(payloadBodyAsJSON, "captcha_id")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}
	captchaText, err := utils.GetStringFromKV(payloadBodyAsJSON, "captcha_text")
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "DATA_CORRUPT:INVALID_UNMARSHAL_PAYLOAD_BODY_BYTES")
	}

	if preKeyData == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PREKEY", "NOT_ERROR:UNPACK_ERROR:PREKEY_NOT_FOUND")
	}

	storedCaptchaId, err := utils.GetStringFromKV(preKeyData, "captcha_id")
	if err != nil {
		return err
	}
	storedCaptchaText, err := utils.GetStringFromKV(preKeyData, "captcha_text")
	if err != nil {
		return err
	}

	if captchaId != storedCaptchaId {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnprocessableEntity, "INVALID_CAPTCHA", "NOT_ERROR:INVALID_CAPTCHA")
		return
	}
	if captchaText != storedCaptchaText {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnprocessableEntity, "INVALID_CAPTCHA", "NOT_ERROR:INVALID_CAPTCHA")
		return
	}

	var user utils.JSON
	var userOrganizationMemberships []utils.JSON
	var userLoggedOrganizationId int64
	var userLoggedOrganizationUid string
	var userLoggedOrganization utils.JSON
	var verificationResult bool
	if s.OnAuthenticateUser != nil {
		verificationResult, user, userLoggedOrganization, err = s.OnAuthenticateUser(aepr, userLoginId, userPassword, organizationUId)
		if err != nil {
			return err
		}
		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	} else {
		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"loginid": userLoginId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userId, err := utils.GetInt64FromKV(user, "id")
		if err != nil {
			return err
		}

		us := utils.JSON{
			"user_id": userId,
		}

		if organizationUId != "" {
			us["organization_uid"] = organizationUId
		}

		_, userOrganizationMemberships, err = user_management.ModuleUserManagement.UserOrganizationMembership.Select(&aepr.Log, nil, us, nil,
			map[string]string{"order_index": "asc"}, nil, nil)
		if err != nil {
			return err
		}

		if len(userOrganizationMemberships) == 0 {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}

		userLoggedOrganizationId, err = utils.GetInt64FromKV(userOrganizationMemberships[0], "organization_id")
		if err != nil {
			return err
		}
		userLoggedOrganizationUid, err = utils.GetStringFromKV(userOrganizationMemberships[0], "organization_uid")
		if err != nil {
			return err
		}

		_, userLoggedOrganization, err = user_management.ModuleUserManagement.Organization.ShouldGetById(&aepr.Log, userLoggedOrganizationId)
		if err != nil {
			return err
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPassword)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, base.LogMsgNotErrorInvalidCredential)
		}
	}

	sessionKey, err := GenerateSessionKey()
	if err != nil {
		return err
	}

	userId, ok := user["id"].(int64)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(500, "INTERNAL_ERROR", "SHOULD_NOT_HAPPEN:USER_ID_NOT_FOUND_IN_USER")
	}
	a := []any{userOrganizationMemberships}
	sessionObject, allowed, err2 := s.RegenerateSessionObject(aepr, userId, sessionKey, user, userLoggedOrganizationId, userLoggedOrganizationUid, userLoggedOrganization, a)
	if err2 != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:SESSION_KEY_EXPIRED_%s", err2.Error())
	}

	if !allowed {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusForbidden, "USER_ROLE_PRIVILEGE_FORBIDDEN", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
	}

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
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

	if api.OnE2EEPrekeyPack == nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "NOT_IMPLEMENTED", "NOT_IMPLEMENTED:OnE2EEPrekeyPack_IS_NIL:%v", aepr.EndPoint.EndPointType)
	}

	dataBlockEnvelopeAsHexString, err := api.OnE2EEPrekeyPack(aepr, preKeyIndex, edB0PrivateKeyAsBytes, sharedKey2AsBytes, lvSessionObject)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"d": dataBlockEnvelopeAsHexString,
	})

	return err
}

func (s *DxmSelf) SelfLoginToken(aepr *api.DXAPIEndPointRequest) (err error) {
	sessionObject, ok := aepr.LocalData["session_object"].(utils.JSON)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:NO_SESSION_OBJECT")
	}
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	sessionKey, err := utils.GetStringFromKV(sessionObject, "session_key")
	if err != nil {
		return err
	}
	userLoggedOrganizationId, err := utils.GetInt64FromKV(aepr.LocalData, "organization_id")
	if err != nil {
		return err
	}
	userLoggedOrganizationUid, err := utils.GetStringFromKV(aepr.LocalData, "organization_uid")
	if err != nil {
		return err
	}
	userLoggedOrganization, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "organization")
	if err != nil {
		return err
	}
	userOrganizationMemberships, err := utils.GetVFromKV[[]interface{}](aepr.LocalData, "user_organization_memberships")
	if err != nil {
		return err
	}

	_, user, err := user_management.ModuleUserManagement.User.GetById(&aepr.Log, userId)
	if err != nil {
		return err
	}
	sessionObject, allowed, err := s.RegenerateSessionObject(aepr, userId, sessionKey, user, userLoggedOrganizationId, userLoggedOrganizationUid, userLoggedOrganization, userOrganizationMemberships)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_KEY_EXPIRED", "NOT_ERROR:SESSION_KEY_EXPIRED_%s", err.Error())
	}

	if !allowed {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusForbidden, "USER_ROLE_PRIVILEGE_FORBIDDEN", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
	}

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
	}

	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	err = user_management.ModuleUserManagement.SessionRedis.Set(sessionKey, sessionObject, sessionKeyTTLAsDuration)
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"session_object": sessionObject,
	})
	return err
}

func SessionKeyToSessionObject(aepr *api.DXAPIEndPointRequest, sessionKey string) (sessionObject utils.JSON, err error) {

	configSystem := *configuration.Manager.Configurations["system"].Data
	configSystemSession, ok := configSystem["sessions"].(utils.JSON)
	if !ok {
		return nil, errors.New("SHOULD_NOT_HAPPEN:CONFIG_SYSTEM_SESSIONS_NOT_FOUND")
	}
	sessionKeyTTLAsInt, ok := configSystemSession["session_ttl_in_seconds"].(int)
	if !ok {
		return nil, errors.New("SHOULD_NOT_HAPPEN:SESSIONS_TTL_SECOND_NOT_FOUND_OR_NOT_INT")
	}
	sessionKeyTTLAsDuration := time.Duration(sessionKeyTTLAsInt) * time.Second

	sessionObject, err = user_management.ModuleUserManagement.SessionRedis.GetEx(sessionKey, sessionKeyTTLAsDuration)
	if err != nil {
		return nil, err
	}
	if sessionObject == nil {
		return nil, aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_NOT_FOUND", "NOT_ERROR:SESSION_NOT_FOUND")
	}
	userId, err := utilsJSON.GetInt64(sessionObject, "user_id")
	if err != nil {
		return nil, err
	}
	user, err := utils.GetVFromKV[utils.JSON](sessionObject, "user")
	if err != nil {
		return nil, err
	}
	userUid, err := utilsJSON.GetString(user, "uid")
	if err != nil {
		return nil, err
	}
	userLoginId, err := utilsJSON.GetString(user, "loginid")
	if err != nil {
		return nil, err
	}
	userFullName, err := utilsJSON.GetString(user, "fullname")
	if err != nil {
		return nil, err
	}
	organization, err := utils.GetVFromKV[utils.JSON](sessionObject, "organization")
	if err != nil {
		return nil, err
	}
	organizationId, err := utilsJSON.GetInt64(organization, "id")
	if err != nil {
		return nil, err
	}
	organizationUid, err := utilsJSON.GetString(organization, "uid")
	if err != nil {
		return nil, err
	}
	organizationName, err := utilsJSON.GetString(organization, "name")
	if err != nil {
		return nil, err
	}
	userOrganizationMemberships, ok := sessionObject["user_organization_memberships"].([]interface{})
	if !ok {
		return nil, aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "", "NOT_ERROR:USER_ORGANIZATION_MEMBERSHIPS_NOT_FOUND")
	}

	if user == nil {
		return nil, aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "", "USER_NOT_FOUND")
	}
	aepr.LocalData["session_object"] = sessionObject
	aepr.LocalData["session_key"] = sessionKey
	aepr.LocalData["user_id"] = userId
	aepr.LocalData["user_uid"] = userUid
	aepr.LocalData["user"] = user
	aepr.LocalData["organization_id"] = organizationId
	aepr.LocalData["organization_uid"] = organizationUid
	aepr.LocalData["organization_name"] = organizationName
	aepr.LocalData["organization"] = organization
	aepr.LocalData["user_organization_memberships"] = userOrganizationMemberships

	aepr.CurrentUser.Id = utils.Int64ToString(userId)
	aepr.CurrentUser.Uid = userUid
	aepr.CurrentUser.LoginId = userLoginId
	aepr.CurrentUser.FullName = userFullName
	aepr.CurrentUser.OrganizationId = utils.Int64ToString(organizationId)
	aepr.CurrentUser.OrganizationUid = organizationUid
	aepr.CurrentUser.OrganizationName = organizationName

	return sessionObject, nil
}

func CheckUserPrivilegeForEndPoint(aepr *api.DXAPIEndPointRequest, userEffectivePrivilegeIds utils.JSON) (err error) {
	if aepr.EndPoint.Privileges == nil {
		return nil
	}
	if len(aepr.EndPoint.Privileges) == 0 {
		return nil
	}

	for k := range userEffectivePrivilegeIds {
		if slices.Contains(aepr.EndPoint.Privileges, k) {
			return nil
		}
	}

	return aepr.WriteResponseAndNewErrorf(http.StatusForbidden, "", "NOT_ERROR:USER_ROLE_PRIVILEGE_FORBIDDEN")
}

func (s *DxmSelf) CheckMaintenanceMode(aepr *api.DXAPIEndPointRequest, userEffectivePrivilegeIds utils.JSON) (err error) {
	globalStoreSystemValue, err := s.GlobalStoreRedis.Get(s.KeyGlobalStoreSystem)
	if err != nil {
		// if no key set, that means Normal mode
		return CheckUserPrivilegeForEndPoint(aepr, userEffectivePrivilegeIds)
	}
	modeAsAny, ok := globalStoreSystemValue[s.KeyGlobalStoreSystemMode]
	if !ok {
		// if no key set, that means Normal mode
		return CheckUserPrivilegeForEndPoint(aepr, userEffectivePrivilegeIds)
	}
	modeValue, ok := modeAsAny.(string)
	if !ok {
		// if no key set, that means Normal mode
		return CheckUserPrivilegeForEndPoint(aepr, userEffectivePrivilegeIds)
	}
	if modeValue != s.ValueGlobalStoreSystemModeMaintenance {
		// if not Maintenance mode, then Normal mode
		return CheckUserPrivilegeForEndPoint(aepr, userEffectivePrivilegeIds)
	}
	// The system now in maintenance mode
	_, ok = userEffectivePrivilegeIds[base.PrivilegeNameIdSetMaintenance]
	if !ok {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusServiceUnavailable, "SYSTEM_UNDER_MAINTENANCE", "NOT_ERROR:SYSTEM_UNDER_MAINTENANCE")
		// If the user has no PrivilegeNameIdSetMaintenance then false
		return nil
	}
	return CheckUserPrivilegeForEndPoint(aepr, userEffectivePrivilegeIds)
}

func (s *DxmSelf) MiddlewareUserLoggedAndPrivilegeCheck(aepr *api.DXAPIEndPointRequest) (err error) {
	aepr.Log.Debugf("Middleware Start: %s", aepr.EndPoint.Uri)
	defer aepr.Log.Debugf("Middleware Done: %s", aepr.EndPoint.Uri)

	authHeader := utils.GetStringFromMapStringStringDefault(aepr.EffectiveRequestHeader, "Authorization", "")
	if authHeader == "" {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, "", "NOT_ERROR:AUTHORIZATION_HEADER_NOT_FOUND")
	}

	const bearerSchema = "Bearer "
	if !strings.HasPrefix(authHeader, bearerSchema) {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnauthorized, "", "NOT_ERROR:INVALID_AUTHORIZATION_HEADER")
	}

	sessionKey := authHeader[len(bearerSchema):]

	sessionObject, err := SessionKeyToSessionObject(aepr, sessionKey)
	if err != nil {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnauthorized, "SESSION_EXPIRED", "NOT_ERROR:SESSION_EXPIRED")
		return nil
	}

	if sessionObject == nil {
		aepr.WriteResponseAsErrorMessageNotLogged(http.StatusUnauthorized, "SESSION_EXPIRED", "NOT_ERROR:SESSION_EXPIRED")
		return nil
	}

	userEffectivePrivilegeIds, err := utils.GetVFromKV[utils.JSON](sessionObject, "user_effective_privilege_ids")
	if err != nil {
		// fallback to map[string]int64 if it was stored that way (e.g., before serialized to JSON)
		userEffectivePrivilegeIdsMap, ok := sessionObject["user_effective_privilege_ids"].(map[string]int64)
		if !ok {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "SESSION_EXPIRED", "NOT_ERROR:USER_EFFECTIVE_PRIVILEGE_IDS_NOT_FOUND")
		}
		userEffectivePrivilegeIds = make(utils.JSON)
		for k, v := range userEffectivePrivilegeIdsMap {
			userEffectivePrivilegeIds[k] = v
		}
	}

	err = s.CheckMaintenanceMode(aepr, userEffectivePrivilegeIds)
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) MiddlewareRequestRateLimitCheck(aepr *api.DXAPIEndPointRequest) (err error) {
	rateLimitGroupNameId := aepr.EndPoint.RateLimitGroupNameId
	// Bypass when ""
	if rateLimitGroupNameId == "" {
		return nil
	}
	identifier := aepr.Request.RemoteAddr
	// You might want to use X-Forwarded-For header if behind a proxy
	forwardedFor := utils.GetStringFromMapStringStringDefault(aepr.EffectiveRequestHeader, "X-Forwarded-For", "")

	if forwardedFor != "" {
		identifier = forwardedFor
	}

	limiter := endpoint_rate_limiter.Manager.EndpointRateLimiter

	allowed, err := limiter.IsAllowed(aepr.Request.Context(), rateLimitGroupNameId, identifier)
	if err != nil {
		aepr.WriteResponseAsError(http.StatusInternalServerError, err)
		return err
	}
	w := *aepr.ResponseWriter
	if !allowed {
		// Get blocked status and remaining time if blocked
		blocked, remaining, _ := limiter.GetBlockedStatus(aepr.Request.Context(), rateLimitGroupNameId, identifier) // error discarded: IsAllowed above already succeeded on the same backend, so this call is virtually guaranteed to succeed; the worst case, Retry-After header is omitted and 429 is still returned
		if blocked {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(remaining.Seconds())))
		}
		return aepr.WriteResponseAndNewErrorf(http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "NOT_ERROR:RATE_LIMIT_EXCEEDED")
	}

	// Add rate limit headers
	remaining, _ := limiter.GetRemainingAttempts(aepr.Request.Context(), rateLimitGroupNameId, identifier)
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

	// Call the actual handler
	return nil
}

func (s *DxmSelf) SelfLogout(aepr *api.DXAPIEndPointRequest) (err error) {
	sessionKey, ok := aepr.LocalData["session_key"].(string)
	if !ok {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "", "SESSION_KEY_IS_NOT_IN_REQUEST_PARAMETER")
	}
	if sessionKey == "" {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusNotFound, "", "SESSION_KEY_IS_EMPTY")
	}
	err = user_management.ModuleUserManagement.SessionRedis.Delete(sessionKey)
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SelfPasswordChange(aepr *api.DXAPIEndPointRequest) (err error) {
	_, preKeyIndex, err := aepr.GetParameterValueAsString("i")
	if err != nil {
		return err
	}
	_, dataAsHexString, err := aepr.GetParameterValueAsString("d")
	if err != nil {
		return err
	}

	lvPayloadElements, _, _, err := user_management.ModuleUserManagement.PreKeyUnpack(preKeyIndex, dataAsHexString)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "DATA_CORRUPT", "UNPACK_ERROR:%s", err.Error())
	}

	lvPayloadNewPassword := lvPayloadElements[0]
	lvPayloadOldPassword := lvPayloadElements[1]

	userPasswordNew := string(lvPayloadNewPassword.Value)
	userPasswordOld := string(lvPayloadOldPassword.Value)

	if user_management.ModuleUserManagement.OnUserFormatPasswordValidation != nil {
		err = user_management.ModuleUserManagement.OnUserFormatPasswordValidation(userPasswordNew)
		if err != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PASSWORD_FORMAT:%s", "NOT_ERROR:INVALID_PASSWORD_FORMAT:%s", err.Error())
		}
	}

	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	var verificationResult bool

	err = databases.Manager.GetOrCreate(user_management.ModuleUserManagement.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err error) {

		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"id": userId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, "ALERT_POSSIBLE_HACKING:USER_NOT_FOUND")
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPasswordOld)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "", base.MsgInvalidCredential)
		}

		err = user_management.ModuleUserManagement.TxUserPasswordCreate(tx, userId, userPasswordNew)
		if err != nil {
			return err
		}
		aepr.Log.Infof("User password changed")

		_, err = user_management.ModuleUserManagement.User.UpdateSimple(utils.JSON{
			"must_change_password": false,
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

func (s *DxmSelf) SelfPasswordChangeV2(aepr *api.DXAPIEndPointRequest) (err error) {
	_, userPasswordNew, err := aepr.GetParameterValueAsString("user_login_password_new")
	if err != nil {
		return err
	}
	_, userPasswordOld, err := aepr.GetParameterValueAsString("user_login_password_old")
	if err != nil {
		return err
	}

	if user_management.ModuleUserManagement.OnUserFormatPasswordValidation != nil {
		err = user_management.ModuleUserManagement.OnUserFormatPasswordValidation(userPasswordNew)
		if err != nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnprocessableEntity, "INVALID_PASSWORD_FORMAT:%s", "NOT_ERROR:INVALID_PASSWORD_FORMAT:%s", err.Error())
		}
	}
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	var verificationResult bool

	err = databases.Manager.GetOrCreate(user_management.ModuleUserManagement.DatabaseNameId).Tx(&aepr.Log, sql.LevelReadCommitted, func(tx *databases.DXDatabaseTx) (err error) {

		_, user, err := user_management.ModuleUserManagement.User.SelectOne(&aepr.Log, nil, utils.JSON{
			"id": userId,
		}, nil, nil)
		if err != nil {
			return err
		}
		if user == nil {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, base.MsgInvalidCredential, "ALERT_POSSIBLE_HACKING:USER_NOT_FOUND")
		}

		verificationResult, err = user_management.ModuleUserManagement.UserPasswordVerify(&aepr.Log, userId, userPasswordOld)
		if err != nil {
			return err
		}

		if !verificationResult {
			return aepr.WriteResponseAndLogAsErrorf(http.StatusUnauthorized, "", base.MsgInvalidCredential)
		}

		err = user_management.ModuleUserManagement.TxUserPasswordCreate(tx, userId, userPasswordNew)
		if err != nil {
			return err
		}
		aepr.Log.Infof("User password changed")

		_, err = user_management.ModuleUserManagement.User.UpdateSimple(utils.JSON{
			"must_change_password": false,
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

func (s *DxmSelf) SelfAvatarUpdate(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"

	err = s.Avatar.Update(aepr, filename, "")
	if err != nil {
		return err
	}

	_, err = user_management.ModuleUserManagement.User.UpdateById(&aepr.Log, userId, utils.JSON{
		"is_avatar_exist": true,
	})
	return nil
}

func (s *DxmSelf) SelfAvatarUpdateFileContentBase64(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"

	_, fileContentBase64, err := aepr.GetParameterValueAsString("content_base64")
	if err != nil {
		return err
	}

	err = s.Avatar.Update(aepr, filename, fileContentBase64)
	if err != nil {
		return err
	}

	_, err = user_management.ModuleUserManagement.User.UpdateById(&aepr.Log, userId, utils.JSON{
		"is_avatar_exist": true,
	})
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadSource(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"
	err = s.Avatar.DownloadSource(aepr, filename)
	if err != nil {
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "", "SELF_AVATAR_NOT_FOUND")
	}

	return nil
}

func (s *DxmSelf) SelfAvatarDownloadSmall(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"
	err = s.Avatar.DownloadProcessedImage(aepr, "small", filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "", "SELF_AVATAR_NOT_FOUND")
	}
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadMedium(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"
	err = s.Avatar.DownloadProcessedImage(aepr, "medium", filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "", "SELF_AVATAR_NOT_FOUND")
	}
	return nil
}

func (s *DxmSelf) SelfAvatarDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	user, err := utils.GetVFromKV[utils.JSON](aepr.LocalData, "user")
	if err != nil {
		return err
	}
	userUid, err := utils.GetStringFromKV(user, "uid")
	if err != nil {
		return err
	}
	filename := userUid + ".webp"
	err = s.Avatar.DownloadProcessedImage(aepr, "big", filename)
	if err != nil {
		aepr.SuppressLogDump = true
		return aepr.WriteResponseAndLogAsErrorf(http.StatusBadRequest, "", "SELF_AVATAR_NOT_FOUND")
	}
	return nil
}

func (s *DxmSelf) SelfProfile(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	_, user, err := user_management.ModuleUserManagement.User.ShouldGetById(&aepr.Log, userId)
	if err != nil {
		return err
	}
	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{"data": utils.JSON{
		"user": user,
	}})
	return nil
}

func (s *DxmSelf) SelfProfileEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
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
	_, applicationNameId, err := aepr.GetParameterValueAsString("application_nameid")
	if err != nil {
		return err
	}
	_, fcmToken, err := aepr.GetParameterValueAsString("fcm_token")
	if err != nil {
		return err
	}
	_, deviceType, err := aepr.GetParameterValueAsString("device_type")
	if err != nil {
		return err
	}
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	err = push_notification.ModulePushNotification.FCM.RegisterUserToken(aepr, applicationNameId, deviceType, userId, fcmToken)
	if err != nil {
		return err
	}

	return nil
}

func (s *DxmSelf) SelfUserMessagePagingListAll(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	qb := user_management.ModuleUserManagement.UserMessage.NewTableSelectQueryBuilder()
	qb.Eq("user_id", userId)
	qb.OrderByDesc("id")
	err = user_management.ModuleUserManagement.UserMessage.DoRequestSearchPagingList(aepr, qb, nil)
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SelfUserMessageIsReadSetToTrue(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	_, userMessageId, err := aepr.GetParameterValueAsInt64("user_message_id")
	if err != nil {
		return err
	}
	_, _, err = user_management.ModuleUserManagement.UserMessage.ShouldSelectOne(&aepr.Log, nil, utils.JSON{
		"id":      userMessageId,
		"user_id": userId,
	}, nil, nil)
	if err != nil {
		return err
	}
	_, err = user_management.ModuleUserManagement.UserMessage.UpdateSimple(utils.JSON{
		"is_read": true,
	}, utils.JSON{
		"id":      userMessageId,
		"user_id": userId,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SelfUserMessageIsReadSetToTrueByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	_, userMessageUid, err := aepr.GetParameterValueAsString("user_message_uid")
	if err != nil {
		return err
	}
	_, userMessage, err := user_management.ModuleUserManagement.UserMessage.ShouldGetByUidAuto(&aepr.Log, userMessageUid)
	if err != nil {
		return err
	}
	// Verify the message belongs to the logged-in user
	messageUserId, ok := userMessage["user_id"].(int64)
	if !ok || messageUserId != userId {
		return aepr.WriteResponseAndNewErrorf(http.StatusForbidden, "",
			"USER_MESSAGE_NOT_FOUND_OR_NOT_OWNED")
	}
	_, err = user_management.ModuleUserManagement.UserMessage.UpdateSimple(utils.JSON{
		"is_read": true,
	}, utils.JSON{
		"uid":     userMessageUid,
		"user_id": userId,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SelfUserMessageAllIsReadSetToTrue(aepr *api.DXAPIEndPointRequest) (err error) {
	userId, err := utils.GetInt64FromKV(aepr.LocalData, "user_id")
	if err != nil {
		return err
	}
	_, err = user_management.ModuleUserManagement.UserMessage.UpdateSimple(utils.JSON{
		"is_read": true,
	}, utils.JSON{
		"user_id": userId,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *DxmSelf) SystemModeIsMaintenance() (isMaintenance bool) {
	globalStoreSystemValue, err := s.GlobalStoreRedis.Get(s.KeyGlobalStoreSystem)
	if err != nil {
		return false
	}
	modeAsAny, ok := globalStoreSystemValue[s.KeyGlobalStoreSystemMode]
	if !ok {
		// if no key set, that means Normal mode
		return false
	}
	modeValue, ok := modeAsAny.(string)
	if !ok {
		// if no key set, that means Normal mode
		return false
	}
	if modeValue != s.ValueGlobalStoreSystemModeMaintenance {
		// if not Maintenance mode, then Normal mode
		return false
	}

	return true
}

func (s *DxmSelf) SelfSystemModeIsMaintenance(aepr *api.DXAPIEndPointRequest) (err error) {
	isModeMaintenance := s.SystemModeIsMaintenance()
	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"value": isModeMaintenance,
	})
	return nil
}

func (s *DxmSelf) SelfSystemSetModeToMaintenance(aepr *api.DXAPIEndPointRequest) (err error) {
	err = s.GlobalStoreRedis.Set(s.KeyGlobalStoreSystem, utils.JSON{
		s.KeyGlobalStoreSystemMode: s.ValueGlobalStoreSystemModeMaintenance,
	}, 0)
	if err != nil {
		return err
	}
	if s.OnSystemSetToModeMaintenance != nil {
		err = s.OnSystemSetToModeMaintenance(&aepr.Log)
	}
	return nil
}

func (s *DxmSelf) SelfSystemSetModeToNormal(aepr *api.DXAPIEndPointRequest) (err error) {
	err = s.GlobalStoreRedis.Set(s.KeyGlobalStoreSystem, utils.JSON{
		s.KeyGlobalStoreSystemMode: s.ValueGlobalStoreSystemModeNormal,
	}, 0)
	if err != nil {
		return err
	}
	if s.OnSystemSetToModeNormal != nil {
		err = s.OnSystemSetToModeNormal(&aepr.Log)
	}
	return nil
}

var ModuleSelf = DxmSelf{
	UserOrganizationMembershipType: user_management.UserOrganizationMembershipTypeMultipleOrganizationPerUser,
}
