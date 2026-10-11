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

	"firebase.google.com/go/v4/errorutils"
	"firebase.google.com/go/v4/messaging"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/databases"
	db "github.com/donnyhardyanto/dxlib/databases/db"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/messaging/fcm"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/types"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/base"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	// StatusSending marks a message one worker has claimed and is sending. Its
	// next_retry_time holds the end of the claim; past that, another worker may
	// take the message over (the first one crashed or hung).
	StatusSending = "SENDING"
)

// FCMClaimLease is how long a claimed message stays reserved for its worker.
var FCMClaimLease = 5 * time.Minute

type DxmPushNotification struct {
	FCM     FirebaseCloudMessaging
	EMail   EmailMessaging
	SMS     SMSMessaging
	Whatapp WhatappMessaging
}

type FCMTopicMessageFunc func(dtx *databases.DXDatabaseTx, l *log.DXLog, fcmTopicMessageId int64, fcmApplicationId int64, fcmApplicationNameId string) (err error)

type FirebaseCloudMessaging struct {
	FCMApplication  *tables.DXRawTable
	FCMUserToken    *tables.DXRawTable
	FCMMessage      *tables.DXRawTable
	FCMTopicMessage *tables.DXRawTable
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
	f.FCMApplication = tables.NewDXRawTableSimple(f.DatabaseNameId,
		"push_notification.fcm_application", "push_notification.fcm_application", "push_notification.fcm_application",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "service_account_source", "id", "uid"},
		[]string{"id", "nameid", "created_at", "uid"},
		[]string{"id", "uid", "nameid", "service_account_source", "created_at", "last_modified_at"},
	)
	f.FCMUserToken = tables.NewDXRawTableSimple(f.DatabaseNameId,
		"push_notification.fcm_user_token", "push_notification.fcm_user_token", "push_notification.v_fcm_user_token",
		"id", "uid", "", "data",
		nil,
		nil,
		// SearchTextFieldNames — string fields only, no id/uid/*_id/*_uid
		[]string{"fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid"},
		// OrderByFieldNames — all fields returned to client, uid last
		[]string{"id", "user_id", "fcm_application_id", "fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid", "uid"},
		// FilterableFieldNames — superset of OrderByFieldNames
		[]string{"id", "uid", "user_id", "fcm_application_id", "fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid"},
	)
	f.FCMMessage = tables.NewDXRawTableSimple(f.DatabaseNameId,
		"push_notification.fcm_message", "push_notification.fcm_message", "push_notification.v_fcm_message",
		"id", "uid", "", "data",
		nil,
		nil,
		// SearchTextFieldNames — string fields only, no id/uid/*_id/*_uid
		[]string{"status", "title", "body", "fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid"},
		// OrderByFieldNames — all fields returned to client, uid last
		[]string{"id", "fcm_user_token_id", "user_id", "fcm_application_id", "fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "status", "title", "body", "data", "user_message_id", "next_retry_time", "retry_count", "is_read", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid", "uid"},
		// FilterableFieldNames — superset of OrderByFieldNames
		[]string{"id", "uid", "fcm_user_token_id", "user_id", "fcm_application_id", "fcm_token", "device_type", "user_loginid", "user_fullname", "fcm_application_nameid", "status", "title", "body", "data", "user_message_id", "next_retry_time", "is_read", "created_at", "created_by_user_id", "created_by_user_nameid", "last_modified_at", "last_modified_by_user_id", "last_modified_by_user_nameid"},
	)
	f.FCMMessage.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"data": types.APIParameterTypeMapStringString,
	}
	f.FCMTopicMessage = tables.NewDXRawTableSimple(f.DatabaseNameId,
		"push_notification.fcm_topic_message", "push_notification.fcm_topic_message", "push_notification.fcm_topic_message",
		"id", "uid", "", "data",
		nil,
		nil,
		[]string{"fcm_application_id", "topic", "status", "title", "body", "id", "uid"},
		[]string{"id", "fcm_application_id", "topic", "status", "retry_count", "created_at", "uid"},
		[]string{"id", "uid", "fcm_application_id", "topic", "status", "created_at", "last_modified_at"},
	)
	f.FCMTopicMessage.FieldTypeMapping = db.DXDatabaseTableFieldTypeMapping{
		"data": types.APIParameterTypeMapStringString,
	}
}

// txGetApplicationByNameId returns the FCM application row, or a
// base.NotFoundError (code FCM_APPLICATION_NOT_FOUND) naming the nameid.
func (f *FirebaseCloudMessaging) txGetApplicationByNameId(dtx *databases.DXDatabaseTx, applicationNameId string) (utils.JSON, error) {
	_, fcmApplication, err := f.FCMApplication.TxGetByNameId(dtx, applicationNameId)
	if err != nil {
		return nil, errors.Wrapf(err, "look up FCM application nameid %q", applicationNameId)
	}
	if fcmApplication == nil {
		return nil, base.NewNotFoundError("FCM_APPLICATION_NOT_FOUND", "FCM application", map[string]any{
			"nameid": applicationNameId,
		})
	}
	return fcmApplication, nil
}

// FCMTokenFingerprint names an FCM registration token in logs and errors
// without giving it away (see base.Fingerprint).
func FCMTokenFingerprint(token string) string {
	return base.Fingerprint(token)
}

func (f *FirebaseCloudMessaging) ApplicationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, nameId, err := aepr.GetParameterValueAsString("nameid")
	if err != nil {
		return err
	}
	_, serviceAccountSource, err := aepr.GetParameterValueAsString("service_account_source")
	if err != nil {
		return err
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
	deviceType = strings.ToUpper(deviceType)

	var userTokenId int64

	err = f.Database.Tx(aepr.Context, &aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		fcmApplication, err := f.txGetApplicationByNameId(dtx, applicationNameId)
		if err != nil {
			return err
		}
		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}

		// Atomic upsert on UNIQUE (fcm_application_id, fcm_token) — handles
		// concurrent same-token registration and device-swap (user_id change)
		// without a TOCTOU window. DB serializes conflicting rows on the
		// unique index.
		_, rowId, _, err := f.FCMUserToken.TxUpsert(dtx,
			utils.JSON{
				"user_id":          userId,
				"device_type":      deviceType,
				"last_modified_at": time.Now().UTC(),
			},
			utils.JSON{
				"fcm_application_id": fcmApplicationId,
				"fcm_token":          token,
			},
		)
		if err != nil {
			return err
		}
		userTokenId = rowId

		return nil
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{
		"data": utils.JSON{
			"id": userTokenId,
		}})
	return nil
}

