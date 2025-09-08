package form_number_management

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
