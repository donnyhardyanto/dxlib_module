package resetdatabases

import (
	"errors"
	"fmt"
	stdos "os"
	"strings"

	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/configuration"
	"github.com/donnyhardyanto/dxlib/databases"
	databaseUtils "github.com/donnyhardyanto/dxlib/databases/utils"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	osUtils "github.com/donnyhardyanto/dxlib/utils/os"
	"github.com/donnyhardyanto/dxlib/vault"
)

var (
	bypassConfirmation = false
	deleteAndCreateDb  = false
	isDev              = false
)

// Run executes the database reset tool with the provided configuration
func Run(config *Config) {
	// Set default values
	if config.DDLOutputFolder == "" {
		config.DDLOutputFolder = "."
	}

	// Initialize vault and app
	log.SetFormatSimple()
	app.App.InitVault = vault.NewHashiCorpVault(
		osUtils.GetEnvDefaultValue("VAULT_ADDRESS", "http://127.0.0.1:8200/"),
		osUtils.GetEnvDefaultValue("VAULT_TOKEN", " dev-vault-token"),
		"__VAULT__",
		osUtils.GetEnvDefaultValue("VAULT_PATH", "kv/data/pgn-partner-dev"),
	)

	// Encryption vault configuration - MUST be explicitly set, no defaults
	encryptionVaultAddress := osUtils.GetEnvDefaultWithFallback([]string{"ENCRYPTION_VAULT_ADDRESS", "VAULT_ADDRESS"}, "")
	encryptionVaultToken := osUtils.GetEnvDefaultWithFallback([]string{"ENCRYPTION_VAULT_TOKEN", "VAULT_TOKEN"}, "")
	encryptionVaultPath := osUtils.GetEnvDefaultWithFallback([]string{"ENCRYPTION_VAULT_PATH", "VAULT_PATH"}, "")

	if encryptionVaultPath == "" {
		log.Log.Fatal("ENCRYPTION_VAULT_PATH or VAULT_PATH environment variable must be set")
		stdos.Exit(1)
	}
	if encryptionVaultAddress == "" {
		log.Log.Fatal("ENCRYPTION_VAULT_ADDRESS or VAULT_ADDRESS environment variable must be set")
		stdos.Exit(1)
	}
	if encryptionVaultToken == "" {
		log.Log.Fatal("ENCRYPTION_VAULT_TOKEN or VAULT_TOKEN environment variable must be set")
		stdos.Exit(1)
	}

	app.App.EncryptionVault = vault.NewHashiCorpVault(
		encryptionVaultAddress,
		encryptionVaultToken,
		"__VAULT__",
		encryptionVaultPath,
	)

	app.Set(config.ProjectName,
		config.ProjectDescription,
		config.ProjectDescription,
		false,
		config.ProjectName+"-debug",
		"abc",
	)

	// Setup lifecycle hooks
	app.App.OnDefineConfiguration = func() error {
		return defineConfiguration(config)
	}
	app.App.OnAfterConfigurationStartAll = func() error {
		return executeReset(config)
	}
	app.App.OnDefineSetVariables = config.OnDefineSetVariables
	app.App.OnExecute = func() error {
		return executeSeed(config)
	}
	app.App.OnStartStorageReady = nil

	err := app.App.Run()
	if err != nil {
		stdos.Exit(1)
	}
}

