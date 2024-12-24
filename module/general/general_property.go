package general

import (
	// "encoding/json"
	//	"github.com/donnyhardyanto/dxlib/database"
	//	dxlibLog "github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	//	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/lib"
)

type DxmGeneral struct {
	dxlibModule.DXModule
	//	Property            *table.DXTable
	Property            *table.DXPropertyTable
	Announcement        *table.DXTable
	AnnouncementPicture *lib.ImageObjectStorage
}

func (g *DxmGeneral) Init(databaseNameId string) {
	g.DatabaseNameId = databaseNameId
	/*	g.Property = table.Manager.NewTable(databaseNameId, "general.property",
		"general.property",
		"general.property", `nameid`, `id`)
	*/
	g.Property = table.Manager.NewPropertyTable(databaseNameId, "general.property",
		"general.property",
		"general.property", `nameid`, `id`)
	g.Announcement = table.Manager.NewTable(databaseNameId, "general.announcement",
		"general.announcement",
		"general.announcement", `uid`, `id`)
}

/*func (g *DxmGeneral) PropertyGetAsString(l *dxlibLog.DXLog, propertyId string) (string, error) {
	_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return "", err
	}

	aType, ok := v["type"].(string)
	if !ok {
		return "", l.ErrorAndCreateErrorf("PropertyGetAsString: type is not string: %v", v["type"])
	}

	value, err := utils.GetJSONFromKV(v, "value")
	if err != nil {
		return "", l.ErrorAndCreateErrorf("PropertyGetAsString:CAN_NOT_GET_JSON_VALUE:%v", err)
	}
	vv, ok := value[aType].(string)
	if !ok {
		return "", l.ErrorAndCreateErrorf("PropertyGetAsString: value is not a number: %v", value[aType])
	}

	return vv, nil
}

func (g *DxmGeneral) PropertyTxSetAsString(dtx *database.DXDatabaseTx, propertyId string, value string) (err error) {
	_, err = g.Property.TxInsert(dtx, utils.JSON{
		"nameid": propertyId,
		"type":   "STRING",
		"value":  MustJsonMarshal(utils.JSON{"STRING": value}),
	})
	return err
}
*/
/*
	func (g *DxmGeneral) PropertyGetAsString(l *dxlibLog.DXLog, propertyId string) (string, error) {
		_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
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
*/
/*func (g *DxmGeneral) PropertyGetAsInteger(l *dxlibLog.DXLog, propertyId string) (int, error) {
	_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
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
*/
/*func (g *DxmGeneral) PropertyGetAsInteger(l *dxlibLog.DXLog, propertyId string) (int, error) {
	_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return 0, err
	}

	aType, ok := v["type"].(string)
	if !ok {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger: type is not string: %v", v["type"])
	}

	value, err := utils.GetJSONFromKV(v, "value")
	if err != nil {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger:CAN_NOT_GET_JSON_VALUE:%v", err)
	}
	vv, ok := value[aType].(float64)
	if !ok {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger: value is not a number: %v", value[aType])
	}

	return int(vv), nil
}

func (g *DxmGeneral) PropertyTxSetAsInteger(dtx *database.DXDatabaseTx, propertyId string, value int) (err error) {
	_, err = g.Property.TxInsert(dtx, utils.JSON{
		"nameid": propertyId,
		"type":   "INT",
		"value":  MustJsonMarshal(utils.JSON{"INT": value}),
	})
	return err
}

func (g *DxmGeneral) PropertyGetAsInt64(l *dxlibLog.DXLog, propertyId string) (int64, error) {
	_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return 0, err
	}

	aType, ok := v["type"].(string)
	if !ok {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger: type is not string: %v", v["type"])
	}

	value, err := utils.GetJSONFromKV(v, "value")
	if err != nil {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger:CAN_NOT_GET_JSON_VALUE:%v", err)
	}
	vv, ok := value[aType].(float64)
	if !ok {
		return 0, l.ErrorAndCreateErrorf("PropertyGetAsInteger: value is not a number: %v", value[aType])
	}

	return int64(vv), nil
}

func MustJsonMarshal(u utils.JSON) []byte {
	b, err := json.Marshal(u)
	if err != nil {
		panic(err)
	}
	return b
}

func (g *DxmGeneral) PropertyTxSetAsInt64(dtx *database.DXDatabaseTx, propertyId string, value int64) (err error) {
	_, err = g.Property.TxInsert(dtx, utils.JSON{
		"nameid": propertyId,
		"type":   "INT64",
		"value":  MustJsonMarshal(utils.JSON{"INT64": value}),
	})
	return err
}

func (g *DxmGeneral) PropertyTxSetAsJSON(dtx *database.DXDatabaseTx, propertyId string, value map[string]any) (err error) {
	_, property, err := g.Property.TxSelectOne(dtx, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return err
	}
	if property == nil {
		_, err = g.Property.TxInsert(dtx, utils.JSON{
			"nameid": propertyId,
			"type":   "JSON",
			"value":  MustJsonMarshal(utils.JSON{"JSON": value}),
		})
		if err != nil {
			return err
		}
	} else {
		_, err = g.Property.TxUpdate(dtx, utils.JSON{
			"value": MustJsonMarshal(utils.JSON{"JSON": value}),
		}, utils.JSON{
			"nameid": propertyId,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *DxmGeneral) PropertyGetAsJSON(l *dxlibLog.DXLog, propertyId string) (map[string]any, error) {
	_, v, err := g.Property.ShouldSelectOne(l, utils.JSON{
		"nameid": propertyId,
	}, nil)
	if err != nil {
		return nil, err
	}

	aType, ok := v["type"].(string)
	if !ok {
		return nil, l.ErrorAndCreateErrorf("PropertyGetAsJSON: type is not string: %v", v["type"])
	}

	value, err := utils.GetJSONFromKV(v, "value")
	if err != nil {
		return nil, l.ErrorAndCreateErrorf("PropertyGetAsJSON:CAN_NOT_GET_JSON_VALUE:%v", err)
	}
	vv, ok := value[aType].(map[string]any)
	if !ok {
		return nil, l.ErrorAndCreateErrorf("PropertyGetAsJSON: value is not a JSON: %v", value[aType])
	}

	return vv, nil
}
*/
var ModuleGeneral DxmGeneral

func init() {
	ModuleGeneral = DxmGeneral{}
}
