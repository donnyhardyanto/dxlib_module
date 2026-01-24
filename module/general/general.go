package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/errors"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table3"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/lib"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	Property            *table3.DXPropertyTable3
	Announcement        *table3.DXTable3
	AnnouncementPicture *lib.ImageObjectStorage
	Template            *table3.DXTable3
}

func (g *DxmGeneral) Init(databaseNameId string) {
	g.DatabaseNameId = databaseNameId
	g.Property = table3.NewDXPropertyTable3Simple(databaseNameId, "general.property",
		"general.property", "id", "uid", "nameid")
	g.Announcement = table3.NewDXTable3Simple(databaseNameId, "general.announcement",
		"general.announcement", "id", "uid", "uid")
	g.Template = table3.NewDXTable3Simple(g.DatabaseNameId,
		"general.template", "general.template", "id", "uid", "nameid")
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