// defineConfiguration sets up the configuration phase (extracted from doOnDefineConfiguration)
func defineConfiguration(config *Config) error {
	// Read IS_DEV environment variable to determine if running in local/dev environment
	isDev = osUtils.GetEnvDefaultValueAsBool(config.EnvVarPrefix+"_IS_DEV", false)

	// IS_DEV=true (Development): Allow a DB drop and bypass confirmation (both can be overridden)
	// IS_DEV=false (Staging/Production): Block a DB drop and require confirmation (CANNOT be overridden)

	deleteAndCreateDBEnvVar := config.EnvVarPrefix + "_RESET_DELETE_AND_CREATE_DB"
	bypassConfirmationEnvVar := config.EnvVarPrefix + "_RESET_BYPASS_CONFIRMATION"

	if isDev {
		t1 := osUtils.GetEnvDefaultValueAsBool(deleteAndCreateDBEnvVar, false)
		if t1 {
			deleteAndCreateDb = true
		}

		// DEVELOPMENT MODE: Bypass confirmation (default: false, can override to true)
		bypassConfirmation = osUtils.GetEnvDefaultValueAsBool(bypassConfirmationEnvVar, false)
	} else {
		// STAGING/PRODUCTION MODE: Force safe behavior (CANNOT be overridden)
		deleteAndCreateDb = false  // HARD BLOCK: Never drop databases in staging/production
		bypassConfirmation = false // HARD BLOCK: Always require confirmation in staging/production

		fmt.Println()
		fmt.Println("╔════════════════════════════════════════════════════════════════╗")
		fmt.Println("║  STAGING/PRODUCTION MODE DETECTED (IS_DEV=false)              ║")
		fmt.Println("║  - Database DROP is BLOCKED (cannot be overridden)            ║")
		fmt.Println("║  - Confirmation is REQUIRED (cannot be overridden)            ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════╝")
		fmt.Println()
	}

	// Call project-specific configuration
	if config.OnDefineConfiguration != nil {
		err := config.OnDefineConfiguration()
		if err != nil {
			return err
		}
	}

	configStorage := *configuration.Manager.Configurations["storage"].Data

	// Disable auto-connect for all project databases
	for _, dbConfig := range config.Databases {
		if storageConfig, ok := configStorage[dbConfig.NameId].(utils.JSON); ok {
			storageConfig["is_connect_at_start"] = false
			storageConfig["must_connected"] = false
		}
	}

	// Disable auto-connect for task-dispatcher-report if exists
	if configStorageDbTaskDispatcherReport, ok := configStorage["task-dispatcher-report"].(utils.JSON); ok {
		configStorageDbTaskDispatcherReport["is_connect_at_start"] = false
		configStorageDbTaskDispatcherReport["must_connected"] = false
	}

	// Setup admin database configuration if deleteAndCreateDb is enabled
	if deleteAndCreateDb && len(config.Databases) > 0 {
		// Detect database type from the first database configuration
		firstDBConfig := configStorage[config.Databases[0].NameId].(utils.JSON)
		configDatabaseTypeStr := firstDBConfig["database_type"].(string)
		configDatabaseType := base.StringToDXDatabaseType(configDatabaseTypeStr)

		if configDatabaseType == base.UnknownDatabaseType {
			PrintErrorBanner(
				"❌ ERROR:",
				"Unknown Database Type",
				fmt.Sprintf("Database type '%s' is not recognized", configDatabaseTypeStr),
				"",
				"Check database type configuration",
			)
			return fmt.Errorf("unknown database type: %s", configDatabaseTypeStr)
		}

		adminDBName, err := GetAdminDatabaseName(configDatabaseType)
		if err != nil {
			PrintErrorBanner(
				"❌ ERROR:",
				"Unsupported Database Type",
				fmt.Sprintf("Error: %s", err.Error()),
				"",
				"This tool supports: PostgreSQL, MariaDB/MySQL, SQL Server, Oracle",
			)
			return err
		}

		envPrefix := GetAdminDatabaseEnvPrefix(configDatabaseType)
		adminDBNameId := strings.ToLower(adminDBName)

		// Create admin database configuration dynamically based on detected database type
		configuration.Manager.NewIfNotExistConfiguration("storage", "storage.json", "json", false, false, map[string]any{
			adminDBNameId: map[string]any{
				"nameid":              adminDBNameId,
				"database_type":       configDatabaseTypeStr,
				"address":             app.App.InitVault.GetStringOrDefault(envPrefix+"_ADDRESS", ""),
				"user_name":           app.App.InitVault.GetStringOrDefault(envPrefix+"_USER_NAME", ""),
				"user_password":       app.App.InitVault.GetStringOrDefault(envPrefix+"_USER_PASSWORD", ""),
				"database_name":       adminDBName,
				"connection_options":  app.App.InitVault.GetStringOrDefault(envPrefix+"_CONNECTION_OPTIONS", "sslmode=disable"),
				"must_connected":      true,
				"is_connect_at_start": true,
			}}, []string{adminDBNameId + ".user_name", adminDBNameId + ".user_password"})

		fmt.Printf("Detected database type: %s (admin database: %s)\n", configDatabaseTypeStr, adminDBName)
	}

	// Disable Redis connections if requested
	if config.DisableRedisConnections {
		configRedis := *configuration.Manager.Configurations["redis"].Data
		for k := range configRedis {
			configRedis[k].(utils.JSON)["must_connected"] = false
			configRedis[k].(utils.JSON)["is_connect_at_start"] = false
		}
	}

	// Initialize all database models
	for _, dbConfig := range config.Databases {
		err := dbConfig.Model.Init()
		if err != nil {
			PrintErrorBanner(
				"❌ ERROR:",
				fmt.Sprintf("%s Initialization Failed", dbConfig.DisplayName),
				fmt.Sprintf("Error: %s", err.Error()),
				"",
				"Check database configuration and connection settings",
			)
			return err
		}
	}

	return nil
}