func (f *FirebaseCloudMessaging) UnregisterUserToken(aepr *api.DXAPIEndPointRequest, applicationNameId string, userId int64, fcmToken string) (err error) {
	err = f.Database.Tx(aepr.Context, &aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		fcmApplication, err := f.txGetApplicationByNameId(dtx, applicationNameId)
		if err != nil {
			return err
		}
		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}

		_, err = f.FCMUserToken.TxHardDelete(dtx, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
			"fcm_token":          fcmToken,
		})
		return err
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{})
	return nil
}

func (f *FirebaseCloudMessaging) ReregisterUserToken(aepr *api.DXAPIEndPointRequest, userId int64, oldFcmToken string, newFcmToken string) (err error) {
	err = f.Database.Tx(aepr.Context, &aepr.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		// Lock the existing (user, oldToken) row and capture app_id + device_type
		// so the atomic claim below is app-scoped and preserves device info.
		_, existingToken, err := f.FCMUserToken.TxSelectOne(dtx, nil, utils.JSON{
			"user_id":   userId,
			"fcm_token": oldFcmToken,
		}, nil, nil, "FOR UPDATE")
		if err != nil {
			return err
		}
		if existingToken == nil {
			return base.NewNotFoundError("FCM_TOKEN_NOT_FOUND", "FCM user token", map[string]any{
				"user_id":   userId,
				"fcm_token": FCMTokenFingerprint(oldFcmToken),
			})
		}

		fcmApplicationId, ok := existingToken["fcm_application_id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}
		deviceType, ok := existingToken["device_type"].(string)
		if !ok {
			return errors.New("FCM_DEVICE_TYPE_TYPE_ASSERTION_FAILED")
		}

		// Atomic claim on UNIQUE (fcm_application_id, fcm_token). Serializes
		// concurrent claims of newFcmToken (different user, device swap, or
		// concurrent RegisterUserToken) via the unique-index row lock.
		_, _, _, err = f.FCMUserToken.TxUpsert(dtx,
			utils.JSON{
				"user_id":          userId,
				"device_type":      deviceType,
				"last_modified_at": time.Now().UTC(),
			},
			utils.JSON{
				"fcm_application_id": fcmApplicationId,
				"fcm_token":          newFcmToken,
			},
		)
		if err != nil {
			return err
		}

		// Clean up the old-token row. Skip when old == new (upsert just touched it).
		if oldFcmToken == newFcmToken {
			return nil
		}
		_, err = f.FCMUserToken.TxHardDelete(dtx, utils.JSON{
			"user_id":            userId,
			"fcm_application_id": fcmApplicationId,
			"fcm_token":          oldFcmToken,
		})
		return err
	})
	if err != nil {
		return err
	}

	aepr.WriteResponseAsJSON(http.StatusOK, nil, utils.JSON{})
	return nil
}

func (f *FirebaseCloudMessaging) SendTopic(ctx context.Context, l *log.DXLog, applicationNameId string, topic string, msgTitle string, msgBody string, msgData map[string]string, onFCMTopicMessage FCMTopicMessageFunc) (err error) {
	return f.Database.Tx(ctx, l, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		fcmApplication, err := f.txGetApplicationByNameId(dtx, applicationNameId)
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

		return nil
	})
}

func (f *FirebaseCloudMessaging) SendToDevice(ctx context.Context, l *log.DXLog, applicationNameId string, userId int64, token string, msgTitle string, msgBody string, msgData map[string]string, userMessageId int64) (err error) {
	return f.Database.Tx(ctx, l, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		fcmApplication, err := f.txGetApplicationByNameId(dtx, applicationNameId)
		if err != nil {
			return err
		}
		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			return errors.New("FCM_APPLICATION_ID_TYPE_ASSERTION_FAILED")
		}

		_, userToken, err := f.FCMUserToken.TxSelectOne(dtx, nil, utils.JSON{
			"fcm_application_id": fcmApplicationId,
			"user_id":            userId,
			"fcm_token":          token,
		}, nil, nil, nil)
		if err != nil {
			return err
		}
		if userToken == nil {
			return base.NewNotFoundError("FCM_USER_TOKEN_NOT_FOUND", "FCM user token", map[string]any{
				"fcm_application_nameid": applicationNameId,
				"user_id":                userId,
				"fcm_token":              FCMTokenFingerprint(token),
			})
		}
		userTokenId, ok := userToken["id"].(int64)
		if !ok {
			return errors.New("USER_TOKEN_ID_TYPE_ASSERTION_FAILED")
		}

		msgDataAsString, err := json.Marshal(msgData)
		if err != nil {
			return err
		}

		userLoginId, err := utils.GetStringFromKV(userToken, "user_loginid")
		if err != nil {
			return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_LOGINID_INVALID")
		}
		userFullName, err := utils.GetStringFromKV(userToken, "user_fullname")
		if err != nil {
			return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_FULLNAME_INVALID")
		}
		fcmApplicationNameId, err := utils.GetStringFromKV(userToken, "fcm_application_nameid")
		if err != nil {
			return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_FCM_APPLICATION_NAMEID_INVALID")
		}
		deviceType, err := utils.GetStringFromKV(userToken, "device_type")
		if err != nil {
			return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_DEVICE_TYPE_INVALID")
		}

		_, err = f.FCMMessage.TxInsertReturningId(dtx, utils.JSON{
			"fcm_user_token_id":      userTokenId,
			"user_id":                userId,
			"fcm_application_id":     fcmApplicationId,
			"fcm_token":              token,
			"device_type":            deviceType,
			"user_loginid":           userLoginId,
			"user_fullname":          userFullName,
			"fcm_application_nameid": fcmApplicationNameId,
			"status":                 StatusPending,
			"title":                  msgTitle,
			"body":                   msgBody,
			"data":                   msgDataAsString,
			"user_message_id":        userMessageId,
		})
		if err != nil {
			return err
		}

		return nil
	})
}

