package push_notification

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/messaging/fcm"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/utils"
)

const (
	FcmServiceAccountSourceRaw   = "RAW"
	FcmServiceAccountSourceVault = "VAULT"
	FcmServiceAccountSourceFile  = "FILE"
	FcmServiceAccountSourceEnv   = "ENV"
)

var (
	FcmServiceAccountSourceValues = []string{
		FcmServiceAccountSourceRaw,
		FcmServiceAccountSourceFile,
		FcmServiceAccountSourceVault,
		FcmServiceAccountSourceEnv,
	}
)

var (
	FCMMessageMaxRetryAttemptCount        int64 = 10    // Maximum number of retry attempts
	FCMMessageExpirationInSeconds         int64 = 86400 // Messages expire after 24 hours
	FCMMessageMaxRetryDelayInSeconds      int64 = 3600
	FCMTopicMessageMaxRetryAttemptCount   int64 = 10    // Maximum number of retry attempts
	FCMTopicMessageExpirationInSeconds    int64 = 86400 // Messages expire after 24 hours
	FCMTopicMessageMaxRetryDelayInSeconds int64 = 3600
)

const (
	StatusPending         = "PENDING"
	StatusSent            = "SENT"
	StatusFailed          = "FAILED"
	StatusFailedPermanent = "FAILED_PERMANENT"
	StatusExpired         = "EXPIRED"
)

type DxmPushNotification struct {
	FCM     FirebaseCloudMessaging
	EMail   EmailMessaging
	SMS     SMSMessaging
	Whatapp WhatappMessaging
}

type FCMMessageFunc func(dtx *databases.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)
type FCMTopicMessageFunc func(dtx *databases.DXDatabaseTx, l *log.DXLog, fcmTopicMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)

type FirebaseCloudMessaging struct {
	FCMApplication  *tables.DXTable
	FCMUserToken    *tables.DXTable
	FCMMessage      *tables.DXTable
	FCMTopicMessage *tables.DXTable
	DatabaseNameId  string
	Database        *databases.DXDatabase
}

type EmailMessaging struct {
	EMailMessage   *tables.DXTable
	DatabaseNameId string
}

type SMSMessaging struct {
	SMSMessage     *tables.DXTable
	DatabaseNameId string
}

type WhatappMessaging struct {
	WAMessage      *tables.DXTable
	DatabaseNameId string
}