// executeReset performs the drop/create/execute phase (extracted from doOnAfterConfigurationStartAll)
func executeReset(config *Config) error {
	// Save and disable database error logging during the DB creation phase
	// because the error_log table doesn't exist yet
	originalOnError := log.OnError
	log.OnError = nil
	defer func() {
		// Restore database error logging after DB is ready
		log.OnError = originalOnError
	}()

	// Prompt for confirmation if not bypassed
	if !bypassConfirmation {
		err := PromptForConfirmation(config.ConfirmationKey1, config.ConfirmationKey2)
		if err != nil {
			return err
		}
	}

	fmt.Println("Executing wipe... START")

	// Double-check: NEVER allow database DROP in staging/production (IS_DEV=false)
	if deleteAndCreateDb && !isDev {
		PrintErrorBanner(
			"⛔ CRITICAL SAFETY VIOLATION:",
			"Database DROP Blocked in Production",
			"Attempted to DROP databases with IS_DEV=false - this is wrong",
			"",
			"This safeguard CANNOT be overridden. Use dev environment.",
		)
		return errors.New("CRITICAL SAFETY VIOLATION: database DROP blocked in production")
	}

	// Drop and create databases if enabled
	if deleteAndCreateDb {
		// Get admin database - the name was determined dynamically during configuration
		var dbAdmin *databases.DXDatabase
		adminDBFound := false
		for _, db := range databases.Manager.Databases {
			// Check if this is the admin database by checking if database_name matches common admin database names
			if db.DatabaseName == "postgres" || db.DatabaseName == "mysql" || db.DatabaseName == "master" || db.DatabaseName == "system" {
				dbAdmin = db
				adminDBFound = true
				break
			}
		}

		if !adminDBFound {
			PrintErrorBanner(
				"❌ ERROR:",
				"Admin Database Configuration Not Found",
				"Could not find admin database configuration (postgres/mysql/master/system)",
				"",
				"Check if deleteAndCreateDb configuration is set correctly",
			)
			return errors.New("admin database configuration not found")
		}

		err := dbAdmin.Connect()
		if err != nil {
			PrintErrorBanner(
				"❌ ERROR:",
				"Admin Database Connection Failed",
				fmt.Sprintf("Database: %s, Error: %s", dbAdmin.DatabaseName, err.Error()),
				"",
				fmt.Sprintf("Check %s_ADDRESS, credentials, and network", GetAdminDatabaseEnvPrefix(dbAdmin.DatabaseType)),
			)
			return err
		}

		// Drop all databases (errors intentionally discarded: DB may not exist on first run)
		for _, dbConfig := range config.Databases {
			db := databases.Manager.Databases[dbConfig.NameId]
			_ = databaseUtils.DropDatabase(dbAdmin.Connection, db.DatabaseName)
		}

		// Create all databases
		for _, dbConfig := range config.Databases {
			db := databases.Manager.Databases[dbConfig.NameId]
			err = databaseUtils.CreateDatabase(dbAdmin.Connection, db.DatabaseName)
			if err != nil {
				PrintErrorBanner(
					"❌ ERROR:",
					fmt.Sprintf("Database Creation Failed: %s", dbConfig.DisplayName),
					fmt.Sprintf("Database: %s, Error: %s", db.DatabaseName, err.Error()),
					"",
					"Check if database already exists or permission issues",
				)
				return err
			}
		}
	}

	// Generate DDL and execute SQL scripts for all databases
	for _, dbConfig := range config.Databases {
		db := databases.Manager.Databases[dbConfig.NameId]

		// Generate DDL
		s, err := dbConfig.CreateDDL(db.DatabaseType)
		if err != nil {
			return err
		}

		// Write DDL to file
		f := config.DDLOutputFolder + "/" + dbConfig.DDLOutputFileName
		err = stdos.WriteFile(f, []byte(s), 0644)
		if err != nil {
			return err
		}

		// Execute SQL scripts from embedded assets
		_, err = db.ExecuteCreateScriptsFromEmbedded(dbConfig.GetSQLContent)
		if err != nil {
			PrintErrorBanner(
				"❌ ERROR:",
				fmt.Sprintf("SQL Schema Creation Failed: %s", dbConfig.DisplayName),
				fmt.Sprintf("Database: %s, Error: %s", db.DatabaseName, err.Error()),
				"",
				"Check if tables already exist or SQL syntax errors",
			)
			return err
		}
	}

	fmt.Println("Executing wipe... DONE")
	return nil
}

// executeSeed performs the seed phase (extracted from doOnExecute)
func executeSeed(config *Config) error {
	if config.OnSeed == nil {
		log.Log.Warn("No seed function provided, skipping seed phase")
		return nil
	}

	log.Log.Warn("Executing seed... START")

	err := config.OnSeed()
	if err != nil {
		PrintErrorBanner(
			"❌ ERROR:",
			"Database Seeding Failed",
			fmt.Sprintf("Error: %s", err.Error()),
			"",
			"Check error details above for specific seed operation",
		)
		return err
	}

	log.Log.Warn("Executing seed... DONE")
	return nil
}
