package general

import (
	"context"

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
	g.Property = tables.NewDXPropertyTableSimple(databaseNameId,
		"general.property", "general.property", "general.property",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "type"},
		[]string{"id", "nameid", "type", "created_at", "uid"},
		[]string{"id", "nameid", "type", "created_at", "uid"},
	)
	g.Announcement = tables.NewDXTableSimple(databaseNameId,
		"general.announcement", "general.announcement", "general.announcement",
		"id", "uid", "uid", "data",
		nil,
		nil,
		[]string{"title", "content"},
		[]string{"id", "title", "content", "timestamp", "created_at", "is_deleted", "uid"},
		[]string{"id", "title", "content", "timestamp", "created_at", "is_deleted", "uid"},
	)
	g.Announcement.DownloadableOrderByFieldNames = []string{"id", "title", "content", "created_at", "uid"}
	g.Template = tables.NewDXTableSimple(g.DatabaseNameId,
		"general.template", "general.template", "general.template",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "type", "subject", "body"},
		[]string{"id", "nameid", "type", "subject", "created_at", "is_deleted", "uid"},
		[]string{"id", "nameid", "type", "subject", "body", "created_at", "is_deleted", "uid"},
	)
}

func (g *DxmGeneral) TemplateGetByNameId(l *log.DXLog, nameId string) (gt utils.JSON, templateTitle string, templateContentType string, templateBody string, err error) {
	_, templateMessage, err := g.Template.ShouldGetByNameId(context.Background(), l, nameId)
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
	_, nameid, err := aepr.GetParameterValueAsString("nameid")
	if err != nil {
		return err
	}
	_, templateType, err := aepr.GetParameterValueAsString("type")
	if err != nil {
		return err
	}
	_, contentType, err := aepr.GetParameterValueAsString("content_type")
	if err != nil {
		return err
	}
	_, subject, err := aepr.GetParameterValueAsString("subject")
	if err != nil {
		return err
	}
	_, body, err := aepr.GetParameterValueAsString("body")
	if err != nil {
		return err
	}
	_, err = g.Template.DoCreate(aepr, map[string]any{
		"nameid":       nameid,
		"type":         templateType,
		"content_type": contentType,
		"subject":      subject,
		"body":         body,
	})
	return err
}

var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
