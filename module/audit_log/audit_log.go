package audit_log

import (
	"time"

	"github.com/donnyhardyanto/dxlib/app"
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
		"log.event", "id", "id")*/
	al.UserActivityLog = table.Manager.NewRawTable(databaseNameId, "audit_log.user_activity_log",
		"audit_log.user_activity_log",
		"audit_log.user_activity_log", "id", "id", "uid", "data")
	al.UserActivityLog.FieldMaxLengths = map[string]int{"error_message": 16000}

	al.ErrorLog = table.Manager.NewRawTable(databaseNameId, "audit_log.error_log",
		"audit_log.error_log",
		"audit_log.error_log", "id", "id", "uid", "data")
	al.ErrorLog.FieldMaxLengths = map[string]int{"message": 16000}
}

func (al *DxmAudit) DoError(errPrev error, logLevel log.DXLogLevel, location string, text string, stack string) (err error) {
	if errPrev != nil {
		text = errPrev.Error() + "\n" + text
	}
	if logLevel > log.DXLogLevelWarn {
		return
	}
	l := len(text)
	st := ""
	if l >= 16000 {
		st = text[:16000] + "..."
	} else {
		st = text
	}
	logLevelAsString := log.DXLogLevelAsString[logLevel]
	_, err = ModuleAuditLog.ErrorLog.Insert(&log.Log, utils.JSON{
		"at":        time.Now(),
		"prefix":    app.App.NameId + " " + app.App.Version,
		"log_level": logLevelAsString,
		"location":  location,
		"message":   st,
		"stack":     stack,
	})
	if err != nil {
		log.Log.Panic(location, err)
		return
	}
	return nil
}

var ModuleAuditLog DxmAudit

func init() {
	ModuleAuditLog = DxmAudit{}
}
