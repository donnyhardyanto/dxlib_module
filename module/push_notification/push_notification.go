package push_notification

import (
	"context"
	"database/sql"
	"firebase.google.com/go/v4/messaging"
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/database"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/messaging/fcm"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
	"math"
	"net/http"
	"sync"
	"time"
)

type DxmPushNotification struct {
	FCM     *FirebaseCloudMessaging
	EMail   *EmailMessaging
	SMS     *SMSMessaging
	Whatapp *WhatappMessaging
}

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

func (f *FirebaseCloudMessaging) DefineTables(databaseNameId string) {
	f.DatabaseNameId = databaseNameId
	f.FCMApplication = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_application",
		"push_notification.fcm_application",
		"push_notification.fcm_application", `nameid`, `id`)
	f.FCMUserToken = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_user_token",
		"push_notification.fcm_user_token",
		"push_notification.fcm_user_token", `id`, `id`)
	f.FCMMessage = table.Manager.NewTable(f.DatabaseNameId, "push_notification.fcm_message",
		"push_notification.fcm_message",
		"push_notification.v_fcm_message", `id`, `id`)
}

func (f *FirebaseCloudMessaging) ApplicationList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.List(aepr)
}

func (f *FirebaseCloudMessaging) ApplicationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = f.FCMApplication.DoCreate(aepr, map[string]interface{}{
		`nameid`:               aepr.ParameterValues[`nameid`].Value.(string),
		`service_account_data`: aepr.ParameterValues[`service_account_data`].Value.(utils.JSON),
	})
	return err
}

func (f *FirebaseCloudMessaging) ApplicationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.Read(aepr)
}

func (f *FirebaseCloudMessaging) ApplicationEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.Edit(aepr)
}

func (f *FirebaseCloudMessaging) ApplicationDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMApplication.SoftDelete(aepr)
}

func (f *FirebaseCloudMessaging) UserTokenList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.List(aepr)
}

/*func (f *FirebaseCloudMessaging) UserTokenCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = f.FCMUserToken.DoCreate(aepr, map[string]interface{}{
		`fcm_token`: aepr.ParameterValues[`fcm_token`].Value.(string),
		`user_id`:   aepr.ParameterValues[`user_id`].Value.(string),
	})
	return err
}*/

func (f *FirebaseCloudMessaging) UserTokenRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.Read(aepr)
}

func (f *FirebaseCloudMessaging) UserTokenHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMUserToken.HardDelete(aepr)
}

func (f *FirebaseCloudMessaging) MessageList(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.List(aepr)
}

/*func (f *FirebaseCloudMessaging) MessageCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = f.FCMMessage.DoCreate(aepr, map[string]interface{}{
		`application_id`: aepr.ParameterValues[`application_id`].Value.(string),
		`user_id`:        aepr.ParameterValues[`user_id`].Value.(string),
		`title`:          aepr.ParameterValues[`title`].Value.(string),
		`body`:           aepr.ParameterValues[`body`].Value.(string),
		`data`:           aepr.ParameterValues[`data`].Value.(utils.JSON),
	})
	return err
}*/

func (f *FirebaseCloudMessaging) MessageRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.Read(aepr)
}

func (f *FirebaseCloudMessaging) MessageHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return f.FCMMessage.HardDelete(aepr)
}

func (f *FirebaseCloudMessaging) RegisterUserToken(aepr *api.DXAPIEndPointRequest, applicationNameId string, userId int64, token string) (err error) {
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

	var userTokenId int64
	_, userToken, err := f.FCMUserToken.TxSelectOne(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
		"fcm_token":          token,
	}, nil)
	if err != nil {
		return err
	}
	if userToken == nil {
		userTokenId, err = f.FCMUserToken.TxInsert(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
			"fcm_token":          token,
		})
		if err != nil {
			return err
		}
	} else {
		userTokenId = userToken["id"].(int64)
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		`id`: userTokenId,
	})
	return nil
}

func (f *FirebaseCloudMessaging) SentToDevice(aepr *api.DXAPIEndPointRequest, applicationNameId string, userId int64, token string, msg fcm.Message) (err error) {
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
		"title":             msg.Title,
		"body":              msg.Body,
		"data":              msg.Data,
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		`fcm_message_id`: fcmMessageId,
	})
	return nil
}