func (f *FirebaseCloudMessaging) Init(databaseNameId string) {
	f.DatabaseNameId = databaseNameId
	f.Database = databases.Manager.GetOrCreate(databaseNameId)
	f.FCMApplication = tables.NewDXTableSimple(f.DatabaseNameId,
		"push_notification.fcm_application", "push_notification.fcm_application", "push_notification.fcm_application",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "service_account_source", "id", "uid"},
		[]string{"id", "nameid", "created_at", "uid"},
		[]string{"id", "uid", "nameid", "service_account_source", "created_at", "last_modified_at", "is_deleted"},
	)
	f.FCMUserToken = tables.NewDXTableSimple(f.DatabaseNameId,
		"push_notification.fcm_user_token", "push_notification.fcm_user_token", "push_notification.v_fcm_user_token",
		"id", "uid", "", "data",
		nil,
		nil,
		// SearchTextFieldNames — string fields only, no id/uid/*_id/*_uid
		[]string{"fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid"},
		// OrderByFieldNames — all fields returned to client, uid last
		[]string{"id", "user_id", "fcm_application_id", "fcm_token", "device_type", "is_deleted", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid", "uid"},
		// FilterableFieldNames — superset of OrderByFieldNames
		[]string{"id", "uid", "user_id", "fcm_application_id", "fcm_token", "device_type", "is_deleted", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid"},
	)
	f.FCMMessage = tables.NewDXTableSimple(f.DatabaseNameId,
		"push_notification.fcm_message", "push_notification.fcm_message", "push_notification.v_fcm_message",
		"id", "uid", "", "data",
		nil,
		nil,
		// SearchTextFieldNames — string fields only, no id/uid/*_id/*_uid
		[]string{"status", "title", "body", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid"},
		// OrderByFieldNames — all fields returned to client, uid last
		[]string{"id", "fcm_user_token_id", "fcm_application_id", "status", "title", "body", "data", "next_retry_time", "retry_count", "is_read", "is_deleted", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid", "uid"},
		// FilterableFieldNames — superset of OrderByFieldNames
		[]string{"id", "uid", "fcm_user_token_id", "user_id", "fcm_application_id", "status", "title", "body", "data", "next_retry_time", "is_read", "is_deleted", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid"},
	)
	f.FCMTopicMessage = tables.NewDXTableSimple(f.DatabaseNameId,
		"push_notification.fcm_topic_message", "push_notification.fcm_topic_message", "push_notification.fcm_topic_message",
		"id", "uid", "", "data",
		nil,
		nil,
		[]string{"fcm_application_id", "topic", "status", "title", "body", "id", "uid"},
		[]string{"id", "fcm_application_id", "topic", "status", "retry_count", "created_at", "uid"},
		[]string{"id", "uid", "fcm_application_id", "topic", "status", "created_at", "last_modified_at"},
	)
}

func (f *FirebaseCloudMessaging) ApplicationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, nameId, err := aepr.GetParameterValueAsString("nameid")
	if err != nil {
		return err
	}
	_, serviceAccountSource, err := aepr.GetParameterValueAsString("service_account_source")
	if err != nil {
		return nil
	}
	if !utils.TsIsContain[string](FcmServiceAccountSourceValues, serviceAccountSource) {
		return errors.Errorf("INVALID_FCM_SERVICE_ACCOUNT_SOURCE_VALUE:%v", serviceAccountSource)
	}
	_, serviceAccountData, err := aepr.GetParameterValueAsJSON("service_account_data")
	if err != nil {
		return err
	}

	serviceAccountDataAsBytes, err := json.Marshal(serviceAccountData)
	if err != nil {
		return errors.New(fmt.Sprintf("ERROR_CONVERTING_SERVICE_ACCOUNT_DATA:%+v", err))
	}

	_, err = f.FCMApplication.DoCreate(aepr, map[string]interface{}{
		"nameid":                 nameId,
		"service_account_source": serviceAccountSource,
		"service_account_data":   serviceAccountDataAsBytes,
	})
	if err != nil {
		return err
	}

	return nil
}

func (f *FirebaseCloudMessaging) RegisterUserToken(aepr *api.DXAPIEndPointRequest, applicationNameId string, deviceType string, userId int64, token string) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}
	fcmApplicationId, ok := fcmApplication["id"].(int64)
	if !ok {
		return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
	}

	_, existingUserTokens, err := f.FCMUserToken.TxSelect(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"fcm_token":          token,
	}, nil, nil, nil, nil)
	if err != nil {
		return err
	}

	for _, existingUserToken := range existingUserTokens {
		existingUserId, ok := existingUserToken["user_id"].(int64)
		if !ok {
			continue
		}
		if existingUserId != userId {
			existingUserTokenId, ok := existingUserToken["id"].(int64)
			if !ok {
				continue
			}
			_, err = f.FCMUserToken.TxHardDelete(dtx, utils.JSON{
				"id": existingUserTokenId,
			})
			if err != nil {
				return err
			}
		}
	}

	var userTokenId int64

	_, userToken, err := f.FCMUserToken.TxSelectOne(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
		"device_type":        deviceType,
	}, nil, nil, nil)
	if err != nil {
		return err
	}
	if userToken == nil {
		_, returningValues, err := f.FCMUserToken.TxInsert(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
			"fcm_token":          token,
			"device_type":        deviceType,
		}, []string{"id"})
		if err != nil {
			return err
		}
		userTokenId, ok = returningValues["id"].(int64)
		if !ok {
			return errors.New("RETURNING_USER_TOKEN_ID_TYPE_ASSERTION_FAILED")
		}
	} else {
		userTokenId, ok = userToken["id"].(int64)
		if !ok {
			return errors.New("USER_TOKEN_ID_TYPE_ASSERTION_FAILED")
		}
	}

	if err = dtx.Commit(); err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"data": utils.JSON{
			"id": userTokenId,
		}})
	return nil
}

func (f *FirebaseCloudMessaging) SendTopic(l *log.DXLog, applicationNameId string, topic string, msgTitle string, msgBody string, msgData map[string]string, onFCMTopicMessage FCMTopicMessageFunc) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}
	fcmApplicationId, ok := fcmApplication["id"].(int64)
	if !ok {
		return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
	}

	msgDataAsString, err := json.Marshal(msgData)
	if err != nil {
		return err
	}

	fcmTopicMessageId, err := f.FCMTopicMessage.TxInsertReturningId(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"status":             StatusPending,
		"topic":              topic,
		"title":              msgTitle,
		"body":               msgBody,
		"data":               msgDataAsString,
	})
	if err != nil {
		return err
	}

	if onFCMTopicMessage != nil {
		err = onFCMTopicMessage(dtx, l, fcmTopicMessageId, fcmApplicationId, applicationNameId)
		if err != nil {
			return err
		}
	}

	return dtx.Commit()
}

