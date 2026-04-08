package user_management

func getSQLServerOrganizationIdsFragment(userIdRef string) string {
	return `(SELECT '[' + ISNULL(STRING_AGG(CAST(uom2.organization_id AS NVARCHAR(20)), ',') WITHIN GROUP (ORDER BY uom2.order_index), '') + ']'
        FROM user_management.user_organization_membership uom2
        WHERE uom2.user_id = ` + userIdRef + `)`
}

func getSQLServerRoleNamesTextFragment(userIdRef string) string {
	return `(SELECT STRING_AGG(r.name, ', ') WITHIN GROUP (ORDER BY r.name)
        FROM (SELECT DISTINCT r2.name FROM user_management.user_role_membership urm2
                 JOIN user_management.role r2 ON urm2.role_id = r2.id
        WHERE urm2.user_id = ` + userIdRef + `) r)`
}

func getSQLServerRoleNameidsTextFragment(userIdRef string) string {
	return `(SELECT STRING_AGG(r.nameid, ', ') WITHIN GROUP (ORDER BY r.nameid)
        FROM (SELECT DISTINCT r2.nameid FROM user_management.user_role_membership urm2
                 JOIN user_management.role r2 ON urm2.role_id = r2.id
        WHERE urm2.user_id = ` + userIdRef + `) r)`
}
