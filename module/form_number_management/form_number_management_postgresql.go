package form_number_management

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
