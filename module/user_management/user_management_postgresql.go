package user_management

func getPostgreSQLOrganizationIdsFragment(userIdRef string) string {
	return `COALESCE(
        (SELECT ARRAY_AGG(uom2.organization_id ORDER BY uom2.order_index)
         FROM user_management.user_organization_membership uom2
         WHERE uom2.user_id = ` + userIdRef + `),
        ARRAY[]::BIGINT[])`
}

func getPostgreSQLRoleNamesTextFragment(userIdRef string) string {
	return `(SELECT string_agg(DISTINCT r.name, ', ' ORDER BY r.name)
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}

func getPostgreSQLRoleNameidsTextFragment(userIdRef string) string {
	return `(SELECT string_agg(DISTINCT r.nameid, ', ' ORDER BY r.nameid)
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}
