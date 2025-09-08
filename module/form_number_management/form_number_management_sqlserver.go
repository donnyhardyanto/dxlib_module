package form_number_management

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
