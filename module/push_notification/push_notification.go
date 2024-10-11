package push_notification

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
)

type DxmPushNotification struct {
	FCMApplication *table.DXTable
	FCMUserToken   *table.DXTable
	FCMMessage     *table.DXTable
}

func (w *DxmPushNotification) DefineTables(databaseNameId string) {
	w.FCMApplication = table.Manager.NewTable(databaseNameId, "push_notification.fcm_application",
		"push_notification.fcm_application",
		"push_notification.fcm_application", `nameid`, `id`)
	w.FCMUserToken = table.Manager.NewTable(databaseNameId, "push_notification.user_fcm_token",
		"push_notification.user_fcm_token",
		"push_notification.user_fcm_token", `id`, `id`)
	w.FCMMessage = table.Manager.NewTable(databaseNameId, "push_notification.fcm_message",
		"push_notification.fcm_message",
		"push_notification.fcm_message", `id`, `id`)
}

func (w *DxmPushNotification) FCMApplicationList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMApplication.List(aepr)
}

func (w *DxmPushNotification) FCMApplicationCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.FCMApplication.DoCreate(aepr, map[string]interface{}{
		`nameid`:               aepr.ParameterValues[`nameid`].Value.(string),
		`service_account_data`: aepr.ParameterValues[`service_account_data`].Value.(utils.JSON),
	})
	return err
}

func (w *DxmPushNotification) FCMApplicationRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMApplication.Read(aepr)
}

func (w *DxmPushNotification) FCMApplicationEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMApplication.Edit(aepr)
}

func (w *DxmPushNotification) FCMApplicationDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMApplication.SoftDelete(aepr)
}

func (w *DxmPushNotification) FCMUserTokenList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMUserToken.List(aepr)
}

func (w *DxmPushNotification) FCMUserTokenCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.FCMUserToken.DoCreate(aepr, map[string]interface{}{
		`fcm_token`: aepr.ParameterValues[`fcm_token`].Value.(string),
		`user_id`:   aepr.ParameterValues[`user_id`].Value.(string),
	})
	return err
}

func (w *DxmPushNotification) FCMUserTokenRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMUserToken.Read(aepr)
}

func (w *DxmPushNotification) FCMUserTokenHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMUserToken.HardDelete(aepr)
}

func (w *DxmPushNotification) FCMMessageList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMMessage.List(aepr)
}

func (w *DxmPushNotification) FCMMessageCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.FCMMessage.DoCreate(aepr, map[string]interface{}{
		`application_id`: aepr.ParameterValues[`application_id`].Value.(string),
		`user_id`:        aepr.ParameterValues[`user_id`].Value.(string),
		`title`:          aepr.ParameterValues[`title`].Value.(string),
		`body`:           aepr.ParameterValues[`body`].Value.(string),
		`data`:           aepr.ParameterValues[`data`].Value.(utils.JSON),
	})
	return err
}

func (w *DxmPushNotification) FCMMessageRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMMessage.Read(aepr)
}

func (w *DxmPushNotification) FCMMessageHardDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.FCMMessage.HardDelete(aepr)
}
