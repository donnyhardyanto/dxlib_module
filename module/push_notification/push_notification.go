package push_notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/messaging/fcm"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/pkg/errors"
)

type DxmPushNotification struct {
	FCM     FirebaseCloudMessaging
	EMail   EmailMessaging
	SMS     SMSMessaging
	Whatapp WhatappMessaging
}

type FCMMessageFunc func(dtx *database.DXDatabaseTx, l *log.DXLog, fcmMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)
type FirebaseCloudMessaging struct {
	FCMApplication *table.DXTable
	FCMUserToken   *table.DXTable
	FCMMessage     *table.DXTable
	DatabaseNameId string
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
	f.FCMApplication = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_application",
		"push_notification.fcm_application",
		"push_notification.fcm_application", "nameid", "id", "uid", "data")
	f.FCMUserToken = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_user_token",
		"push_notification.fcm_user_token",
		"push_notification.fcm_user_token", "id", "id", "uid", "data")
	f.FCMMessage = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_message",
		"push_notification.fcm_message",
		"push_notification.v_fcm_message", "id", "id", "uid", "data")
}

/*func (f *FirebaseCloudMessaging) ApplicationRequestPagingList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.RequestPagingList(aepr)
}*/

func (f *FirebaseCloudMessaging) ApplicationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, nameId, err := aepr.GetParameterValueAsString("nameid")
	if err != nil {
		return err
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
		"nameid":               nameId,
		"service_account_data": serviceAccountDataAsBytes,
	})
	if err != nil {
		return err
	}

	//	aepr.WriteResponseAsJSON(http.StatusOK, nil, nil)
	return nil
}

/*func (f *FirebaseCloudMessaging) ApplicationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.RequestRead(aepr)
}*/

/*func (f *FirebaseCloudMessaging) ApplicationEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.RequestEdit(aepr)
}*/

/*
	func (f *FirebaseCloudMessaging) ApplicationDelete(aepr *api.DXAPIEndPointRequest) (err error) {
		return f.FCMApplication.RequestSoftDelete(aepr)
	}
*/
func (f *FirebaseCloudMessaging) UserTokenList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.RequestPagingList(aepr)
}

func (f *FirebaseCloudMessaging) UserTokenRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.RequestRead(aepr)
}

func (f *FirebaseCloudMessaging) UserTokenHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.RequestHardDelete(aepr)
}

func (f *FirebaseCloudMessaging) MessageList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.RequestPagingList(aepr)
}

func (f *FirebaseCloudMessaging) MessageRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.RequestRead(aepr)
}

func (f *FirebaseCloudMessaging) MessageHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.RequestHardDelete(aepr)
}

func (f *FirebaseCloudMessaging) RegisterUserToken(aepr *api.DXAPIEndPointRequest, applicationNameId string, deviceType string, userId int64, token string) (err error) {
	dbTaskDispatcher := database.Manager.Databases[f.DatabaseNameId]
	var dtx *database.DXDatabaseTx
	dtx, err = dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(&aepr.Log, err)

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}
	fcmApplicationId := fcmApplication["id"].(int64)

	_, existingUserTokens, err := f.FCMUserToken.TxSelect(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"fcm_token":          token,
	}, nil, nil)
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
	_, userToken, err := f.FCMUserToken.TxSelectOne(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
		"device_type":        deviceType,
	}, nil)
	if err != nil {
		return err
	}
	if userToken == nil {
		userTokenId, err = f.FCMUserToken.TxInsert(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
			"fcm_token":          token,
			"device_type":        deviceType,
		})
		if err != nil {
			return err
		}
	} else {
		userTokenId = userToken["id"].(int64)
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"data": utils.JSON{
			"id": userTokenId,
		}})
	return nil
}

