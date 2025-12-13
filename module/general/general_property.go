package general

import (
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib_module/lib"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	Property            *table.DXPropertyTable
	Announcement        *table.DXTable
	AnnouncementPicture *lib.ImageObjectStorage
	/*	EMailTemplate       *table.DXTable
		SMSTemplate         *table.DXTable
		FCMTemplate         *table.DXTable*/
	Template *table.DXTable
}

func (g *DxmGeneral) Init(databaseNameId string) {
	g.DatabaseNameId = databaseNameId
	g.Property = table.Manager.NewPropertyTable(databaseNameId, "general.property",
		"general.property",
		"general.property", "nameid", "id", "uid", "data")
	g.Announcement = table.Manager.NewTable(databaseNameId, "general.announcement",
		"general.announcement",
		"general.announcement", "uid", "id", "uid", "data")
	/*g.EMailTemplate = table.Manager.NewTable(g.DatabaseNameId,
		"general.email_template", "general.email_template",
		"general.email_template", "nameid", "id", "uid", "data")
	g.SMSTemplate = table.Manager.NewTable(g.DatabaseNameId,
		"general.sms_template", "general.sms_template",
		"general.sms_template", "nameid", "id", "uid", "data")
	g.FCMTemplate = table.Manager.NewTable(g.DatabaseNameId,
		"general.fcm_template", "general.fcm_template",
		"general.fcm_template", "nameid", "id", "uid", "data")
	*/
	g.Template = table.Manager.NewTable(g.DatabaseNameId,
		"general.template", "general.template",
		"settings.template", "nameid", "id", "uid", "data")
}

var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
