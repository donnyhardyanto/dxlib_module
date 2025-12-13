package general

import (
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/lib"
	"github.com/pkg/errors"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	Property            *table.DXPropertyTable
	Announcement        *table.DXTable
	AnnouncementPicture *lib.ImageObjectStorage
	Template            *table.DXTable
}

func (g *DxmGeneral) Init(databaseNameId string) {
	g.DatabaseNameId = databaseNameId
	g.Property = table.Manager.NewPropertyTable(databaseNameId, "general.property",
		"general.property",
		"general.property", "nameid", "id", "uid", "data")
	g.Announcement = table.Manager.NewTable(databaseNameId, "general.announcement",
		"general.announcement",
		"general.announcement", "uid", "id", "uid", "data")
	g.Template = table.Manager.NewTable(g.DatabaseNameId,
		"general.template", "general.template",
		"settings.template", "nameid", "id", "uid", "data")
}

func (g *DxmGeneral) TemplateGetByNameId(l *log.DXLog, nameId string) (gt utils.JSON, templateTitle string, templateContentType string, templateBody string, err error) {
	_, templateMessage, err := g.Template.ShouldGetByNameId(l, nameId)
	if err != nil {
		return nil, "", "", "", err
	}
	templateTitle, ok := templateMessage["subject"].(string)
	if !ok {
		return nil, "", "", "", errors.New("INVALID_TEMPLATE_TITLE")
	}
	templateContentType, ok = templateMessage["content_type"].(string)
	if !ok {
		return nil, "", "", "", errors.New("INVALID_TEMPLATE_CONTENT_TYPE")
	}
	templateBody, ok = templateMessage["body"].(string)
	if !ok {
		return nil, "", "", "", errors.New("INVALID_TEMPLATE_BODY")
	}
	return templateMessage, templateTitle, templateContentType, templateBody, nil
}

var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
