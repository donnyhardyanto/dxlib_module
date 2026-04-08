package user_management

func getMariaDBOrganizationIdsFragment(userIdRef string) string {
	return `(SELECT CONCAT('[', IFNULL(GROUP_CONCAT(uom2.organization_id ORDER BY uom2.order_index SEPARATOR ','), ''), ']')
        FROM user_management.user_organization_membership uom2
        WHERE uom2.user_id = ` + userIdRef + `)`
}

func getMariaDBRoleNamesTextFragment(userIdRef string) string {
	return `(SELECT GROUP_CONCAT(DISTINCT r.name ORDER BY r.name SEPARATOR ', ')
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}

func getMariaDBRoleNameidsTextFragment(userIdRef string) string {
	return `(SELECT GROUP_CONCAT(DISTINCT r.nameid ORDER BY r.nameid SEPARATOR ', ')
        FROM user_management.user_role_membership urm2
                 JOIN user_management.role r ON urm2.role_id = r.id
        WHERE urm2.user_id = ` + userIdRef + `)`
}
