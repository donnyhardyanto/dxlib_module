package audit_log

import (
	"time"

	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/log"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/utils"
)

type DxmAudit struct {
	dxlibModule.DXModule
	/*	EventLog        *tables.DXTable
	 */
	UserActivityLog *tables.DXRawTable
	ErrorLog        *tables.DXRawTable
}

func (al *DxmAudit) Init(databaseNameId string) {
	al.UserActivityLog = tables.NewDXRawTableSimple(databaseNameId, "audit_log.user_activity_log",
		"audit_log.user_activity_log", "audit_log.user_activity_log", "id", "uid", "id", "data", nil, nil, nil)
	al.UserActivityLog.FieldMaxLengths = map[string]int{"error_message": 16000}

	al.ErrorLog = tables.NewDXRawTableSimple(databaseNameId, "audit_log.error_log",
		"audit_log.error_log", "audit_log.error_log", "id", "uid", "id", "data", nil, nil, nil)
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

	// Temporarily disable OnError to prevent infinite recursion when logging insert errors
	originalOnError := log.OnError
	log.OnError = nil

	_, err = ModuleAuditLog.ErrorLog.InsertReturningId(&log.Log, utils.JSON{
		"at":        time.Now(),
		"prefix":    app.App.NameId + " " + app.App.Version,
		"log_level": logLevelAsString,
		"location":  location,
		"message":   st,
		"stack":     stack,
	})

	// Restore OnError
	log.OnError = originalOnError

	if err != nil {
		log.Log.Errorf(err, "AUDIT_LOG_INSERT_ERROR_LOG_FAILED: failed to insert error log to databases")
		return err
	}
	return nil
}

var ModuleAuditLog DxmAudit

func init() {
	ModuleAuditLog = DxmAudit{}
}
