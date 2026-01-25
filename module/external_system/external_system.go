package external_system

import (
	"net/http"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
)

type DxmExternalSystemLoginFunc func(aNameId string, key string, secret string, ttl int) (isSuccess bool, session string, err error)
type DxmExternalSystemAuthenticateFunc func(aNameId string, session string, ttl int) (err error)

type DxmExternalSystem struct {
	ExternalSystem *table.DXTable3
	OnLogin        DxmExternalSystemLoginFunc
	OnAuthenticate DxmExternalSystemAuthenticateFunc
}

func (w *DxmExternalSystem) Init(databaseNameId string) {
	w.ExternalSystem = table.NewDXTable3Simple(databaseNameId, "configuration.external_system",
		"configuration.external_system", "id", "uid", "nameid")
}
func (w *DxmExternalSystem) ExternalSystemList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.RequestPagingList(aepr)
}

func (w *DxmExternalSystem) ExternalSystemCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	configuration, ok := aepr.ParameterValues["configuration"].Value.(utils.JSON)
	if !ok {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, "", "CONFIGURATION_IS_NOT_JSON")
	}
	configurationAsString, err := utils.JSONToString(configuration)
	if err != nil {
		return err
	}
	_, err = w.ExternalSystem.DoCreate(aepr, map[string]any{
		"nameid":        aepr.ParameterValues["nameid"].Value.(string),
		"type":          aepr.ParameterValues["type"].Value.(string),
		"configuration": configurationAsString,
	})
	return err
}

func (w *DxmExternalSystem) ExternalSystemRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.RequestRead(aepr)
}

func (w *DxmExternalSystem) ExternalSystemEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.RequestEdit(aepr)
}

func (w *DxmExternalSystem) ExternalSystemDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.RequestSoftDelete(aepr)
}

var ModuleExternalSystem DxmExternalSystem

func init() {
	ModuleExternalSystem = DxmExternalSystem{}
}