func (f *FirebaseCloudMessaging) SentToUser(aepr *api.DXAPIEndPointRequest, applicationNameId string, userId string, msg fcm.Message) (err error) {
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

	_, userTokens, err := f.FCMUserToken.TxSelect(dtx, utils.JSON{
		"fcm_application_id": fcmApplicationId,
		"user_id":            userId,
	}, nil)
	if err != nil {
		return err
	}

	var fcmMessageIds []int64
	for _, userToken := range userTokens {
		fcmMessageId, err := f.FCMMessage.TxInsert(dtx, utils.JSON{
			"fcm_user_token_id": userToken["id"],
			"status":            "PENDING",
			"title":             msg.Title,
			"body":              msg.Body,
			"data":              msg.Data,
		})
		if err != nil {
			return err
		}
		fcmMessageIds = append(fcmMessageIds, fcmMessageId)
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		`fcm_message_ids`: fcmMessageIds,
	})

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
		serviceAccountData := fcmApplication["service_account_data"].(utils.JSON)

		_, err := fcm.Manager.StoreApplication(context.Background(), fcmApplicationId, serviceAccountData)
		if err != nil {
			log.Log.Warnf("ERROR_GET_FIREBASE_APP:%d:%v", fcmApplicationId, err)
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
		return fmt.Errorf("failed to get Firebase app: %v", err)
	}

	_, fcmMessages, err := f.FCMMessage.Select(&log.Log, nil, utils.JSON{
		"application_id": applicationId,
		"c1":             db.SQLExpression{Expression: "status = 'PENDING' OR status = 'FAILED'"},
		"c2":             db.SQLExpression{Expression: "(next_retry_time <= NOW()) or (next_retry_time IS NULL)"},
	}, nil, 100)
	if err != nil {
		return fmt.Errorf("failed to fetch messages: %v", err)
	}

	for _, fcmMessage := range fcmMessages {
		MsgNextRetryTime := fcmMessage["next_retry_time"].(time.Time)
		if MsgNextRetryTime.After(time.Now()) {
			continue // Skip messages that are not ready for retry
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			log.Log.Warnf("Rate limit wait error: %v", err)
			continue
		}
		retryCount := fcmMessage["retry_count"].(int)
		fcmMessageId := fcmMessage["id"].(int64)
		message := fcm.Message{
			Title: fcmMessage["title"].(string),
			Body:  fcmMessage["body"].(string),
			Data:  fcmMessage["data"].(map[string]string),
		}
		err = f.sendNotification(ctx, firebaseServiceAccount.Client, fcmMessage["token"].(string), fcmMessage["device_type"].(string), &message)
		if err != nil {
			log.Log.Warnf("Error sending notification %d: %v", fcmMessage["id"], err)
			retryCount++
			err = f.updateMessageStatus(fcmMessage["id"].(int64), "FAILED", retryCount)
		} else {
			err = f.updateMessageStatus(fcmMessage["id"].(int64), "SENT", retryCount)
		}
		if err != nil {
			log.Log.Warnf("Error updating message %d status: %v", fcmMessageId, err)
		}
	}

	return nil
}

func (f *FirebaseCloudMessaging) sendNotification(ctx context.Context, client *messaging.Client, token, deviceType string, msg *fcm.Message) error {
	message := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: msg.Title,
			Body:  msg.Body,
		},
		Data: msg.Data,
	}
	switch deviceType {
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
		return fmt.Errorf("UNKNOWN_DEVICE_TYPE: %s", deviceType)
	}

	_, err := client.Send(ctx, message)
	return err
}

func (f *FirebaseCloudMessaging) updateMessageStatus(messageId int64, status string, retryCount int) (err error) {
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

func (f *FirebaseCloudMessaging) calculateNextRetryTime(retryCount int) time.Time {
	delay := time.Duration(math.Min(float64(1*time.Hour), float64(5*time.Second)*math.Pow(2, float64(retryCount))))
	return time.Now().Add(delay)
}

var ModulePushNotification DxmPushNotification

func init() {
	ModulePushNotification = DxmPushNotification{
		FCM: &FirebaseCloudMessaging{},
	}
}
