package external_system

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/table"
)

type DxmExternalSystem struct {
	ExternalSystem *table.DXTable
}

func (w *DxmExternalSystem) Init(databaseNameId string) {
	w.ExternalSystem = table.Manager.NewTable(databaseNameId, "configuration.external_system",
		"configuration.external_system",
		"configuration.external_system", `nameid`, `id`)
}
func (w *DxmExternalSystem) ExternalSystemList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.List(aepr)
}

func (w *DxmExternalSystem) ExternalSystemCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.ExternalSystem.DoCreate(aepr, map[string]any{
		`nameid`:        aepr.ParameterValues[`nameid`].Value.(string),
		`type`:          aepr.ParameterValues[`type`].Value.(string),
		`configuration`: aepr.ParameterValues[`configuration`].Value.(string),
	})
	return err
}

func (w *DxmExternalSystem) ExternalSystemRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.Read(aepr)
}

func (w *DxmExternalSystem) ExternalSystemEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.Edit(aepr)
}

func (w *DxmExternalSystem) ExternalSystemDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.ExternalSystem.SoftDelete(aepr)
}

var ModuleExternalSystem DxmExternalSystem

func init() {
	ModuleExternalSystem = DxmExternalSystem{}
}
