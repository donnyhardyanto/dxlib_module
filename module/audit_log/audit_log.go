package audit_log

import (
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
)

type DxmAudit struct {
	dxlibModule.DXModule
	EventLog        *table.DXTable
	UserActivityLog *table.DXTable
}

func (al *DxmAudit) Init(databaseNameId string) {
	al.EventLog = table.Manager.NewTable(databaseNameId, "log.event",
		"log.event",
		"log.event", `id`, `id`)
	al.UserActivityLog = table.Manager.NewTable(databaseNameId, "audit.user_activity_log",
		"audit.user_activity_log",
		"audit.user_activity_log", `id`, `id`)
}

var ModuleAudit DxmAudit

func init() {
	ModuleAudit = DxmAudit{}
}
