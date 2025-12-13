package general

import (
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/pkg/errors"
)

/*func (g *DxmGeneral) FCMTemplateGetByNameId(l *log.DXLog, nameId string) (gt utils.JSON, templateTitle string, templateBody string, err error) {
	_, templateMessage, err := g.FCMTemplate.ShouldGetByNameId(l, nameId)
	if err != nil {
		return nil, "", "", err
	}
	templateTitle, ok := templateMessage["subject"].(string)
	if !ok {
		return nil, "", "", errors.New("INVALID_TEMPLATE_TITLE")
	}
	templateBody, ok = templateMessage["body"].(string)
	if !ok {
		return nil, "", "", errors.New("INVALID_TEMPLATE_BODY")
	}
	return templateMessage, templateTitle, templateBody, nil
}*/

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
