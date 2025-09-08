package form_number_management

func (fnm *DxmFormNumberManagement) getOracleQuery() string {
	return `
MERGE INTO form_number_management.form_number_counters fc
USING (SELECT :1 AS nameid, :2 AS last_year, :3 AS last_month FROM DUAL) src
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
