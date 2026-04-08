package user_management

func (um *DxmUserManagement) getMariaDBOrganizationIdsFragment() string {
	return `(SELECT CONCAT('[', IFNULL(GROUP_CONCAT(uom2.organization_id ORDER BY uom2.order_index SEPARATOR ','), ''), ']')
        FROM user_management.user_organization_membership uom2
        WHERE uom2.user_id = a.id) AS organization_ids`
}
