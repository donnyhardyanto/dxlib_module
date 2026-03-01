// Package form_number_management provides cross-databases form number generation with automatic monthly reset
package form_number_management

import (
	"context"
	"fmt"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases/db"
	"github.com/donnyhardyanto/dxlib/errors"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/tables"
)

type DxmFormNumberManagement struct {
	dxlibModule.DXModule

	FormNumberCounter *tables.DXRawTable
}

func (fnm *DxmFormNumberManagement) Init(databaseNameId string) {
	fnm.DatabaseNameId = databaseNameId
	fnm.FormNumberCounter = tables.NewDXRawTableSimple(fnm.DatabaseNameId,
		"form_number_management.form_number_counters", "form_number_management.form_number_counters", "form_number_management.form_number_counters",
		"id", "uid", "nameid", "data",
		nil,
		[][]string{{"nameid"}},
		[]string{"nameid", "type", "prefix"},
		[]string{"id", "nameid", "type", "last_year", "last_month", "last_sequence"},
		[]string{"id", "uid", "nameid", "type", "created_at", "last_modified_at"},
	)
}

// Generate creates a new form number with automatic monthly reset
// Format: {formType}-YYMMNNNNNN where YYMM is year-month and NNNNNN is 6-digit sequence
// timezone: IANA timezone name (e.g., "Asia/Jakarta", "UTC", "America/New_York")
func (fnm *DxmFormNumberManagement) Generate(ctx context.Context, nameid string, timezone string) (string, error) {

	if timezone == "" {
		timezone = "UTC"
	}

	// Load timezone location
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return "", errors.Errorf("invalid timezone '%s': %+v", timezone, err)
	}

	// Get the current year and month separately in ma specified timezone
	now := time.Now().In(loc)
	year := now.Year()
	month := int(now.Month())

	var query string
	var args []interface{}

	err = fnm.FormNumberCounter.EnsureDatabase()
	if err != nil {
		return "", errors.Errorf("failed to ensure databases connection: %+v", err)
	}

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
		return "", errors.Errorf("unsupported databases type: %s", fnm.FormNumberCounter.Database.DatabaseType)
	}

	_, r, err := db.RawQueryRows(ctx, fnm.FormNumberCounter.Database.Connection, nil, query, args)
	if err != nil {
		return "", errors.Errorf("failed to generate form number: %+v", err)
	}

	// SAFER:
	if len(r) == 0 {
		return "", errors.Errorf("no result returned from form number generation query")
	}
	rr := r[0]

	formNumberType, ok := rr["type"].(string)
	if !ok {
		return "", errors.Errorf("form number record 'type' is missing or not a string")
	}
	formNumberTemplate, ok := rr["template"].(string)
	if !ok {
		return "", errors.Errorf("form number record 'template' is missing or not a string")
	}
	formNumberPrefix, ok := rr["prefix"].(string)
	if !ok {
		return "", errors.Errorf("form number record 'prefix' is missing or not a string")
	}
	formNumberLastYear, ok := rr["last_year"].(int64)
	if !ok {
		return "", errors.Errorf("form number record 'last_year' is missing or not an int64")
	}
	formNumberLastMonth, ok := rr["last_month"].(int64)
	if !ok {
		return "", errors.Errorf("form number record 'last_month' is missing or not an int64")
	}
	formNumberLastSequence, ok := rr["last_sequence"].(int64)
	if !ok {
		return "", errors.Errorf("form number record 'last_sequence' is missing or not an int64")
	}

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
func (fnm *DxmFormNumberManagement) GenerateWithLocal(ctx context.Context, nameid string) (string, error) {
	return fnm.Generate(ctx, nameid, "Local")
}

var ModuleFormNumberManagement DxmFormNumberManagement

func init() {
	ModuleFormNumberManagement = DxmFormNumberManagement{}
}
