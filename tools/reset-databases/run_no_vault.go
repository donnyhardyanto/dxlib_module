package resetdatabases

import (
	"fmt"
	stdos "os"
	"strings"

	"github.com/donnyhardyanto/dxlib/app"
	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/configuration"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	osUtils "github.com/donnyhardyanto/dxlib/utils/os"
)

// RunNoVault executes the database reset tool without HashiCorp Vault.
// Identical to Run() but reads all credentials directly from environment variables.
// Use this for projects that do not use Vault for secret management.
func RunNoVault(config *Config) {
	if config.DDLOutputFolder == "" {
		config.DDLOutputFolder = "."
	}

	log.SetFormatSimple()

	app.Set(config.ProjectName,
		config.ProjectDescription,
		config.ProjectDescription,
		false,
		config.ProjectName+"-debug",
		"abc",
	)

	app.App.OnDefineConfiguration = func() error {
		return defineConfigurationNoVault(config)
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

// defineConfigurationNoVault is identical to defineConfiguration() but uses
// environment variables directly instead of Vault for admin database credentials.
func defineConfigurationNoVault(config *Config) error {
	isDev = osUtils.GetEnvDefaultValueAsBool(config.EnvVarPrefix+"_IS_DEV", false)

	deleteAndCreateDBEnvVar := config.EnvVarPrefix + "_RESET_DELETE_AND_CREATE_DB"
	bypassConfirmationEnvVar := config.EnvVarPrefix + "_RESET_BYPASS_CONFIRMATION"

	if isDev {
		t1 := osUtils.GetEnvDefaultValueAsBool(deleteAndCreateDBEnvVar, false)
		if t1 {
			deleteAndCreateDb = true
		}
		bypassConfirmation = osUtils.GetEnvDefaultValueAsBool(bypassConfirmationEnvVar, false)
	} else {
		deleteAndCreateDb = false
		bypassConfirmation = false

		fmt.Println()
		fmt.Println("╔════════════════════════════════════════════════════════════════╗")
		fmt.Println("║  STAGING/PRODUCTION MODE DETECTED (IS_DEV=false)              ║")
		fmt.Println("║  - Database DROP is BLOCKED (cannot be overridden)            ║")
		fmt.Println("║  - Confirmation is REQUIRED (cannot be overridden)            ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════╝")
		fmt.Println()
	}

	if config.OnDefineConfiguration != nil {
		err := config.OnDefineConfiguration()
		if err != nil {
			return err
		}
	}

	configStorage := *configuration.Manager.Configurations["storage"].Data

	for _, dbConfig := range config.Databases {
		if storageConfig, ok := configStorage[dbConfig.NameId].(utils.JSON); ok {
			storageConfig["is_connect_at_start"] = false
			storageConfig["must_connected"] = false
		}
	}

	if configStorageDbTaskDispatcherReport, ok := configStorage["task-dispatcher-report"].(utils.JSON); ok {
		configStorageDbTaskDispatcherReport["is_connect_at_start"] = false
		configStorageDbTaskDispatcherReport["must_connected"] = false
	}

	if deleteAndCreateDb && len(config.Databases) > 0 {
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

		// Read admin credentials from environment variables directly (no Vault).
		configuration.Manager.NewIfNotExistConfiguration("storage", "storage.json", "json", false, false, map[string]any{
			adminDBNameId: map[string]any{
				"nameid":              adminDBNameId,
				"database_type":       configDatabaseTypeStr,
				"address":             osUtils.GetEnvDefaultValue(envPrefix+"_ADDRESS", ""),
				"user_name":           osUtils.GetEnvDefaultValue(envPrefix+"_USER_NAME", ""),
				"user_password":       osUtils.GetEnvDefaultValue(envPrefix+"_USER_PASSWORD", ""),
				"database_name":       adminDBName,
				"connection_options":  osUtils.GetEnvDefaultValue(envPrefix+"_CONNECTION_OPTIONS", "sslmode=disable"),
				"must_connected":      true,
				"is_connect_at_start": true,
			}}, []string{adminDBNameId + ".user_name", adminDBNameId + ".user_password"})

		fmt.Printf("Detected database type: %s (admin database: %s)\n", configDatabaseTypeStr, adminDBName)
	}

	if config.DisableRedisConnections {
		if redisCfg, ok := configuration.Manager.Configurations["redis"]; ok && redisCfg != nil && redisCfg.Data != nil {
			configRedis := *redisCfg.Data
			for k := range configRedis {
				configRedis[k].(utils.JSON)["must_connected"] = false
				configRedis[k].(utils.JSON)["is_connect_at_start"] = false
			}
		}
	}

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
