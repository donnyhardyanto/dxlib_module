package resetdatabases

import (
	"fmt"

	"github.com/donnyhardyanto/dxlib/base"
)

// GetAdminDatabaseName returns the appropriate admin database name for each database type
func GetAdminDatabaseName(dbType base.DXDatabaseType) (string, error) {
	switch dbType {
	case base.DXDatabaseTypePostgreSQL, base.DXDatabaseTypePostgresSQLV2:
		return "postgres", nil
	case base.DXDatabaseTypeMariaDB:
		return "mysql", nil
	case base.DXDatabaseTypeSQLServer:
		return "master", nil
	case base.DXDatabaseTypeOracle:
		return "system", nil
	default:
		return "", fmt.Errorf("unsupported database type: %s", dbType.String())
	}
}

// GetAdminDatabaseEnvPrefix returns the environment variable prefix for admin database connection
func GetAdminDatabaseEnvPrefix(dbType base.DXDatabaseType) string {
	switch dbType {
	case base.DXDatabaseTypePostgreSQL, base.DXDatabaseTypePostgresSQLV2:
		return "DB_POSTGRES"
	case base.DXDatabaseTypeMariaDB:
		return "DB_MYSQL"
	case base.DXDatabaseTypeSQLServer:
		return "DB_SQLSERVER"
	case base.DXDatabaseTypeOracle:
		return "DB_ORACLE"
	default:
		return "DB_ADMIN"
	}
}
