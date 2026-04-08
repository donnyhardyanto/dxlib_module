package user_management

func (um *DxmUserManagement) getPostgreSQLOrganizationIdsFragment() string {
	return `(SELECT ARRAY_AGG(uom2.organization_id ORDER BY uom2.order_index)
        FROM user_management.user_organization_membership uom2
        WHERE uom2.user_id = a.id) AS organization_ids`
}
