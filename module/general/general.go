package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/lib"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	Property            *tables.DXPropertyTable
	Announcement        *tables.DXTable
	AnnouncementPicture *lib.ImageObjectStorage
	Template            *tables.DXTable
}

func (g *DxmGeneral) Init(databaseNameId string) {
	g.DatabaseNameId = databaseNameId
	g.Property = tables.NewDXPropertyTableSimple(databaseNameId, "general.property",
		"general.property", "general.property", "id", "uid", "nameid", "data", nil)
	g.Announcement = tables.NewDXTableSimple(databaseNameId, "general.announcement",
		"general.announcement", "general.announcement", "id", "uid", "uid", "data", nil)
	g.Template = tables.NewDXTableSimple(g.DatabaseNameId,
		"general.template", "general.template", "general.template", "id", "uid", "nameid", "data", nil)
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

func (g *DxmGeneral) TemplateCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = g.Template.DoCreate(aepr, map[string]any{
		"nameid":       aepr.ParameterValues["nameid"].Value.(string),
		"type":         aepr.ParameterValues["type"].Value.(string),
		"content_type": aepr.ParameterValues["content_type"].Value.(string),
		"subject":      aepr.ParameterValues["subject"].Value.(string),
		"body":         aepr.ParameterValues["body"].Value.(string),
	})
	return err
}

var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
