package general

import (
	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/lib"
	"strconv"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	Property            *table.DXTable
	Announcements       *table.DXTable
	AnnouncementPicture *lib.ImageObjectStorage
}

func (g *DxmGeneral) DefineTables(databaseNameId string) {
	g.Property = table.Manager.NewTable(databaseNameId, "general.property",
		"general.property",
		"general.property", `nameid`, `id`)
	g.Announcements = table.Manager.NewTable(databaseNameId, "general.announcements",
		"general.announcements",
		"general.announcements", `uid`, `id`)
}

func (g *DxmGeneral) PropertyGetAsString(l *dxlibLog.DXLog, propertyId string) (string, error) {
	_, v, err := g.Property.MustSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return "", err
	}
	vv, ok := v["value"].(string)
	if !ok {
		err := l.ErrorAndCreateErrorf("PropertyGetAsString: value is not string: %v", v["value"])
		return "", err
	}
	return vv, nil
}

func (g *DxmGeneral) PropertyGetAsInteger(l *dxlibLog.DXLog, propertyId string) (int, error) {
	_, v, err := g.Property.MustSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return 0, err
	}
	vv, ok := v["value"].(string)
	if !ok {
		err := l.ErrorAndCreateErrorf("PropertyGetAsString: value is not string: %v", v["value"])
		return 0, err
	}
	vvi, err := strconv.Atoi(vv)
	if err != nil {
		err := l.ErrorAndCreateErrorf("PropertyGetAsInteger: strconv.Atoi error: %v", err.Error())
		return 0, err
	}
	return vvi, nil
}

var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