func (f *FirebaseCloudMessaging) SendToDevice(l *log.DXLog, applicationNameId string, userId int64, token string, msgTitle string, msgBody string, msgData map[string]string, onFCMMessage FCMMessageFunc) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}
	fcmApplicationId, ok := fcmApplication["id"].(int64)
	if !ok {
		return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
	}

	_, userToken, err := f.FCMUserToken.TxShouldSelectOne(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
	}, nil, nil, nil)
	if err != nil {
		return err
	}
	userTokenId, ok := userToken["id"].(int64)
	if !ok {
		return errors.New("USER_TOKEN_ID_TYPE_ASSERTION_FAILED")
	}

	msgDataAsString, err := json.Marshal(msgData)
	if err != nil {
		return err
	}
	fcmMessageId, err := f.FCMMessage.TxInsertReturningId(dtx, utils.JSON{
		"fcm_user_token_id": userTokenId,
		"status":            StatusPending,
		"title":             msgTitle,
		"body":              msgBody,
		"data":              msgDataAsString,
	})
	if err != nil {
		return err
	}

	if onFCMMessage != nil {
		err = onFCMMessage(dtx, l, fcmMessageId, fcmApplicationId, applicationNameId)
		if err != nil {
			return err
		}
	}

	return dtx.Commit()
}

func (f *FirebaseCloudMessaging) SendToUser(l *log.DXLog, applicationNameId string, userId int64, msgTitle string, msgBody string, msgData map[string]string, onFCMMessage FCMMessageFunc) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}

	fcmApplicationId, ok := fcmApplication["id"].(int64)
	if !ok {
		return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
	}

	_, userTokens, err := f.FCMUserToken.TxSelect(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
	}, nil, nil, nil, nil)
	if err != nil {
		return err
	}

	msgDataAsJSONString, err := json.Marshal(msgData)
	if err != nil {
		return err
	}

	var fcmMessageIds []int64
	for _, userToken := range userTokens {
		fcmMessageId, err := f.FCMMessage.InsertReturningId(l, utils.JSON{
			"fcm_user_token_id": userToken["id"],
			"status":            StatusPending,
			"title":             msgTitle,
			"body":              msgBody,
			"data":              msgDataAsJSONString,
		})
		if err != nil {
			return err
		}
		fcmMessageIds = append(fcmMessageIds, fcmMessageId)

		if onFCMMessage != nil {
			err = onFCMMessage(dtx, l, fcmMessageId, fcmApplicationId, applicationNameId)
			if err != nil {
				return err
			}
		}
	}

	return dtx.Commit()
}

func (f *FirebaseCloudMessaging) RequestCreateTestMessageToUser(aepr *api.DXAPIEndPointRequest) (err error) {
	isApplicationNameIdExist, applicationNameId, err := aepr.GetParameterValueAsString("application_nameid")
	if err != nil {
		return err
	}
	if !isApplicationNameIdExist {
		return errors.New("application_nameid is required")
	}

	isUserIdExist, userId, err := aepr.GetParameterValueAsInt64("user_id")
	if err != nil {
		return err
	}
	if !isUserIdExist {
		return errors.New("user_id is required")
	}

	_, msgTitle, err := aepr.GetParameterValueAsString("msg_title")
	if err != nil {
		return err
	}
	_, msgBody, err := aepr.GetParameterValueAsString("msg_body")
	if err != nil {
		return err
	}

	_, msgDataRaw, err := aepr.GetParameterValueAsJSON("msg_data")
	if err != nil {
		return err
	}
	msgData := make(map[string]string)
	for k, v := range msgDataRaw {
		if str, ok := v.(string); ok {
			msgData[k] = str
		} else {
			msgData[k] = fmt.Sprintf("%v", v)
		}
	}

	err = f.SendToUser(&aepr.Log, applicationNameId, userId, msgTitle, msgBody, msgData, nil)
	if err != nil {
		return errors.Errorf("failed to send test message: %+v", err)
	}

	return nil

}

