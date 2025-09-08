// Package form_number provides cross-database form number generation with automatic monthly reset
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

	// Get current YYMM in specified timezone
	now := time.Now().In(loc)
	yearMonth := fmt.Sprintf("%02d%02d", now.Year()%100, int(now.Month()))

	var query string
	var args []interface{}

	switch fnm.FormNumberCounter.Database.DatabaseType {
	case database_type.PostgreSQL:
		query = fnm.getPostgreSQLQuery()
		args = []interface{}{nameid, yearMonth}

	case database_type.Oracle:
		query = fnm.getOracleQuery()
		args = []interface{}{nameid, yearMonth, nameid, yearMonth}

	case database_type.SQLServer:
		query = fnm.getSQLServerQuery()
		args = []interface{}{nameid, yearMonth}

	case database_type.MySQL:
		query = fnm.getMariaDBQuery()
		args = []interface{}{nameid, yearMonth, yearMonth}

	default:
		return "", fmt.Errorf("unsupported database type: %s", fnm.FormNumberCounter.Database.DatabaseType)
	}

	_, r, err := db.QueryRows(fnm.FormNumberCounter.Database.Connection, nil, query, args)
	if err != nil {
		return "", fmt.Errorf("failed to generate form number: %w", err)
	}

	rr := r[0]
	formNumberType := rr["type"].(string)
	formNumberTemplate := rr["template"].(string)
	formNumberPrefix := rr["prefix"].(string)
	formNumberLastYearMonth := rr["last_year_month"].(string)
	formNumberLastSequence := rr["last_sequence"].(int)

	// Format form number
	formNumber := ""
	switch formNumberType {
	case "CONTINUES":
		formNumber = fmt.Sprintf(formNumberTemplate, formNumberPrefix, formNumberLastSequence)
	case "RESET_PER_MONTH":
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
        INSERT INTO form_number_management.form_number_counters (nameid, last_year_month, last_sequence)
        VALUES ($1, $2, 1)
        ON CONFLICT (nameid) DO UPDATE SET
            last_year_month = $2,
            last_sequence = CASE 
                WHEN form_number_management.form_number_counters.last_year_month = $2 THEN form_number_management.form_number_counters.last_sequence + 1
                ELSE 1
            END,
            updated_at = CURRENT_TIMESTAMP
        RETURNING last_sequence, updated_at
    `
}

func (fnm *DxmFormNumberManagement) getOracleQuery() string {
	return `
        MERGE INTO form_number_management.form_number_counters fc
        USING (SELECT ? as nameid, ? as last_year_month FROM dual) src
        ON (fc.nameid = src.nameid)
        WHEN MATCHED THEN
            UPDATE SET 
                last_year_month = src.year_month,
                last_sequence = CASE 
                    WHEN fc.last_year_month = src.last_year_month THEN fc.last_sequence + 1
                    ELSE 1
                END,
                updated_at = CURRENT_TIMESTAMP
        WHEN NOT MATCHED THEN
            INSERT (nameid, last_year_month, last_sequence)
            VALUES (?, ?, 1)
    `
}

func (fnm *DxmFormNumberManagement) getSQLServerQuery() string {
	return `
        WITH upsert AS (
            MERGE form_number_management.form_number_counters AS target
            USING (SELECT ? as nameid, ? as last_year_month) AS source
            ON target.nameid = source.nameid
            WHEN MATCHED THEN
                UPDATE SET 
                    last_year_month = source.last_year_month,
                    last_sequence = CASE 
                        WHEN target.last_year_month = source.last_year_month THEN target.last_sequence + 1
                        ELSE 1
                    END,
                    updated_at = GETDATE()
            WHEN NOT MATCHED THEN
                INSERT (nameid, last_year_month, last_sequence)
                VALUES (source.nameid, source.last_year_month, 1)
            OUTPUT inserted.last_sequence, inserted.updated_at
        )
        SELECT last_sequence, updated_at FROM upsert
    `
}

func (fnm *DxmFormNumberManagement) getMariaDBQuery() string {
	return `
        INSERT INTO form_number_management.form_number_counters (nameid, last_year_month, last_sequence)
        VALUES (?, ?, 1)
        ON DUPLICATE KEY UPDATE
            last_year_month = VALUES(last_year_month),
            last_sequence = CASE 
                WHEN last_year_month = ? THEN last_sequence + 1
                ELSE 1
            END,
            updated_at = NOW()
    `
}

var ModuleFormNumberManagement DxmFormNumberManagement

func init() {
	ModuleFormNumberManagement = DxmFormNumberManagement{}
}
