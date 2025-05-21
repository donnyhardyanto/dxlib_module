package webapp

import (
	"github.com/donnyhardyanto/dxlib/api"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
)

type DxmWebapp struct {
	dxlibModule.DXModule
	App  *table.DXTable
	Page *table.DXTable
}

func (w *DxmWebapp) Init(databaseNameId string) {
	w.App = table.Manager.NewTable(databaseNameId, "webapp.app", "webapp.app",
		"webapp.app", "nameid", "id", "uid", "data")
	w.Page = table.Manager.NewTable(databaseNameId, "webapp.page", "webapp.page",
		"webapp.page", "nameid", "id", "uid", "data")
}

func (w *DxmWebapp) AppList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.App.RequestPagingList(aepr)
}

func (w *DxmWebapp) AppCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.App.DoCreate(aepr, map[string]any{
		"nameid": aepr.ParameterValues["nameid"].Value.(string),
	})
	return err
}

func (w *DxmWebapp) AppRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.App.RequestRead(aepr)
}

func (w *DxmWebapp) AppEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.App.RequestEdit(aepr)
}

func (w *DxmWebapp) AppDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.App.RequestSoftDelete(aepr)
}

func (w *DxmWebapp) PageList(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.Page.RequestPagingList(aepr)
}

func (w *DxmWebapp) PageCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = w.Page.DoCreate(aepr, map[string]any{
		"app_id":  aepr.ParameterValues["app_id"].Value.(int64),
		"nameid":  aepr.ParameterValues["nameid"].Value.(string),
		"content": aepr.ParameterValues["content"].Value.(string),
	})
	return err
}

func (w *DxmWebapp) PageRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.Page.RequestRead(aepr)
}

func (w *DxmWebapp) PageEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.Page.RequestEdit(aepr)
}

func (w *DxmWebapp) PageDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return w.Page.RequestSoftDelete(aepr)
}

var ModuleWebapp DxmWebapp

func init() {
	ModuleWebapp = DxmWebapp{}
}