func (f *FirebaseCloudMessaging) AllApplicationSendToUser(l *log.DXLog, userId int64, msgTitle string, msgBody string, msgData map[string]string, onFCMMessage FCMMessageFunc) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplications, err := f.FCMApplication.SelectAll(l)
	if err != nil {
		return err
	}

	msgDataAsJSONString, err := json.Marshal(msgData)
	if err != nil {
		return errors.Wrap(err, "FAILED_TO_MARSHAL_MSG_DATA")
	}

	for _, fcmApplication := range fcmApplications {

		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}
		fcmApplicationNameId, ok := fcmApplication["nameid"].(string)
		if !ok {
			return errors.New("FCM_APPLICATION_NAMEID_TYPE_ASSERTION_FAILED")
		}

		_, userTokens, err := f.FCMUserToken.TxSelect(dtx, nil, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
		}, nil, nil, nil, nil)
		if err != nil {
			return err
		}

		var fcmMessageIds []int64
		for _, userToken := range userTokens {
			fcmMessageId, err := f.FCMMessage.TxInsertReturningId(dtx, utils.JSON{
				"fcm_user_token_id": userToken["id"],
				"status":            StatusPending,
				"title":             msgTitle,
				"body":              msgBody,
				"data":              msgDataAsJSONString,
			})
			if err != nil {
				return err
			}
			fcmMessageIds = append(fcmMessageIds, fcmMessageId)

			if onFCMMessage != nil {
				err = onFCMMessage(dtx, l, fcmMessageId, fcmApplicationId, fcmApplicationNameId)
				if err != nil {
					return err
				}
			}
		}
	}

	return dtx.Commit()
}
func (f *FirebaseCloudMessaging) AllApplicationSendTopic(l *log.DXLog, topic string, msgTitle string, msgBody string, msgData map[string]string, onFCMTopicMessage FCMTopicMessageFunc) (err error) {
	if err = f.Database.EnsureConnection(); err != nil {
		return err
	}

	dtx, err := f.Database.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Rollback()

	_, fcmApplications, err := f.FCMApplication.SelectAll(l)
	if err != nil {
		return err
	}

	msgDataAsJSONString, err := json.Marshal(msgData)
	if err != nil {
		return errors.Wrap(err, "FAILED_TO_MARSHAL_MSG_DATA")
	}

	for _, fcmApplication := range fcmApplications {

		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}
		fcmApplicationNameId, ok := fcmApplication["nameid"].(string)
		if !ok {
			return errors.New("FCM_APPLICATION_NAMEID_TYPE_ASSERTION_FAILED")
		}

		fcmTopicMessageId, err := f.FCMTopicMessage.TxInsertReturningId(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"status":             StatusPending,
			"topic":              topic,
			"title":              msgTitle,
			"body":               msgBody,
			"data":               msgDataAsJSONString,
		})
		if err != nil {
			return err
		}

		if onFCMTopicMessage != nil {
			err = onFCMTopicMessage(dtx, l, fcmTopicMessageId, fcmApplicationId, fcmApplicationNameId)
			if err != nil {
				return err
			}
		}

	}

	return dtx.Commit()
}

func GetFCMApplicationServiceAccountData(fcmApplication utils.JSON) (dataAsJSON utils.JSON, err error) {
	fcmApplicationId, ok := fcmApplication["id"].(int64)
	if !ok {
		return nil, errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
	}
	fcmApplicationServiceAccountSource, ok := fcmApplication["service_account_source"].(string)
	if !ok {
		return nil, errors.Errorf("FCM_APPLICATION_SERVICE_ACCOUNT_SOURCE_TYPE_ASSERTION_FAILED:%d", fcmApplicationId)
	}
	serviceAccountData, err := utils.GetJSONFromKV(fcmApplication, "service_account_data")
	if err != nil {
		return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%+v", fcmApplicationId, err)
	}

	switch fcmApplicationServiceAccountSource {
	case FcmServiceAccountSourceRaw:
		dataAsJSON = serviceAccountData
	case FcmServiceAccountSourceFile:
		serviceAccountFilename, err := utils.GetStringFromKV(serviceAccountData, "filename")
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%+v", fcmApplicationId, err)

		}
		dataAsBytes, err := os.ReadFile(serviceAccountFilename)
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%+v", fcmApplicationId, err)
		}
		if err := json.Unmarshal(dataAsBytes, &dataAsJSON); err != nil {
			return nil, errors.Wrapf(err, "ERROR_PARSING_SERVICE_ACCOUNT_JSON:%d:%+v", fcmApplicationId, err)
		}
	case FcmServiceAccountSourceEnv:
		envVarName, err := utils.GetStringFromKV(serviceAccountData, "env_var_name")
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_ENV_VAR_NAME:%d:%v", fcmApplicationId, err)
		}
		envVarValue := os.Getenv(envVarName)
		if envVarValue == "" {
			return nil, errors.Errorf("ERROR_ENV_VAR_NOT_FOUND:%d:%s", fcmApplicationId, envVarName)
		}
		if err := json.Unmarshal([]byte(envVarValue), &dataAsJSON); err != nil {
			return nil, errors.Wrapf(err, "ERROR_PARSING_SERVICE_ACCOUNT_JSON_FROM_ENV:%d:%v", fcmApplicationId, err)
		}
	case FcmServiceAccountSourceVault:
		vaultVarName, err := utils.GetStringFromKV(serviceAccountData, "vault_var_name")
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_ENV_VAR_NAME:%d:%v", fcmApplicationId, err)
		}
		vaultVarValue := app.App.InitVault.GetStringOrDefault(vaultVarName, "")
		if err := json.Unmarshal([]byte(vaultVarValue), &dataAsJSON); err != nil {
			return nil, errors.Wrapf(err, "ERROR_PARSING_SERVICE_ACCOUNT_JSON_FROM_VAULT:%d:%v", fcmApplicationId, err)
		}
	default:
		return nil, errors.Errorf("UNKNOWN_SERVICE_ACCOUNT_SOURCE:%d:%s", fcmApplicationId, fcmApplicationServiceAccountSource)
	}
	return dataAsJSON, nil
}

