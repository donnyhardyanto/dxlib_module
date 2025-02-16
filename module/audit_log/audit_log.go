package audit_log

import (
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/donnyhardyanto/dxlib/utils"
)

type DxmAudit struct {
	dxlibModule.DXModule
	/*	EventLog        *table.DXTable
	 */
	UserActivityLog *table.DXRawTable
	ErrorLog        *table.DXRawTable
}

func (al *DxmAudit) Init(databaseNameId string) {
	/*	al.EventLog = table.Manager.NewTable(databaseNameId, "log.event",
		"log.event",
		"log.event", `id`, `id`)*/
	al.UserActivityLog = table.Manager.NewRawTable(databaseNameId, "audit.user_activity_log",
		"audit.user_activity_log",
		"audit.user_activity_log", `id`, `id`, "uid")
	al.ErrorLog = table.Manager.NewRawTable(databaseNameId, "log.error",
		"log.error",
		"log.error", `id`, `id`, "uid")
}

func (al *DxmAudit) DoError(logLevel log.DXLogLevel, location string, text string, stack string) (err error) {
	if logLevel > log.DXLogLevelError {
		return
	}
	logLevelAsString := log.DXLogLevelAsString[logLevel]
	_, err = ModuleAudit.ErrorLog.Insert(&log.Log, utils.JSON{
		"log_level": logLevelAsString,
		"location":  location,
		"message":   text,
		"stack":     stack,
	})
	if err != nil {
		log.Log.Panic(location, err)
		return
	}
	return nil
}

var ModuleAudit DxmAudit

func init() {
	ModuleAudit = DxmAudit{}
}
