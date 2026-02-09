package external_system

import (
	"net/http"

	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/utils"
)

type DxmExternalSystemLoginFunc func(aNameId string, key string, secret string, ttl int) (isSuccess bool, session string, err error)
type DxmExternalSystemAuthenticateFunc func(aNameId string, session string, ttl int) (err error)

type DxmExternalSystem struct {
	ExternalSystem *tables.DXTable
	OnLogin        DxmExternalSystemLoginFunc
	OnAuthenticate DxmExternalSystemAuthenticateFunc
}

func (w *DxmExternalSystem) Init(databaseNameId string) {
	w.ExternalSystem = tables.NewDXTableSimple(databaseNameId,
		"configuration.external_system", "configuration.external_system", "configuration.v_external_system",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "type"},
		[]string{"id", "nameid", "type", "created_at", "last_modified_at"},
		[]string{"id", "uid", "nameid", "type", "created_at", "last_modified_at", "is_deleted"},
	)
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

var ModuleExternalSystem DxmExternalSystem

func init() {
	ModuleExternalSystem = DxmExternalSystem{}
}
