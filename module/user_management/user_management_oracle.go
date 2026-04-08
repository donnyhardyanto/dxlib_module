package user_management

func getOracleOrganizationIdsFragment(userIdRef string) string {
	return `NVL(
        (SELECT '[' || LISTAGG(uom2.organization_id, ',') WITHIN GROUP (ORDER BY uom2.order_index) || ']'
         FROM user_management.user_organization_membership uom2
         WHERE uom2.user_id = ` + userIdRef + `),
        '[]')`
}

func getOracleRoleNamesTextFragment(userIdRef string) string {
	return `(SELECT LISTAGG(DISTINCT r.name, ', ') WITHIN GROUP (ORDER BY r.name)
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}

func getOracleRoleNameidsTextFragment(userIdRef string) string {
	return `(SELECT LISTAGG(DISTINCT r.nameid, ', ') WITHIN GROUP (ORDER BY r.nameid)
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}
