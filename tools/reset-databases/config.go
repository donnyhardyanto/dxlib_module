package resetdatabases

import "github.com/donnyhardyanto/dxlib/base"

// Config holds all configuration for the database reset tool
type Config struct {
	// Project identification
	ProjectName        string
	ProjectDescription string

	// Confirmation settings
	ConfirmationKey1 string
	ConfirmationKey2 string

	// Databases to reset
	Databases []DatabaseConfig

	// Callbacks for project-specific logic
	OnDefineConfiguration func() error
	OnDefineSetVariables  func() error
	OnSeed                func() error

	// Environment variable customization
	EnvVarPrefix string // e.g., "PGN_PARTNER" generates "IS_PGN_PARTNER_RESET_DELETE_AND_CREATE_DB"

	// DDL output settings
	DDLOutputFolder string // default: "."

	// Redis configuration handling (optional)
	DisableRedisConnections bool // default: true
}

// DatabaseConfig represents a single database to reset
type DatabaseConfig struct {
	// Identification
	NameId      string // e.g., "config" (key in databases.Manager.Databases)
	DisplayName string // e.g., "Config Database" (for error messages)

	// Database model
	Model DatabaseModel

	// DDL generation
	CreateDDL func(dbType base.DXDatabaseType) (string, error)

	// SQL content provider
	GetSQLContent func(path string) (string, error)

	// DDL output file name
	DDLOutputFileName string // e.g., "db_partner_config.sql"
}

// DatabaseModel interface that all database models must implement
type DatabaseModel interface {
	Init() error
}
