// Package form_number_management provides cross-database form number generation with automatic monthly reset
package form_number_management

import (
	"fmt"
	"time"

	"github.com/donnyhardyanto/dxlib/database/database_type"
	"github.com/donnyhardyanto/dxlib/database/protected/db"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/table"
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
		return "", fmt.Errorf("invalid timezone '%s': %w", timezone, err)
	}

	// Get current year and month separately in specified timezone
	now := time.Now().In(loc)
	year := now.Year()
	month := int(now.Month())

	var query string
	var args []interface{}

	switch fnm.FormNumberCounter.Database.DatabaseType {
	case database_type.PostgreSQL:
		query = fnm.getPostgreSQLQuery()
		args = []interface{}{nameid, year, month}

	case database_type.Oracle:
		query = fnm.getOracleQuery()
		args = []interface{}{nameid, year, month} // 3 parameters instead of 6

	case database_type.SQLServer:
		query = fnm.getSQLServerQuery()
		args = []interface{}{nameid, year, month}

	case database_type.MariaDB:
		query = fnm.getMariaDBQuery()
		args = []interface{}{nameid, year, month, year, month} // 5 parameters: INSERT (3) + UPDATE (2)
	default:
		return "", fmt.Errorf("unsupported database type: %s", fnm.FormNumberCounter.Database.DatabaseType)
	}

	_, r, err := db.QueryRows(fnm.FormNumberCounter.Database.Connection, nil, query, args)
	if err != nil {
		return "", fmt.Errorf("failed to generate form number: %w", err)
	}

	// SAFER:
	if len(r) == 0 {
		return "", fmt.Errorf("no result returned from form number generation query")
	}
	rr := r[0]

	formNumberType := rr["type"].(string)
	formNumberTemplate := rr["template"].(string)
	formNumberPrefix := rr["prefix"].(string)
	formNumberLastYear := rr["last_year"].(int)
	formNumberLastMonth := rr["last_month"].(int)
	formNumberLastSequence := rr["last_sequence"].(int)

	// Format form number
	formNumber := ""
	switch formNumberType {
	case "CONTINUOUS":
		formNumber = fmt.Sprintf(formNumberTemplate, formNumberPrefix, formNumberLastSequence)
	case "RESET_PER_MONTH":
		// Reconstruct YYMM format for backward compatibility
		formNumberLastYearMonth := fmt.Sprintf("%02d%02d", formNumberLastYear%100, formNumberLastMonth)
		formNumber = fmt.Sprintf(formNumberTemplate, formNumberPrefix, formNumberLastYearMonth, formNumberLastSequence)
	}

	return formNumber, nil
}

// GenerateWithLocal is a convenience method that uses local system timezone
func (fnm *DxmFormNumberManagement) GenerateWithLocal(nameid string) (string, error) {
	return fnm.Generate(nameid, "Local")
}

// Database-specific query implementations

func (fnm *DxmFormNumberManagement) getPostgreSQLQuery() string {
	return `
        INSERT INTO form_number_management.form_number_counters (nameid, last_year, last_month, last_sequence)
        VALUES ($1, $2, $3, 1)
        ON CONFLICT (nameid) DO UPDATE SET
            last_year = $2,
            last_month = $3,
            last_sequence = CASE 
                WHEN form_number_management.form_number_counters.type = 'RESET_PER_MONTH' THEN
                    CASE 
                        WHEN form_number_management.form_number_counters.last_year = $2 
                             AND form_number_management.form_number_counters.last_month = $3 
                        THEN form_number_management.form_number_counters.last_sequence + 1
                        ELSE 1
                    END
                WHEN form_number_management.form_number_counters.type = 'CONTINUOUS' THEN
                    form_number_management.form_number_counters.last_sequence + 1
                ELSE form_number_management.form_number_counters.last_sequence + 1
            END,
            updated_at = CURRENT_TIMESTAMP
        RETURNING type, template, prefix, last_year, last_month, last_sequence, updated_at
    `
}

func (fnm *DxmFormNumberManagement) getOracleQuery() string {
	return `
        MERGE INTO form_number_management.form_number_counters fc
        USING (SELECT :1 as nameid, :2 as last_year, :3 as last_month FROM dual) src
        ON (fc.nameid = src.nameid)
        WHEN MATCHED THEN
            UPDATE SET 
                last_year = src.last_year,
                last_month = src.last_month,
                last_sequence = CASE 
                    WHEN fc.type = 'RESET_PER_MONTH' THEN
                        CASE 
                            WHEN fc.last_year = src.last_year AND fc.last_month = src.last_month 
                            THEN fc.last_sequence + 1
                            ELSE 1
                        END
                    WHEN fc.type = 'CONTINUOUS' THEN
                        fc.last_sequence + 1
                    ELSE fc.last_sequence + 1
                END,
                updated_at = CURRENT_TIMESTAMP
        WHEN NOT MATCHED THEN
            INSERT (nameid, last_year, last_month, last_sequence, updated_at)
            VALUES (src.nameid, src.last_year, src.last_month, 1, CURRENT_TIMESTAMP)
        RETURNING type, template, prefix, last_year, last_month, last_sequence, updated_at
    `
}

func (fnm *DxmFormNumberManagement) getSQLServerQuery() string {
	return `
        WITH upsert AS (
            MERGE form_number_management.form_number_counters AS target
            USING (SELECT ? as nameid, ? as last_year, ? as last_month) AS source
            ON target.nameid = source.nameid
            WHEN MATCHED THEN
                UPDATE SET 
                    last_year = source.last_year,
                    last_month = source.last_month,
                    last_sequence = CASE 
                        WHEN target.type = 'RESET_PER_MONTH' THEN
                            CASE 
                                WHEN target.last_year = source.last_year AND target.last_month = source.last_month 
                                THEN target.last_sequence + 1
                                ELSE 1
                            END
                        WHEN target.type = 'CONTINUOUS' THEN
                            target.last_sequence + 1
                        ELSE target.last_sequence + 1
                    END,
                    updated_at = GETDATE()
            WHEN NOT MATCHED THEN
                INSERT (nameid, last_year, last_month, last_sequence)
                VALUES (source.nameid, source.last_year, source.last_month, 1)
            OUTPUT inserted.type, inserted.template, inserted.prefix, 
                   inserted.last_year, inserted.last_month, inserted.last_sequence, inserted.updated_at
        )
        SELECT type, template, prefix, last_year, last_month, last_sequence, updated_at FROM upsert
    `
}

// The  RETURNING clause need using modern MariaDB versions (10.5+) ,
func (fnm *DxmFormNumberManagement) getMariaDBQuery() string {
	return `
        INSERT INTO form_number_management.form_number_counters (nameid, last_year, last_month, last_sequence)
        VALUES (?, ?, ?, 1)
        ON DUPLICATE KEY UPDATE
            last_year = VALUES(last_year),
            last_month = VALUES(last_month),
            last_sequence = CASE 
                WHEN type = 'RESET_PER_MONTH' THEN
                    CASE 
                        WHEN last_year = ? AND last_month = ? 
                        THEN last_sequence + 1
                        ELSE 1
                    END
                WHEN type = 'CONTINUOUS' THEN
                    last_sequence + 1
                ELSE last_sequence + 1
            END,
            updated_at = NOW()
        RETURNING type, template, prefix, last_year, last_month, last_sequence, updated_at
    `
}

var ModuleFormNumberManagement DxmFormNumberManagement

func init() {
	ModuleFormNumberManagement = DxmFormNumberManagement{}
}