func (f *FirebaseCloudMessaging) SendToDevice(l *log.DXLog, applicationNameId string, userId int64, token string, msgTitle string, msgBody string, msgData map[string]string, onFCMMessage FCMMessageFunc) (err error) {
	dbTaskDispatcher := database.Manager.Databases[f.DatabaseNameId]
	var dtx *database.DXDatabaseTx
	dtx, err = dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(l, err)

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}
	fcmApplicationId := fcmApplication["id"].(int64)

	_, userToken, err := f.FCMUserToken.TxShouldSelectOne(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
	}, nil)
	if err != nil {
		return err
	}
	userTokenId := userToken["id"].(int64)

	fcmMessageId, err := f.FCMMessage.TxInsert(dtx, utils.JSON{
		"fcm_user_token_id": userTokenId,
		"status":            "PENDING",
		"title":             msgTitle,
		"body":              msgBody,
		"data":              msgData,
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
	return nil
}

func (f *FirebaseCloudMessaging) SendToUser(l *log.DXLog, applicationNameId string, userId int64, msgTitle string, msgBody string, msgData map[string]string, onFCMMessage FCMMessageFunc) (err error) {
	dbTaskDispatcher := database.Manager.Databases[f.DatabaseNameId]
	var dtx *database.DXDatabaseTx
	dtx, err = dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(l, err)

	_, fcmApplication, err := f.FCMApplication.TxShouldGetByNameId(dtx, applicationNameId)
	if err != nil {
		return err
	}

	fcmApplicationId := fcmApplication["id"].(int64)

	_, userTokens, err := f.FCMUserToken.TxSelect(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
	}, nil, nil)
	if err != nil {
		return err
	}

	msgDataAsJSONString, err := json.Marshal(msgData)
	if err != nil {
		return err
	}

	var fcmMessageIds []int64
	for _, userToken := range userTokens {
		fcmMessageId, err := f.FCMMessage.Insert(l, utils.JSON{
			"fcm_user_token_id": userToken["id"],
			"status":            "PENDING",
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

	return nil
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
	dbTaskDispatcher := database.Manager.Databases[f.DatabaseNameId]
	var dtx *database.DXDatabaseTx
	dtx, err = dbTaskDispatcher.TransactionBegin(sql.LevelReadCommitted)
	if err != nil {
		return err
	}
	defer dtx.Finish(l, err)

	_, fcmApplications, err := f.FCMApplication.SelectAll(l)
	if err != nil {
		return err
	}

	for _, fcmApplication := range fcmApplications {

		fcmApplicationId := fcmApplication["id"].(int64)
		fcmApplicationNameId := fcmApplication["nameid"].(string)

		_, userTokens, err := f.FCMUserToken.TxSelect(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
		}, nil, nil)
		if err != nil {
			return err
		}

		var fcmMessageIds []int64
		for _, userToken := range userTokens {
			fcmMessageId, err := f.FCMMessage.TxInsert(dtx, utils.JSON{
				"fcm_user_token_id": userToken["id"],
				"status":            "PENDING",
				"title":             msgTitle,
				"body":              msgBody,
				"data":              msgData,
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

	return nil
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
		serviceAccountData, err := utils.GetJSONFromKV(fcmApplication, "service_account_data")
		if err != nil {
			log.Log.Errorf(err, "ERROR_GET_SERVICE_ACCOUNT_DATA:%d:%v", fcmApplicationId, err)
			continue
		}
		_, err = fcm.Manager.StoreApplication(context.Background(), fcmApplicationId, serviceAccountData)
		if err != nil {
			log.Log.Warnf("ERROR_GET_FIREBASE_APP:%d:%v", fcmApplicationId, err)
			continue
		}
		go func() {
			defer wg.Done()
			fcmApplicationId := fcmApplication["id"].(int64)
			err := f.processMessages(fcmApplicationId)
			if err != nil {
				log.Log.Warnf("Error processing messages for fcmApplication %s: %v", fcmApplication["nameid"], err)
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
		"c1":                 db.SQLExpression{Expression: "((status = 'PENDING') OR (status = 'FAILED'))"},
		"c2":                 db.SQLExpression{Expression: "((next_retry_time <= NOW()) or (next_retry_time IS NULL))"},
	}, nil, nil, 100)
	if err != nil {
		return errors.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmMessage := range fcmMessages {
		log.Log.Debugf("Processing message %d", fcmMessage["id"])

		if fcmMessage["next_retry_time"] != nil {
			MsgNextRetryTime := fcmMessage["next_retry_time"].(time.Time)
			if MsgNextRetryTime.After(time.Now()) {
				continue // Skip messages that are not ready for retry
			}
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			log.Log.Warnf("Rate limit wait error: %v", err)
			continue
		}
		retryCount := fcmMessage["retry_count"].(int64)
		fcmMessageId := fcmMessage["id"].(int64)
		fcmToken := fcmMessage["fcm_token"].(string)
		deviceType := fcmMessage["device_type"].(string)
		msgTitle := fcmMessage["title"].(string)
		msgBody := fcmMessage["body"].(string)
		msgData := map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)}
		if msgDataTemp, ok := fcmMessage["data"].(map[string]string); ok {
			msgData = msgDataTemp
		}
		err = f.sendNotification(ctx, firebaseServiceAccount.Client, fcmToken, deviceType, msgTitle, msgBody, msgData)
		if err != nil {
			log.Log.Warnf("Error sending notification %d: %v", fcmMessageId, err)
			retryCount++
			err = f.updateMessageStatus(fcmMessage["id"].(int64), "FAILED", retryCount)
		} else {
			log.Log.Warnf("Notification sent %d", fcmMessageId)
			err = f.updateMessageStatus(fcmMessage["id"].(int64), "SENT", retryCount)
		}
		if err != nil {
			log.Log.Warnf("Error updating message %d status: %v", fcmMessageId, err)
		}
	}

	return nil
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

func (f *FirebaseCloudMessaging) updateMessageStatus(messageId int64, status string, retryCount int64) (err error) {
	p := utils.JSON{
		"status": status,
	}
	if status != "SENT" {
		nextRetryTime := f.calculateNextRetryTime(retryCount)
		p["retry_count"] = retryCount
		p["next_retry_time"] = nextRetryTime
	}
	_, err = f.FCMMessage.Update(p, utils.JSON{
		"id": messageId,
	})
	return err
}

func (f *FirebaseCloudMessaging) calculateNextRetryTime(retryCount int64) time.Time {
	delay := time.Duration(math.Min(float64(1*time.Hour), float64(5*time.Second)*math.Pow(2, float64(retryCount))))
	return time.Now().Add(delay)
}

var ModulePushNotification DxmPushNotification

func init() {
	ModulePushNotification = DxmPushNotification{
		FCM: FirebaseCloudMessaging{},
	}
}
