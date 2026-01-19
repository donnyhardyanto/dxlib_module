// Package form_number_management provides cross-database form number generation with automatic monthly reset
package form_number_management

import (
	"fmt"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
	"github.com/pkg/errors"
)

type DxmFormNumberManagement struct {
	dxlibModule.DXModule

	FormNumberCounter *table.DXRawTable
}

func (fnm *DxmFormNumberManagement) Init(databaseNameId string) {
	fnm.DatabaseNameId = databaseNameId
	fnm.FormNumberCounter = table.Manager.NewRawTable(fnm.DatabaseNameId, "form_number_management.form_number_counters", "form_number_management.form_number_counters",
		"form_number_management.form_number_counters", "nameid", "id", "uid", "data")
}

// Generate creates a new form number with automatic monthly reset
// Format: {formType}-YYMMNNNNNN where YYMM is year-month and NNNNNN is 6-digit sequence
// timezone: IANA timezone name (e.g., "Asia/Jakarta", "UTC", "America/New_York")
func (fnm *DxmFormNumberManagement) Generate(nameid string, timezone string) (string, error) {

	if timezone == "" {
		timezone = "UTC"
	}

	// Load timezone location
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return "", errors.Errorf("invalid timezone '%s': %+v", timezone, err)
	}

	// Get current year and month separately in specified timezone
	now := time.Now().In(loc)
	year := now.Year()
	month := int(now.Month())

	var query string
	var args []interface{}

	switch fnm.FormNumberCounter.Database.DatabaseType {
	case base.DXDatabaseTypePostgreSQL:
		query = fnm.getPostgreSQLQuery()
		args = []interface{}{nameid, year, month}

	case base.DXDatabaseTypeOracle:
		query = fnm.getOracleQuery()
		args = []interface{}{nameid, year, month} // 3 parameters instead of 6

	case base.DXDatabaseTypeSQLServer:
		query = fnm.getSQLServerQuery()
		args = []interface{}{nameid, year, month}

	case base.DXDatabaseTypeMariaDB:
		query = fnm.getMariaDBQuery()
		args = []interface{}{nameid, year, month, year, month} // 5 parameters: INSERT (3) + UPDATE (2)
	default:
		return "", errors.Errorf("unsupported database type: %s", fnm.FormNumberCounter.Database.DatabaseType)
	}

	_, r, err := db.QueryRows(fnm.FormNumberCounter.Database.Connection, nil, query, args)
	if err != nil {
		return "", errors.Errorf("failed to generate form number: %+v", err)
	}

	// SAFER:
	if len(r) == 0 {
		return "", errors.Errorf("no result returned from form number generation query")
	}
	rr := r[0]

	formNumberType := rr["type"].(string)
	formNumberTemplate := rr["template"].(string)
	formNumberPrefix := rr["prefix"].(string)
	formNumberLastYear := rr["last_year"].(int64)
	formNumberLastMonth := rr["last_month"].(int64)
	formNumberLastSequence := rr["last_sequence"].(int64)

	// Format form number
	formNumber := ""
	switch formNumberType {
	case "CONTINUOUS":
		formNumber = fmt.Sprintf(formNumberTemplate, formNumberPrefix, formNumberLastSequence)
	case "RESET_PER_MONTH":
		// Reconstruct YYMM format for backward compatibility
		formNumberLastYearMonth := fmt.Sprintf("%02d%02d", formNumberLastYear%100, formNumberLastMonth)
		formNumber = fmt.Sprintf(formNumberTemplate, formNumberPrefix, formNumberLastYearMonth, formNumberLastSequence)
	default:
		return "", errors.Errorf("invalid form number type '%s'", formNumberType)
	}

	return formNumber, nil
}

// GenerateWithLocal is a convenience method that uses local system timezone
func (fnm *DxmFormNumberManagement) GenerateWithLocal(nameid string) (string, error) {
	return fnm.Generate(nameid, "Local")
}

var ModuleFormNumberManagement DxmFormNumberManagement

func init() {
	ModuleFormNumberManagement = DxmFormNumberManagement{}
}