func (f *FirebaseCloudMessaging) Execute() (err error) {

	_, fcmApplications, err := f.FCMApplication.SelectAll(&log.Log)
	if err != nil {
		log.Log.Warnf("Error fetching FirebaseCloudMessaging applications during refresh: %v", err)
		time.Sleep(1 * time.Minute)
		return
	}

	var wg sync.WaitGroup
	for _, fcmApplication := range fcmApplications {
		wg.Add(1)
		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			log.Log.Warnf("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
			wg.Done()
			continue
		}
		dataAsJSON, err := GetFCMApplicationServiceAccountData(fcmApplication)
		if err != nil {
			log.Log.Errorf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%+v", fcmApplicationId, err)
			wg.Done()
			continue
		}
		_, err = fcm.Manager.StoreApplication(context.Background(), fcmApplicationId, dataAsJSON)
		if err != nil {
			log.Log.Warnf("ERROR_GET_FIREBASE_APP:%d:%v", fcmApplicationId, err)
			wg.Done()
			continue
		}
		go func() {
			defer wg.Done()
			fcmApplicationId, ok := fcmApplication["id"].(int64)
			if !ok {
				log.Log.Warnf("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED_IN_GOROUTINE")
				return
			}
			fcmApplicationNameId, ok := fcmApplication["nameid"].(string)
			if !ok {
				log.Log.Warnf("FCM_APPLICATION_NAMEID_TYPE_ASSERTION_FAILED_IN_GOROUTINE:%d", fcmApplicationId)
				return
			}
			err := f.processSendTopic(fcmApplicationId)
			if err != nil {
				log.Log.Warnf("ERROR_PROCESSING_TOPIC_MESSAGES_FOR_SENDING_FROM_FCM_APPLICATION:%s:%+v", fcmApplicationNameId, err)
			}
			err = f.processMessages(fcmApplicationId)
			if err != nil {
				log.Log.Warnf("ERROR_PROCESSING_MESSAGES_FOR_SENDING_FROM_FCM_APPLICAtiON:%s:%+v", fcmApplicationNameId, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

func (f *FirebaseCloudMessaging) processMessages(applicationId int64) error {

	ctx := context.Background()

	firebaseServiceAccount, err := fcm.Manager.GetServiceAccount(applicationId)
	if err != nil {
		return errors.Errorf("failed to get Firebase app: %v", err)
	}

	// Use QueryBuilder for safe parameterized query
	qb := f.FCMMessage.NewTableSelectQueryBuilder()
	qb.Eq("fcm_application_id", applicationId)
	qb.InStrings("status", []string{StatusPending, StatusFailed})
	qb.And("((next_retry_time <= NOW()) or (next_retry_time IS NULL))")
	qb.Limit(100)

	_, fcmMessages, err := f.FCMMessage.SelectWithBuilder(&log.Log, qb)
	if err != nil {
		return errors.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmMessage := range fcmMessages {
		fcmMessageId, ok := fcmMessage["id"].(int64)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_ID_TYPE_ASSERTION_FAILED")
			continue
		}
		log.Log.Debugf("Processing message %d", fcmMessageId)

		retryCount, ok := fcmMessage["retry_count"].(int64)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_RETRY_COUNT_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		fcmToken, ok := fcmMessage["fcm_token"].(string)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_FCM_TOKEN_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		deviceType, ok := fcmMessage["device_type"].(string)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_DEVICE_TYPE_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgTitle, ok := fcmMessage["title"].(string)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_TITLE_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgBody, ok := fcmMessage["body"].(string)
		if !ok {
			log.Log.Warnf("FCM_MESSAGE_BODY_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgData := map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)}
		if msgDataTemp, ok := fcmMessage["data"].(map[string]string); ok {
			msgData = msgDataTemp
		}

		if fcmMessage["next_retry_time"] != nil {
			MsgNextRetryTime, ok := fcmMessage["next_retry_time"].(time.Time)
			if !ok {
				log.Log.Warnf("FCM_MESSAGE_NEXT_RETRY_TIME_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
				continue
			}
			if MsgNextRetryTime.After(time.Now()) {
				continue // Skip messages that are not ready for retry
			}
		}

		// Check if a message has exceeded the maximum retry count
		if retryCount >= FCMMessageMaxRetryAttemptCount {
			log.Log.Warnf("Message %d exceeded max retry count (%d), marking as permanently failed", fcmMessageId, FCMMessageMaxRetryAttemptCount)
			err = f.updateMessageStatus(fcmMessageId, StatusFailedPermanent, retryCount)
			if err != nil {
				log.Log.Warnf("Failed to update message status to FAILED_PERMANENT: %v", err)
			}
			continue
		}

		// Check if a message has expired
		if createdAt, ok := fcmMessage["created_at"].(time.Time); ok {
			expirationDuration := time.Duration(FCMMessageExpirationInSeconds) * time.Second
			if time.Since(createdAt) > expirationDuration {
				log.Log.Warnf("Message %d has expired (created: %v), marking as expired", fcmMessageId, createdAt)
				err = f.updateMessageStatus(fcmMessageId, StatusExpired, retryCount)
				if err != nil {
					log.Log.Warnf("Failed to update message status to EXPIRED: %v", err)
				}
				continue
			}
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			log.Log.Warnf("Rate limit wait error: %v", err)
			continue
		}

		err = f.sendNotificationWithErrorHandling(ctx, firebaseServiceAccount.Client, fcmToken, deviceType, msgTitle, msgBody, msgData, fcmMessageId, retryCount)
		if err != nil {
			log.Log.Warnf("ERROR SEND NOTIFICATION %d: %v", fcmMessageId, err)
			retryCount++
			err = f.updateMessageStatus(fcmMessageId, StatusFailed, retryCount)
		} else {
			log.Log.Warnf("SENT NOTIFICATION:%d", fcmMessageId)
			err = f.updateMessageStatus(fcmMessageId, StatusSent, retryCount)
		}
		if err != nil {
			log.Log.Warnf("ERROR UPDATING FCM MESSAGE ID %d STATUS: %+v", fcmMessageId, err)
		}
	}

	return nil
}

func (f *FirebaseCloudMessaging) processSendTopic(applicationId int64) error {
	ctx := context.Background()

	firebaseServiceAccount, err := fcm.Manager.GetServiceAccount(applicationId)
	if err != nil {
		return errors.Errorf("failed to get Firebase app: %v", err)
	}

	// Use QueryBuilder for safe parameterized query
	qb := f.FCMTopicMessage.NewTableSelectQueryBuilder()
	qb.Eq("fcm_application_id", applicationId)
	qb.InStrings("status", []string{StatusPending, StatusFailed})
	qb.And("((next_retry_time <= NOW()) or (next_retry_time IS NULL))")
	qb.Limit(100)

	_, fcmTopicMessages, err := f.FCMTopicMessage.SelectWithBuilder(&log.Log, qb)
	if err != nil {
		return errors.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmTopicMessage := range fcmTopicMessages {
		fcmMessageId, err := utils.GetInt64FromKV(fcmTopicMessage, "id")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_ID_TYPE_ASSERTION_FAILED")
			continue
		}
		log.Log.Debugf("Processing topic message %d", fcmMessageId)

		retryCount, err := utils.GetInt64FromKV(fcmTopicMessage, "retry_count")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_RETRY_COUNT_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		fcmTopicMessageId, err := utils.GetInt64FromKV(fcmTopicMessage, "id")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_ID_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgTopic, err := utils.GetStringFromKV(fcmTopicMessage, "topic")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_TOPIC_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgTitle, err := utils.GetStringFromKV(fcmTopicMessage, "title")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_TITLE_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgBody, err := utils.GetStringFromKV(fcmTopicMessage, "body")
		if err != nil {
			log.Log.Warnf("FCM_TOPIC_MESSAGE_BODY_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
			continue
		}
		msgData := map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)}
		if msgDataTemp, err := utils.GetVFromKV[map[string]string](fcmTopicMessage, "data"); err == nil {
			msgData = msgDataTemp
		}

		if fcmTopicMessage["next_retry_time"] != nil {
			MsgNextRetryTime, err := utils.GetVFromKV[time.Time](fcmTopicMessage, "next_retry_time")
			if err != nil {
				log.Log.Warnf("FCM_TOPIC_MESSAGE_NEXT_RETRY_TIME_TYPE_ASSERTION_FAILED:%d", fcmMessageId)
				continue
			}
			if MsgNextRetryTime.After(time.Now()) {
				continue // Skip messages that are not ready for retry
			}
		}

		// Check if a message has exceeded the maximum retry count
		if retryCount >= FCMTopicMessageMaxRetryAttemptCount {
			log.Log.Warnf("Topic message %d exceeded max retry count (%d), marking as permanently failed", fcmTopicMessageId, FCMMessageMaxRetryAttemptCount)
			err = f.updateTopicMessageStatus(fcmTopicMessageId, StatusFailedPermanent, retryCount)
			if err != nil {
				log.Log.Warnf("Failed to update topic message status to FAILED_PERMANENT: %v", err)
			}
			continue
		}

		// Check if a message has expired
		if createdAt, ok := fcmTopicMessage["created_at"].(time.Time); ok {
			expirationDuration := time.Duration(FCMTopicMessageExpirationInSeconds) * time.Second
			if time.Since(createdAt) > expirationDuration {
				log.Log.Warnf("Topic message %d has expired (created: %v), marking as expired", fcmTopicMessageId, createdAt)
				err = f.updateTopicMessageStatus(fcmTopicMessageId, StatusExpired, retryCount)
				if err != nil {
					log.Log.Warnf("Failed to update topic message status to EXPIRED: %v", err)
				}
				continue
			}
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			log.Log.Warnf("Rate limit wait error: %v", err)
			continue
		}

		err = f.sendTopicNotificationWithErrorHandling(ctx, firebaseServiceAccount.Client, msgTopic, msgTitle, msgBody, msgData, fcmTopicMessageId, retryCount)
		if err != nil {
			log.Log.Warnf("ERROR_SEND_TOPIC_NOTIFICATION:%d:%+v", fcmTopicMessageId, err)
			retryCount++
			err = f.updateTopicMessageStatus(fcmTopicMessageId, StatusFailed, retryCount)
		} else {
			log.Log.Warnf("SENT_TOPIC_NOTIFICATION:%d", fcmTopicMessageId)
			err = f.updateTopicMessageStatus(fcmTopicMessageId, StatusSent, retryCount)
		}
		if err != nil {
			log.Log.Warnf("ERROR_UPDATING_FCM_TOPIC_MESSAGE_ID %d STATUS: %+v", fcmTopicMessageId, err)
		}
	}

	return nil
}

// Add this new helper function for sending notifications with proper error handling
func (f *FirebaseCloudMessaging) sendNotificationWithErrorHandling(ctx context.Context, client *messaging.Client, token, deviceType, msgTitle, msgBody string, msgData map[string]string, fcmMessageId, retryCount int64) error {
	err := f.sendNotification(ctx, client, token, deviceType, msgTitle, msgBody, msgData)

	if err != nil {
		// Check if this is a permanent error that shouldn't be retried
		if isPermanentFCMError(err) {
			log.Log.Warnf("Permanent FCM error for message %d: %v, marking as permanently failed", fcmMessageId, err)
			return f.updateMessageStatus(fcmMessageId, StatusFailedPermanent, retryCount)
		}

		// Temporary error - retry
		log.Log.Warnf("Temporary error sending notification %d (attempt %d): %v", fcmMessageId, retryCount+1, err)
		retryCount++
		return f.updateMessageStatus(fcmMessageId, StatusFailed, retryCount)
	}

	// Success
	log.Log.Infof("Successfully sent notification: %d", fcmMessageId)
	return f.updateMessageStatus(fcmMessageId, StatusSent, retryCount)
}

func (f *FirebaseCloudMessaging) sendNotification(ctx context.Context, client *messaging.Client, token, deviceType string, msgTitle string, msgBody string, msgData map[string]string) error {
	message := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: msgTitle,
			Body:  msgBody,
		},
		Data: msgData,
	}
	switch strings.ToUpper(deviceType) {
	case "ANDROID":
		message.Android = &messaging.AndroidConfig{
			Priority: "high",
		}
	case "IOS":
		message.APNS = &messaging.APNSConfig{
			Headers: map[string]string{
				"apns-priority": "10",
			},
		}
	default:
		return errors.Errorf("UNKNOWN_DEVICE_TYPE: %s", deviceType)
	}

	_, err := client.Send(ctx, message)
	return err
}

// Add this new helper function for sending topic notifications with proper error handling
func (f *FirebaseCloudMessaging) sendTopicNotificationWithErrorHandling(ctx context.Context, client *messaging.Client, topic, msgTitle, msgBody string, msgData map[string]string, fcmTopicMessageId, retryCount int64) error {
	err := f.sendTopicNotification(ctx, client, topic, msgTitle, msgBody, msgData)

	if err != nil {
		// Check if this is a permanent error that shouldn't be retried
		if isPermanentFCMError(err) {
			log.Log.Warnf("Permanent FCM error for topic message %d: %v, marking as permanently failed", fcmTopicMessageId, err)
			return f.updateTopicMessageStatus(fcmTopicMessageId, StatusFailedPermanent, retryCount)
		}

		// Temporary error - retry
		log.Log.Warnf("Temporary error sending topic notification %d (attempt %d): %v", fcmTopicMessageId, retryCount+1, err)
		retryCount++
		return f.updateTopicMessageStatus(fcmTopicMessageId, StatusFailed, retryCount)
	}

	// Success
	log.Log.Infof("Successfully sent topic notification: %d", fcmTopicMessageId)
	return f.updateTopicMessageStatus(fcmTopicMessageId, StatusSent, retryCount)
}

func (f *FirebaseCloudMessaging) sendTopicNotification(ctx context.Context, client *messaging.Client, topic string, msgTitle string, msgBody string, msgData map[string]string) error {
	message := &messaging.Message{
		Notification: &messaging.Notification{
			Title: msgTitle,
			Body:  msgBody,
		},
		Topic: topic,
		Data:  msgData,
	}

	_, err := client.Send(ctx, message)
	return err
}

// Add this new helper function to determine if an FCM error is permanent
func isPermanentFCMError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific FCM error types that indicate permanent failures
	errStr := err.Error()

	// These error patterns indicate permanent failures that shouldn't be retried
	permanentErrorPatterns := []string{
		"registration-token-not-registered",
		"invalid-registration-token",
		"invalid-argument",
		"invalid-recipient",
		"invalid-package-name",
		"mismatched-credential",
		"invalid-apns-credentials",
		"unregistered",
		"InvalidRegistration",
		"NotRegistered",
		"InvalidPackageName",
		"MismatchSenderId",
		"not-found",
		"Requested entity was not found",
		"permission-denied",
		"unauthenticated",
		"authentication-error",
	}

	for _, pattern := range permanentErrorPatterns {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(pattern)) {
			return true
		}
	}

	// Firebase v4 uses standard error strings, check for HTTP status codes in error message
	// 400 Bad Request - Invalid arguments
	// 401 Unauthorized - Authentication issues
	// 403 Forbidden - Permission denied
	// 404 Not Found - Token not registered
	if strings.Contains(errStr, "400") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") {
		return true
	}

	return false
}

func (f *FirebaseCloudMessaging) updateMessageStatus(messageId int64, status string, retryCount int64) (err error) {
	p := utils.JSON{
		"status": status,
	}
	if status != StatusSent {
		nextRetryTime := f.calculateMessageNextRetryTime(retryCount)
		p["retry_count"] = retryCount
		p["next_retry_time"] = nextRetryTime
	}
	_, err = f.FCMMessage.UpdateSimple(p, utils.JSON{
		"id": messageId,
	})
	return err
}

func (f *FirebaseCloudMessaging) updateTopicMessageStatus(messageId int64, status string, retryCount int64) (err error) {
	p := utils.JSON{
		"status": status,
	}
	if status != StatusSent {
		nextRetryTime := f.calculateTopicMessageNextRetryTime(retryCount)
		p["retry_count"] = retryCount
		p["next_retry_time"] = nextRetryTime
	}
	_, err = f.FCMTopicMessage.UpdateSimple(p, utils.JSON{
		"id": messageId,
	})
	return err
}

// Update the calculateMessageNextRetryTime function using crypto/rand
func (f *FirebaseCloudMessaging) calculateMessageNextRetryTime(retryCount int64) time.Time {
	// Exponential backoff with jitter
	baseDelay := float64(5 * time.Second)
	maxDelay := float64(FCMMessageMaxRetryDelayInSeconds) * float64(time.Second)

	// Calculate exponential delay
	delay := baseDelay * math.Pow(2, float64(retryCount))

	// Cap at maximum delay
	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter (±10% randomization) using crypto/rand
	jitterRange := int64(delay * 0.1)

	// Generate random jitter between -jitterRange and +jitterRange
	maxJitter := big.NewInt(jitterRange * 2)
	randomBig, err := rand.Int(rand.Reader, maxJitter)
	if err != nil {
		// If a random generation fails, use no jitter
		return time.Now().Add(time.Duration(delay))
	}

	jitter := randomBig.Int64() - jitterRange
	finalDelay := time.Duration(delay) + time.Duration(jitter)

	return time.Now().Add(finalDelay)
}

// Update the calculateTopicMessageNextRetryTime function using crypto/rand
func (f *FirebaseCloudMessaging) calculateTopicMessageNextRetryTime(retryCount int64) time.Time {
	// Exponential backoff with jitter
	baseDelay := float64(5 * time.Second)
	maxDelay := float64(FCMTopicMessageMaxRetryDelayInSeconds) * float64(time.Second)

	// Calculate exponential delay
	delay := baseDelay * math.Pow(2, float64(retryCount))

	// Cap at maximum delay
	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter (±10% randomization) using crypto/rand
	jitterRange := int64(delay * 0.1)

	// Generate random jitter between -jitterRange and +jitterRange
	maxJitter := big.NewInt(jitterRange * 2)
	randomBig, err := rand.Int(rand.Reader, maxJitter)
	if err != nil {
		// If a random generation fails, use no jitter
		return time.Now().Add(time.Duration(delay))
	}

	jitter := randomBig.Int64() - jitterRange
	finalDelay := time.Duration(delay) + time.Duration(jitter)

	return time.Now().Add(finalDelay)
}

var ModulePushNotification DxmPushNotification

func init() {
	ModulePushNotification = DxmPushNotification{
		FCM: FirebaseCloudMessaging{},
	}
}
