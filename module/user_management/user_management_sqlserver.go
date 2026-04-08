package user_management

func (um *DxmUserManagement) getSQLServerOrganizationIdsFragment() string {
	return `(SELECT '[' + ISNULL(STRING_AGG(CAST(uom2.organization_id AS NVARCHAR(20)), ',') WITHIN GROUP (ORDER BY uom2.order_index), '') + ']'
        FROM user_management.user_organization_membership uom2
        WHERE uom2.user_id = a.id) AS organization_ids`
}
