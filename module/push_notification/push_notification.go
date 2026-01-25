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
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/database/db"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/messaging/fcm"
	"github.com/donnyhardyanto/dxlib/table"
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

type FCMMessageFunc func(dtx *database.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)
type FCMTopicMessageFunc func(dtx *database.DXDatabaseTx, l *log.DXLog, fcmTopicMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)

type FirebaseCloudMessaging struct {
	FCMApplication  *table.DXTable
	FCMUserToken    *table.DXTable
	FCMMessage      *table.DXTable
	FCMTopicMessage *table.DXTable
	DatabaseNameId  string
	Database        *database.DXDatabase
}

type EmailMessaging struct {
	EMailMessage   *table.DXTable
	DatabaseNameId string
}

type SMSMessaging struct {
	SMSMessage     *table.DXTable
	DatabaseNameId string
}

type WhatappMessaging struct {
	WAMessage      *table.DXTable
	DatabaseNameId string
}

func (f *FirebaseCloudMessaging) Init(databaseNameId string) {
	f.DatabaseNameId = databaseNameId
	f.Database = database.Manager.GetOrCreate(databaseNameId)
	// NewDXTable3Simple(databaseNameId, tableName, resultObjectName, listViewNameId, fieldNameForRowId, fieldNameForRowUid, fieldNameForRowNameId, responseEnvelopeObjectName)
	f.FCMApplication = table.NewDXTable3Simple(f.DatabaseNameId, "push_notification.fcm_application",
		"push_notification.fcm_application", "push_notification.fcm_application", "id", "uid", "nameid", "data")
	f.FCMUserToken = table.NewDXTable3Simple(f.DatabaseNameId, "push_notification.fcm_user_token",
		"push_notification.fcm_user_token", "push_notification.fcm_user_token", "id", "uid", "", "data")
	f.FCMMessage = table.NewDXTable3Simple(f.DatabaseNameId, "push_notification.fcm_message",
		"push_notification.fcm_message", "push_notification.v_fcm_message", "id", "uid", "", "data")
	f.FCMTopicMessage = table.NewDXTable3Simple(f.DatabaseNameId, "push_notification.fcm_topic_message",
		"push_notification.fcm_topic_message", "push_notification.fcm_topic_message", "id", "uid", "", "data")
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
	fcmApplicationId := fcmApplication["id"].(int64)

	_, existingUserTokens, err := f.FCMUserToken.TxSelect(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"fcm_token":          token,
	}, nil, nil, nil, nil)
	if err != nil {
		return err
	}

	for _, existingUserToken := range existingUserTokens {
		existingUserId := existingUserToken["user_id"].(int64)
		if existingUserId != userId {
			_, err = f.FCMUserToken.TxHardDelete(dtx, utils.JSON{
				"id": existingUserToken["id"].(int64),
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
		userTokenId = returningValues["id"].(int64)
	} else {
		userTokenId = userToken["id"].(int64)
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
	fcmApplicationId := fcmApplication["id"].(int64)

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
	fcmApplicationId := fcmApplication["id"].(int64)

	_, userToken, err := f.FCMUserToken.TxShouldSelectOne(dtx, nil, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
	}, nil, nil, nil)
	if err != nil {
		return err
	}
	userTokenId := userToken["id"].(int64)

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

	fcmApplicationId := fcmApplication["id"].(int64)

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

		fcmApplicationId := fcmApplication["id"].(int64)
		fcmApplicationNameId := fcmApplication["nameid"].(string)

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

		fcmApplicationId := fcmApplication["id"].(int64)
		fcmApplicationNameId := fcmApplication["nameid"].(string)

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
	fcmApplicationId := fcmApplication["id"].(int64)
	fcmApplicationServiceAccountSource := fcmApplication["service_account_source"].(string)
	serviceAccountData, err := utils.GetJSONFromKV(fcmApplication, "service_account_data")
	if err != nil {
		return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%v", fcmApplicationId, err)
	}

	switch fcmApplicationServiceAccountSource {
	case FcmServiceAccountSourceRaw:
		dataAsJSON = serviceAccountData
	case FcmServiceAccountSourceFile:
		serviceAccountFilename, err := utils.GetStringFromKV(serviceAccountData, "filename")
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%v", fcmApplicationId, err)

		}
		dataAsBytes, err := os.ReadFile(serviceAccountFilename)
		if err != nil {
			return nil, errors.Wrapf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%v", fcmApplicationId, err)
		}
		if err := json.Unmarshal(dataAsBytes, &dataAsJSON); err != nil {
			return nil, errors.Wrapf(err, "ERROR_PARSING_SERVICE_ACCOUNT_JSON:%d:%v", fcmApplicationId, err)
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
		fcmApplicationId := fcmApplication["id"].(int64)
		dataAsJSON, err := GetFCMApplicationServiceAccountData(fcmApplication)
		if err != nil {
			log.Log.Errorf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%v", fcmApplicationId, err)
			continue
		}
		_, err = fcm.Manager.StoreApplication(context.Background(), fcmApplicationId, dataAsJSON)
		if err != nil {
			log.Log.Warnf("ERROR_GET_FIREBASE_APP:%d:%v", fcmApplicationId, err)
			continue
		}
		go func() {
			defer wg.Done()
			fcmApplicationId := fcmApplication["id"].(int64)
			fcmApplicationNameId := fcmApplication["nameid"].(string)
			err := f.processSendTopic(fcmApplicationId)
			if err != nil {
				log.Log.Warnf("ERROR_PROCESSING_TOPIC_MESSAGES_FOR_SENDING_FROM_FCM_APPLICATION:%s:%v", fcmApplicationNameId, err)
			}
			err = f.processMessages(fcmApplicationId)
			if err != nil {
				log.Log.Warnf("ERROR_PROCESSING_MESSAGES_FOR_SENDING_FROM_FCM_APPLICAtiON:%s:%v", fcmApplicationNameId, err)
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

	_, fcmMessages, err := f.FCMMessage.Select(&log.Log, nil, utils.JSON{
		"fcm_application_id": applicationId,
		"c1":                 db.SQLExpression{Expression: fmt.Sprintf("((status = '%s') OR (status = '%s'))", StatusPending, StatusFailed)},
		"c2":                 db.SQLExpression{Expression: "((next_retry_time <= NOW()) or (next_retry_time IS NULL))"},
	}, nil, nil, 100, nil)
	if err != nil {
		return errors.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmMessage := range fcmMessages {
		fcmMessageId := fcmMessage["id"].(int64)
		log.Log.Debugf("Processing message %d", fcmMessageId)

		retryCount := fcmMessage["retry_count"].(int64)
		fcmToken := fcmMessage["fcm_token"].(string)
		deviceType := fcmMessage["device_type"].(string)
		msgTitle := fcmMessage["title"].(string)
		msgBody := fcmMessage["body"].(string)
		msgData := map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)}
		if msgDataTemp, ok := fcmMessage["data"].(map[string]string); ok {
			msgData = msgDataTemp
		}

		if fcmMessage["next_retry_time"] != nil {
			MsgNextRetryTime := fcmMessage["next_retry_time"].(time.Time)
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

	_, fcmTopicMessages, err := f.FCMTopicMessage.Select(&log.Log, nil, utils.JSON{
		"fcm_application_id": applicationId,
		"c1":                 db.SQLExpression{Expression: fmt.Sprintf("((status = '%s') OR (status = '%s'))", StatusPending, StatusFailed)},
		"c2":                 db.SQLExpression{Expression: "((next_retry_time <= NOW()) or (next_retry_time IS NULL))"},
	}, nil, nil, 100, nil)
	if err != nil {
		return errors.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmTopicMessage := range fcmTopicMessages {
		fcmMessageId := fcmTopicMessage["id"].(int64)
		log.Log.Debugf("Processing topic message %d", fcmMessageId)

		retryCount := fcmTopicMessage["retry_count"].(int64)
		fcmTopicMessageId := fcmTopicMessage["id"].(int64)
		msgTopic := fcmTopicMessage["topic"].(string)
		msgTitle := fcmTopicMessage["title"].(string)
		msgBody := fcmTopicMessage["body"].(string)
		msgData := map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)}
		if msgDataTemp, ok := fcmTopicMessage["data"].(map[string]string); ok {
			msgData = msgDataTemp
		}

		if fcmTopicMessage["next_retry_time"] != nil {
			MsgNextRetryTime := fcmTopicMessage["next_retry_time"].(time.Time)
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