func (f *FirebaseCloudMessaging) SendToUser(ctx context.Context, l *log.DXLog, applicationNameId string, userId int64, msgTitle string, msgBody string, msgData map[string]string, userMessageId int64) (err error) {
	return f.Database.Tx(ctx, l, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		fcmApplication, err := f.txGetApplicationByNameId(dtx, applicationNameId)
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

		msgDataAsBytes, err := json.Marshal(msgData)
		if err != nil {
			return err
		}

		for _, userToken := range userTokens {
			tokenFcmToken, err := utils.GetStringFromKV(userToken, "fcm_token")
			if err != nil {
				return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_FCM_TOKEN_INVALID")
			}
			tokenDeviceType, err := utils.GetStringFromKV(userToken, "device_type")
			if err != nil {
				return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_DEVICE_TYPE_INVALID")
			}
			tokenUserLoginId, err := utils.GetStringFromKV(userToken, "user_loginid")
			if err != nil {
				return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_LOGINID_INVALID")
			}
			tokenUserFullName, err := utils.GetStringFromKV(userToken, "user_fullname")
			if err != nil {
				return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_FULLNAME_INVALID")
			}

			_, err = f.FCMMessage.TxInsertReturningId(dtx, utils.JSON{
				"fcm_user_token_id":      userToken["id"],
				"user_id":                userId,
				"fcm_application_id":     fcmApplicationId,
				"fcm_token":              tokenFcmToken,
				"device_type":            tokenDeviceType,
				"user_loginid":           tokenUserLoginId,
				"user_fullname":          tokenUserFullName,
				"fcm_application_nameid": applicationNameId,
				"status":                 StatusPending,
				"title":                  msgTitle,
				"body":                   msgBody,
				"data":                   msgDataAsBytes,
				"user_message_id":        userMessageId,
			})
			if err != nil {
				return err
			}
		}

		return nil
	})
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

	_, msgData, err := aepr.GetParameterValueAsMapStringString("msg_data")
	if err != nil {
		return err
	}

	err = f.SendToUser(aepr.Context, &aepr.Log, applicationNameId, userId, msgTitle, msgBody, msgData, 0)
	if err != nil {
		return errors.Errorf("failed to send test message: %+v", err)
	}

	return nil

}

func (f *FirebaseCloudMessaging) AllApplicationSendToUser(ctx context.Context, l *log.DXLog, userId int64, msgTitle string, msgBody string, msgData map[string]string, userMessageId int64) (err error) {
	ctx, span := otel.Tracer("push_notification").Start(ctx, "FCM.AllApplicationSendToUser")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("fcm.user_id", userId),
		attribute.String("fcm.msg_title", msgTitle),
	)

	return f.Database.Tx(ctx, l, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		_, fcmApplications, err := f.FCMApplication.TxSelect(dtx, nil, nil, nil, nil, nil, nil)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "SELECT_ALL_APPLICATIONS_FAILED")
			return err
		}

		msgDataAsBytes, err := json.Marshal(msgData)
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

			for _, userToken := range userTokens {
				tokenFcmToken, err := utils.GetStringFromKV(userToken, "fcm_token")
				if err != nil {
					return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_FCM_TOKEN_INVALID")
				}
				tokenDeviceType, err := utils.GetStringFromKV(userToken, "device_type")
				if err != nil {
					return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_DEVICE_TYPE_INVALID")
				}
				tokenUserLoginId, err := utils.GetStringFromKV(userToken, "user_loginid")
				if err != nil {
					return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_LOGINID_INVALID")
				}
				tokenUserFullName, err := utils.GetStringFromKV(userToken, "user_fullname")
				if err != nil {
					return errors.Wrapf(err, "SHOULD_NOT_HAPPEN:FCM_USER_TOKEN_USER_FULLNAME_INVALID")
				}

				_, err = f.FCMMessage.TxInsertReturningId(dtx, utils.JSON{
					"fcm_user_token_id":      userToken["id"],
					"user_id":                userId,
					"fcm_application_id":     fcmApplicationId,
					"fcm_token":              tokenFcmToken,
					"device_type":            tokenDeviceType,
					"user_loginid":           tokenUserLoginId,
					"user_fullname":          tokenUserFullName,
					"fcm_application_nameid": fcmApplicationNameId,
					"status":                 StatusPending,
					"title":                  msgTitle,
					"body":                   msgBody,
					"data":                   msgDataAsBytes,
					"user_message_id":        userMessageId,
				})
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}
func (f *FirebaseCloudMessaging) AllApplicationSendTopic(ctx context.Context, l *log.DXLog, topic string, msgTitle string, msgBody string, msgData map[string]string, onFCMTopicMessage FCMTopicMessageFunc) (err error) {
	ctx, span := otel.Tracer("push_notification").Start(ctx, "FCM.AllApplicationSendTopic")
	defer span.End()
	span.SetAttributes(
		attribute.String("fcm.topic", topic),
		attribute.String("fcm.msg_title", msgTitle),
	)

	return f.Database.Tx(ctx, l, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		_, fcmApplications, err := f.FCMApplication.TxSelect(dtx, nil, nil, nil, nil, nil, nil)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "SELECT_ALL_APPLICATIONS_FAILED")
			return err
		}

		msgDataAsBytes, err := json.Marshal(msgData)
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
				"data":               msgDataAsBytes,
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

		return nil
	})
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
		vaultVarValue := app.App.InitVault.GetStringOrDefault(context.Background(), vaultVarName, "")
		if err := json.Unmarshal([]byte(vaultVarValue), &dataAsJSON); err != nil {
			return nil, errors.Wrapf(err, "ERROR_PARSING_SERVICE_ACCOUNT_JSON_FROM_VAULT:%d:%v", fcmApplicationId, err)
		}
	default:
		return nil, errors.Errorf("UNKNOWN_SERVICE_ACCOUNT_SOURCE:%d:%s", fcmApplicationId, fcmApplicationServiceAccountSource)
	}
	return dataAsJSON, nil
}

func (f *FirebaseCloudMessaging) Execute() (err error) {
	ctx, span := otel.Tracer("push_notification").Start(context.Background(), "FCM.Execute")
	defer span.End()

	_, fcmApplications, err := f.FCMApplication.SelectAll(ctx, &log.Log)
	if err != nil {
		log.Log.Warnf("FCM drain: listing the FCM applications in push_notification.fcm_application failed, no message is sent this pass; next try in 1m0s: %v", err)
		time.Sleep(1 * time.Minute)
		return errors.Wrap(err, "FCM drain: list the FCM applications in push_notification.fcm_application")
	}

	var wg sync.WaitGroup
	for _, fcmApplication := range fcmApplications {
		fcmApplicationId, ok := fcmApplication["id"].(int64)
		if !ok {
			log.Log.Warnf("FCM drain: an FCM application row has id of type %T, want int64; its messages are skipped this pass", fcmApplication["id"])
			continue
		}
		fcmApplicationNameId, ok := fcmApplication["nameid"].(string)
		if !ok {
			log.Log.Warnf("FCM drain: FCM application id %d has nameid of type %T, want string; its messages are skipped this pass", fcmApplicationId, fcmApplication["nameid"])
			continue
		}
		dataAsJSON, err := GetFCMApplicationServiceAccountData(fcmApplication)
		if err != nil {
			log.Log.Errorf(err, "FCM drain: reading the service account of FCM application %s (id %d, source %v) failed; its messages are not sent until it is fixed: %+v", fcmApplicationNameId, fcmApplicationId, fcmApplication["service_account_source"], err)
			continue
		}
		_, err = fcm.Manager.StoreApplication(ctx, fcmApplicationId, dataAsJSON)
		if err != nil {
			log.Log.Warnf("FCM drain: creating the Firebase client of FCM application %s (id %d) from its service account failed; its messages are not sent this pass: %v", fcmApplicationNameId, fcmApplicationId, err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f.processSendTopic(ctx, fcmApplicationId, fcmApplicationNameId)
			if err != nil {
				log.Log.Warnf("FCM drain: topic messages of FCM application %s (id %d) were not processed this pass: %+v", fcmApplicationNameId, fcmApplicationId, err)
			}
			err = f.processMessages(ctx, fcmApplicationId, fcmApplicationNameId)
			if err != nil {
				log.Log.Warnf("FCM drain: device messages of FCM application %s (id %d) were not processed this pass: %+v", fcmApplicationNameId, fcmApplicationId, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// fcmDrainPass counts what one drain pass did with the due messages of one
// FCM application. Its String is the pass's progress line.
type fcmDrainPass struct {
	Kind              string // "message" or "topic message"
	ApplicationId     int64
	ApplicationNameId string
	Due               int
	Sent              int
	Retry             int
	FailedLastAttempt int
	FailedPermanent   int
	Exhausted         int
	Expired           int
	NotDue            int
	HeldElsewhere     int
	ClaimErrors       int
	RateLimitErrors   int
	InvalidRows       int
	StatusWriteErrors int
	UnrecordedSent    int
}

func (p *fcmDrainPass) String() string {
	return fmt.Sprintf("FCM %s drain pass for application %s (id %d): %d due; %d sent, %d to retry, "+
		"%d failed on their last attempt (marked failed permanently next pass), %d failed permanently, %d out of attempts, %d expired, %d not due yet, %d held by another worker, %d claim errors, %d rate limiter errors, "+
		"%d unreadable rows, %d status writes failed, %d sent but not recorded (sent again when their claim ends)",
		p.Kind, p.ApplicationNameId, p.ApplicationId, p.Due, p.Sent, p.Retry, p.FailedLastAttempt, p.FailedPermanent,
		p.Exhausted, p.Expired, p.NotDue, p.HeldElsewhere, p.ClaimErrors, p.RateLimitErrors,
		p.InvalidRows, p.StatusWriteErrors, p.UnrecordedSent)
}

// fcmSendTarget is one claimed message and how to record what happened to it.
type fcmSendTarget struct {
	Kind              string // "message" or "topic message"
	Id                int64
	Recipient         string // token id and fingerprint, or the topic
	ApplicationNameId string
	MaxAttempts       int64
	UpdateStatus      func(ctx context.Context, id int64, status string, retryCount int64) (time.Time, error)
	OnTokenRejected   func() // nil for topic messages
}

func (t fcmSendTarget) name() string {
	return fmt.Sprintf("FCM %s %d to %s of application %s", t.Kind, t.Id, t.Recipient, t.ApplicationNameId)
}

// recordSendResult logs the outcome of one send attempt and writes the
// message's new status. retryCount is the number of attempts that failed
// before this one.
func recordSendResult(ctx context.Context, pass *fcmDrainPass, t fcmSendTarget, sendErr error, retryCount int64) {
	attempt := retryCount + 1
	if sendErr == nil {
		if _, err := t.UpdateStatus(ctx, t.Id, StatusSent, retryCount); err != nil {
			pass.UnrecordedSent++
			log.Log.Errorf(err, "%s was accepted by FCM on attempt %d of %d, but writing status %s failed; it will be sent again when its claim ends: %v",
				t.name(), attempt, t.MaxAttempts, StatusSent, err)
			return
		}
		pass.Sent++
		log.Log.Infof("%s sent on attempt %d of %d", t.name(), attempt, t.MaxAttempts)
		return
	}

	detail := describeFCMError(sendErr)
	if isPermanentFCMError(sendErr) {
		pass.FailedPermanent++
		log.Log.Warnf("%s failed permanently on attempt %d of %d (%s); marking it %s: %v",
			t.name(), attempt, t.MaxAttempts, detail, StatusFailedPermanent, sendErr)
		if t.OnTokenRejected != nil && isTokenRelatedPermanentError(sendErr) {
			t.OnTokenRejected()
		}
		if _, err := t.UpdateStatus(ctx, t.Id, StatusFailedPermanent, retryCount); err != nil {
			pass.StatusWriteErrors++
			log.Log.Errorf(err, "%s: writing status %s failed; it will be tried again when its claim ends: %v", t.name(), StatusFailedPermanent, err)
		}
		return
	}

	if attempt >= t.MaxAttempts {
		pass.FailedLastAttempt++
	} else {
		pass.Retry++
	}
	nextRetryTime, err := t.UpdateStatus(ctx, t.Id, StatusFailed, attempt)
	if err != nil {
		pass.StatusWriteErrors++
		log.Log.Errorf(err, "%s failed on attempt %d of %d (%s): %v; writing status %s failed too, it will be tried again when its claim ends: %v",
			t.name(), attempt, t.MaxAttempts, detail, sendErr, StatusFailed, err)
		return
	}
	if attempt >= t.MaxAttempts {
		log.Log.Warnf("%s failed on attempt %d of %d (%s), no attempts left; it is marked %s on the next pass: %v",
			t.name(), attempt, t.MaxAttempts, detail, StatusFailedPermanent, sendErr)
		return
	}
	log.Log.Warnf("%s failed on attempt %d of %d (%s); attempt %d of %d in %s: %v",
		t.name(), attempt, t.MaxAttempts, detail, attempt+1, t.MaxAttempts, time.Until(nextRetryTime).Round(time.Second), sendErr)
}

// writeStatusOrLog writes a message's status outside a send (unreadable row,
// out of attempts, expired) and counts and logs a failed write.
func writeStatusOrLog(ctx context.Context, pass *fcmDrainPass, t fcmSendTarget, status string, retryCount int64) {
	if _, err := t.UpdateStatus(ctx, t.Id, status, retryCount); err != nil {
		pass.StatusWriteErrors++
		log.Log.Errorf(err, "%s: writing status %s failed; it is tried again on a later pass: %v", t.name(), status, err)
	}
}

func (f *FirebaseCloudMessaging) processMessages(ctx context.Context, applicationId int64, applicationNameId string) error {

	firebaseServiceAccount, err := fcm.Manager.GetServiceAccount(applicationId)
	if err != nil {
		return errors.Wrapf(err, "get the Firebase client of FCM application %s (id %d)", applicationNameId, applicationId)
	}

	// Use QueryBuilder for safe parameterized query
	qb := f.FCMMessage.NewTableSelectQueryBuilder()
	qb.Eq("fcm_application_id", applicationId)
	qb.InStrings("status", []string{StatusPending, StatusFailed, StatusSending})
	qb.And("((next_retry_time <= NOW()) or (next_retry_time IS NULL))")
	qb.Limit(100)

	_, fcmMessages, err := f.FCMMessage.SelectWithBuilder(ctx, &log.Log, qb)
	if err != nil {
		return errors.Wrapf(err, "fetch the due messages of FCM application %s (id %d) from push_notification.fcm_message", applicationNameId, applicationId)
	}

	pass := &fcmDrainPass{Kind: "message", ApplicationId: applicationId, ApplicationNameId: applicationNameId, Due: len(fcmMessages)}
	defer func() {
		if pass.Due > 0 {
			log.Log.Infof("%s", pass)
		}
	}()

	for _, fcmMessage := range fcmMessages {
		fcmMessageId, err := utils.GetInt64FromKV(fcmMessage, "id")
		if err != nil {
			pass.InvalidRows++
			log.Log.Errorf(err, "SHOULD_NOT_HAPPEN: an FCM message row of application %s has no usable id, so it cannot be marked failed; skipped: %v", applicationNameId, err)
			continue
		}

		target := fcmSendTarget{
			Kind:              "message",
			Id:                fcmMessageId,
			Recipient:         "an unread token",
			ApplicationNameId: applicationNameId,
			MaxAttempts:       FCMMessageMaxRetryAttemptCount,
			UpdateStatus:      f.updateMessageStatus,
		}
		markUnreadable := func(field string, err error, retryCount int64) {
			pass.InvalidRows++
			log.Log.Errorf(err, "SHOULD_NOT_HAPPEN: %s has an unreadable %s; marking it %s: %v", target.name(), field, StatusFailedPermanent, err)
			writeStatusOrLog(ctx, pass, target, StatusFailedPermanent, retryCount)
		}

		retryCount, err := utils.GetInt64FromKV(fcmMessage, "retry_count")
		if err != nil {
			markUnreadable("retry_count", err, 0)
			continue
		}
		fcmToken, err := utils.GetStringFromKV(fcmMessage, "fcm_token")
		if err != nil {
			markUnreadable("fcm_token", err, retryCount)
			continue
		}
		deviceType, err := utils.GetStringFromKV(fcmMessage, "device_type")
		if err != nil {
			markUnreadable("device_type", err, retryCount)
			continue
		}
		target.Recipient = fcmTokenRecipient(fcmMessage, fcmToken, deviceType)
		target.OnTokenRejected = func() { f.deleteUserTokenByFCMToken(ctx, fcmToken, target.Recipient) }

		msgTitle, err := utils.GetStringFromKV(fcmMessage, "title")
		if err != nil {
			markUnreadable("title", err, retryCount)
			continue
		}
		msgBody, err := utils.GetStringFromKV(fcmMessage, "body")
		if err != nil {
			markUnreadable("body", err, retryCount)
			continue
		}
		msgData, err := utils.GetMapStringStringFromKV(fcmMessage, "data")
		if err != nil {
			markUnreadable("data", err, retryCount)
			continue
		}

		// Inject fcm_user_message_uid and user_message_uid into msgData for the mobile app
		if msgData == nil {
			msgData = make(map[string]string)
		}
		fcmMessageUid, uidErr := utils.GetStringFromKV(fcmMessage, "uid")
		if uidErr == nil && fcmMessageUid != "" {
			msgData["fcm_user_message_uid"] = fcmMessageUid
		}
		userMessageUid, umUidErr := utils.GetStringFromKV(fcmMessage, "user_message_uid")
		if umUidErr == nil && userMessageUid != "" {
			msgData["user_message_uid"] = userMessageUid
		}

		MsgNextRetryTime, err := utils.GetTimeFromKV(fcmMessage, "next_retry_time")
		if err == nil {
			if MsgNextRetryTime.After(time.Now()) {
				pass.NotDue++
				continue // Skip messages that are not ready for retry
			}
		}

		// Check if message exceeded max retry count
		if retryCount >= FCMMessageMaxRetryAttemptCount {
			pass.Exhausted++
			log.Log.Warnf("%s failed all %d attempts; marking it %s", target.name(), FCMMessageMaxRetryAttemptCount, StatusFailedPermanent)
			writeStatusOrLog(ctx, pass, target, StatusFailedPermanent, retryCount)
			continue
		}

		// Check if message has expired
		createdAt, err := utils.GetTimeFromKV(fcmMessage, "created_at")
		if err == nil {
			expirationDuration := time.Duration(FCMMessageExpirationInSeconds) * time.Second
			if time.Since(createdAt) > expirationDuration {
				pass.Expired++
				log.Log.Warnf("%s expired unsent after %d of %d attempts (created %s, older than %s); marking it %s",
					target.name(), retryCount, FCMMessageMaxRetryAttemptCount, createdAt.Format(time.RFC3339), expirationDuration, StatusExpired)
				writeStatusOrLog(ctx, pass, target, StatusExpired, retryCount)
				continue
			}
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			pass.RateLimitErrors++
			log.Log.Warnf("%s: waiting for the FCM send rate limiter failed, left for the next pass: %v", target.name(), err)
			continue
		}

		// Claim the message so no other worker sends it too
		isClaimed, err := claimMessageForSending(ctx, f.FCMMessage, fcmMessageId)
		if err != nil {
			pass.ClaimErrors++
			log.Log.Warnf("%s: claiming it in push_notification.fcm_message failed, left for the next pass: %v", target.name(), err)
			continue
		}
		if !isClaimed {
			pass.HeldElsewhere++
			continue // another worker has it
		}

		sendErr := f.sendNotification(ctx, firebaseServiceAccount.Client, fcmToken, deviceType, msgTitle, msgBody, msgData)
		recordSendResult(ctx, pass, target, sendErr, retryCount)
	}

	return nil
}

func (f *FirebaseCloudMessaging) processSendTopic(ctx context.Context, applicationId int64, applicationNameId string) error {

	firebaseServiceAccount, err := fcm.Manager.GetServiceAccount(applicationId)
	if err != nil {
		return errors.Wrapf(err, "get the Firebase client of FCM application %s (id %d)", applicationNameId, applicationId)
	}

	// Use QueryBuilder for safe parameterized query
	qb := f.FCMTopicMessage.NewTableSelectQueryBuilder()
	qb.Eq("fcm_application_id", applicationId)
	qb.InStrings("status", []string{StatusPending, StatusFailed, StatusSending})
	qb.And("((next_retry_time <= NOW()) or (next_retry_time IS NULL))")
	qb.Limit(100)

	_, fcmTopicMessages, err := f.FCMTopicMessage.SelectWithBuilder(ctx, &log.Log, qb)
	if err != nil {
		return errors.Wrapf(err, "fetch the due topic messages of FCM application %s (id %d) from push_notification.fcm_topic_message", applicationNameId, applicationId)
	}

	pass := &fcmDrainPass{Kind: "topic message", ApplicationId: applicationId, ApplicationNameId: applicationNameId, Due: len(fcmTopicMessages)}
	defer func() {
		if pass.Due > 0 {
			log.Log.Infof("%s", pass)
		}
	}()

	for _, fcmTopicMessage := range fcmTopicMessages {
		fcmTopicMessageId, err := utils.GetInt64FromKV(fcmTopicMessage, "id")
		if err != nil {
			pass.InvalidRows++
			log.Log.Errorf(err, "SHOULD_NOT_HAPPEN: an FCM topic message row of application %s has no usable id, so it cannot be marked failed; skipped: %v", applicationNameId, err)
			continue
		}

		target := fcmSendTarget{
			Kind:              "topic message",
			Id:                fcmTopicMessageId,
			Recipient:         "an unread topic",
			ApplicationNameId: applicationNameId,
			MaxAttempts:       FCMTopicMessageMaxRetryAttemptCount,
			UpdateStatus:      f.updateTopicMessageStatus,
		}
		markUnreadable := func(field string, err error, retryCount int64) {
			pass.InvalidRows++
			log.Log.Errorf(err, "SHOULD_NOT_HAPPEN: %s has an unreadable %s; marking it %s: %v", target.name(), field, StatusFailedPermanent, err)
			writeStatusOrLog(ctx, pass, target, StatusFailedPermanent, retryCount)
		}

		retryCount, err := utils.GetInt64FromKV(fcmTopicMessage, "retry_count")
		if err != nil {
			markUnreadable("retry_count", err, 0)
			continue
		}
		msgTopic, err := utils.GetStringFromKV(fcmTopicMessage, "topic")
		if err != nil {
			markUnreadable("topic", err, retryCount)
			continue
		}
		target.Recipient = fmt.Sprintf("topic %q", msgTopic)
		msgTitle, err := utils.GetStringFromKV(fcmTopicMessage, "title")
		if err != nil {
			markUnreadable("title", err, retryCount)
			continue
		}
		msgBody, err := utils.GetStringFromKV(fcmTopicMessage, "body")
		if err != nil {
			markUnreadable("body", err, retryCount)
			continue
		}
		msgData, err := utils.GetMapStringStringFromKV(fcmTopicMessage, "data")
		if err != nil {
			markUnreadable("data", err, retryCount)
			continue
		}

		MsgNextRetryTime, err := utils.GetTimeFromKV(fcmTopicMessage, "next_retry_time")
		if err == nil {
			if MsgNextRetryTime.After(time.Now()) {
				pass.NotDue++
				continue // Skip messages that are not ready for retry
			}
		}

		// Check if message exceeded max retry count
		if retryCount >= FCMTopicMessageMaxRetryAttemptCount {
			pass.Exhausted++
			log.Log.Warnf("%s failed all %d attempts; marking it %s", target.name(), FCMTopicMessageMaxRetryAttemptCount, StatusFailedPermanent)
			writeStatusOrLog(ctx, pass, target, StatusFailedPermanent, retryCount)
			continue
		}

		// Check if message has expired
		createdAt, err := utils.GetTimeFromKV(fcmTopicMessage, "created_at")
		if err == nil {
			expirationDuration := time.Duration(FCMTopicMessageExpirationInSeconds) * time.Second
			if time.Since(createdAt) > expirationDuration {
				pass.Expired++
				log.Log.Warnf("%s expired unsent after %d of %d attempts (created %s, older than %s); marking it %s",
					target.name(), retryCount, FCMTopicMessageMaxRetryAttemptCount, createdAt.Format(time.RFC3339), expirationDuration, StatusExpired)
				writeStatusOrLog(ctx, pass, target, StatusExpired, retryCount)
				continue
			}
		}

		// Wait for rate limit token
		err = fcm.Manager.Limiter.Wait(ctx)
		if err != nil {
			pass.RateLimitErrors++
			log.Log.Warnf("%s: waiting for the FCM send rate limiter failed, left for the next pass: %v", target.name(), err)
			continue
		}

		// Claim the message so no other worker sends it too
		isClaimed, err := claimMessageForSending(ctx, f.FCMTopicMessage, fcmTopicMessageId)
		if err != nil {
			pass.ClaimErrors++
			log.Log.Warnf("%s: claiming it in push_notification.fcm_topic_message failed, left for the next pass: %v", target.name(), err)
			continue
		}
		if !isClaimed {
			pass.HeldElsewhere++
			continue // another worker has it
		}

		sendErr := f.sendTopicNotification(ctx, firebaseServiceAccount.Client, msgTopic, msgTitle, msgBody, msgData)
		recordSendResult(ctx, pass, target, sendErr, retryCount)
	}

	return nil
}

// fcmTokenRecipient names a message's token for logs: its fcm_user_token id,
// a fingerprint and the device type, never the token itself.
func fcmTokenRecipient(fcmMessage utils.JSON, fcmToken string, deviceType string) string {
	tokenId := "unknown"
	if id, err := utils.GetInt64FromKV(fcmMessage, "fcm_user_token_id"); err == nil {
		tokenId = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("token id %s (%s, %s)", tokenId, FCMTokenFingerprint(fcmToken), deviceType)
}

// fcmErrorDetail is what FCM said about a failed send.
type fcmErrorDetail struct {
	MessagingCode string // FCM's own code, such as UNREGISTERED
	PlatformCode  string // the Google API status, such as NOT_FOUND
	HTTPStatus    int    // 0 when no HTTP response came back
}

func (d fcmErrorDetail) String() string {
	code := "no FCM error code"
	switch {
	case d.MessagingCode != "" && d.PlatformCode != "":
		code = fmt.Sprintf("FCM error %s, status %s", d.MessagingCode, d.PlatformCode)
	case d.MessagingCode != "":
		code = "FCM error " + d.MessagingCode
	case d.PlatformCode != "":
		code = "status " + d.PlatformCode
	}
	if d.HTTPStatus == 0 {
		return code + ", no HTTP response"
	}
	return fmt.Sprintf("%s, HTTP %d", code, d.HTTPStatus)
}

// describeFCMError reads the codes and HTTP status out of an error from the
// Firebase SDK. The SDK keeps them in an internal type, so they are read
// through its public predicates.
func describeFCMError(err error) fcmErrorDetail {
	var d fcmErrorDetail
	if err == nil {
		return d
	}
	if resp := errorutils.HTTPResponse(err); resp != nil {
		d.HTTPStatus = resp.StatusCode
	}
	messagingCodes := []struct {
		is   func(error) bool
		code string
	}{
		{messaging.IsUnregistered, "UNREGISTERED"},
		{messaging.IsInvalidArgument, "INVALID_ARGUMENT"},
		{messaging.IsSenderIDMismatch, "SENDER_ID_MISMATCH"},
		{messaging.IsQuotaExceeded, "QUOTA_EXCEEDED"},
		{messaging.IsUnavailable, "UNAVAILABLE"},
		{messaging.IsInternal, "INTERNAL"},
		{messaging.IsThirdPartyAuthError, "THIRD_PARTY_AUTH_ERROR"},
	}
	for _, c := range messagingCodes {
		if c.is(err) {
			d.MessagingCode = c.code
			break
		}
	}
	platformCodes := []struct {
		is   func(error) bool
		code string
	}{
		{errorutils.IsInvalidArgument, "INVALID_ARGUMENT"},
		{errorutils.IsFailedPrecondition, "FAILED_PRECONDITION"},
		{errorutils.IsOutOfRange, "OUT_OF_RANGE"},
		{errorutils.IsUnauthenticated, "UNAUTHENTICATED"},
		{errorutils.IsPermissionDenied, "PERMISSION_DENIED"},
		{errorutils.IsNotFound, "NOT_FOUND"},
		{errorutils.IsConflict, "CONFLICT"},
		{errorutils.IsAborted, "ABORTED"},
		{errorutils.IsAlreadyExists, "ALREADY_EXISTS"},
		{errorutils.IsResourceExhausted, "RESOURCE_EXHAUSTED"},
		{errorutils.IsCancelled, "CANCELLED"},
		{errorutils.IsDataLoss, "DATA_LOSS"},
		{errorutils.IsUnknown, "UNKNOWN"},
		{errorutils.IsInternal, "INTERNAL"},
		{errorutils.IsUnavailable, "UNAVAILABLE"},
		{errorutils.IsDeadlineExceeded, "DEADLINE_EXCEEDED"},
	}
	for _, c := range platformCodes {
		if c.is(err) {
			d.PlatformCode = c.code
			break
		}
	}
	return d
}

// claimMessageForSending reserves one message row for this worker. The UPDATE
// only matches while the row is still claimable (PENDING or FAILED and due, or
// SENDING with an expired claim), so when workers race for the same row exactly
// one of them gets an affected row back.
func claimMessageForSending(ctx context.Context, table *tables.DXRawTable, messageId int64) (bool, error) {
	now := time.Now()
	tqb := table.NewTableUpdateQueryBuilder()
	tqb.UpdateQueryBuilder.Set("status", StatusSending)
	tqb.UpdateQueryBuilder.Set("next_retry_time", now.Add(FCMClaimLease))
	tqb.UpdateQueryBuilder.Where("id", messageId)
	tqb.Conditions = append(tqb.Conditions,
		"((status IN (:claim_pending, :claim_failed) AND (next_retry_time IS NULL OR next_retry_time <= :claim_now)) "+
			"OR (status = :claim_sending AND next_retry_time <= :claim_now))")
	tqb.Args["claim_pending"] = StatusPending
	tqb.Args["claim_failed"] = StatusFailed
	tqb.Args["claim_sending"] = StatusSending
	tqb.Args["claim_now"] = now

	result, _, err := table.UpdateWithBuilder(ctx, &log.Log, tqb)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
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

	ctx, span := otel.Tracer("pgn.fcm").Start(ctx, "fcm.Send",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("peer.service", "google-fcm"),
			attribute.String("fcm.device_type", deviceType),
		),
	)
	_, err := client.Send(ctx, message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
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

	ctx, span := otel.Tracer("pgn.fcm").Start(ctx, "fcm.SendTopic",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("peer.service", "google-fcm"),
			attribute.String("fcm.topic", topic),
		),
	)
	_, err := client.Send(ctx, message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}

// Add this new helper function to determine if an FCM error is permanent
func isPermanentFCMError(err error) bool {
	if err == nil {
		return false
	}

	// Typed checks first: the Firebase SDK knows which error codes are transient
	if messaging.IsUnavailable(err) || messaging.IsInternal(err) || messaging.IsQuotaExceeded(err) ||
		messaging.IsMessageRateExceeded(err) || messaging.IsUnknown(err) {
		return false
	}
	if messaging.IsUnregistered(err) || messaging.IsInvalidArgument(err) || messaging.IsSenderIDMismatch(err) ||
		messaging.IsMismatchedCredential(err) || messaging.IsInvalidAPNSCredentials(err) || messaging.IsThirdPartyAuthError(err) {
		return true
	}

	// Check for specific FCM error types that indicate permanent failures
	errStr := err.Error()

	// These error patterns indicate permanent failures that shouldn't be retried
	permanentErrorPatterns := []string{
		"registration-token-not-registered",
		"invalid-registration-token",
		"not a valid fcm registration token",
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

	return false
}

func isTokenRelatedPermanentError(err error) bool {
	if err == nil {
		return false
	}
	if messaging.IsUnregistered(err) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	tokenPatterns := []string{
		"registration-token-not-registered",
		"invalid-registration-token",
		"not a valid fcm registration token",
		"unregistered",
		"invalidregistration",
		"notregistered",
		"not-found",
		"requested entity was not found",
	}
	for _, pattern := range tokenPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	return false
}

// deleteUserTokenByFCMToken drops a token FCM has rejected for good. recipient
// names it in the log (token id and fingerprint); the token itself is never
// logged.
func (f *FirebaseCloudMessaging) deleteUserTokenByFCMToken(ctx context.Context, fcmToken string, recipient string) {
	txErr := f.Database.Tx(ctx, &log.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		_, err := f.FCMUserToken.TxHardDelete(dtx, utils.JSON{"fcm_token": fcmToken})
		return err
	})
	if txErr != nil {
		log.Log.Warnf("FCM rejected %s for good, but deleting it from push_notification.fcm_user_token failed; later sends to it will fail the same way: %v", recipient, txErr)
	} else {
		log.Log.Infof("FCM rejected %s for good; deleted it from push_notification.fcm_user_token", recipient)
	}
}

// updateMessageStatus writes a device message's status and, for any status
// but SENT, its retry count and the next retry time, which it returns.
func (f *FirebaseCloudMessaging) updateMessageStatus(ctx context.Context, messageId int64, status string, retryCount int64) (nextRetryTime time.Time, err error) {
	p := utils.JSON{
		"status": status,
	}
	if status != StatusSent {
		nextRetryTime = f.calculateMessageNextRetryTime(retryCount)
		p["retry_count"] = retryCount
		p["next_retry_time"] = nextRetryTime
	}
	_, err = f.FCMMessage.UpdateSimple(ctx, p, utils.JSON{
		"id": messageId,
	})
	return nextRetryTime, err
}

// updateTopicMessageStatus is updateMessageStatus for topic messages.
func (f *FirebaseCloudMessaging) updateTopicMessageStatus(ctx context.Context, messageId int64, status string, retryCount int64) (nextRetryTime time.Time, err error) {
	p := utils.JSON{
		"status": status,
	}
	if status != StatusSent {
		nextRetryTime = f.calculateTopicMessageNextRetryTime(retryCount)
		p["retry_count"] = retryCount
		p["next_retry_time"] = nextRetryTime
	}
	_, err = f.FCMTopicMessage.UpdateSimple(ctx, p, utils.JSON{
		"id": messageId,
	})
	return nextRetryTime, err
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
