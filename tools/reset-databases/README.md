# Database Reset Tool Library

A reusable library for creating database reset tools in dxlib-based projects.

## For Humans: Quick Start

### Create a Reset Tool for Your Project

1. **Create main.go:**
```go
package main

import (
    resetdb "github.com/donnyhardyanto/dxlib_module/tools/reset-databases"
    // Import your project packages
)

func main() {
    config := &resetdb.Config{
        ProjectName:        "my-project-reset",
        ProjectDescription: "My Project Reset Tool",
        ConfirmationKey1:   "your-key-1",
        ConfirmationKey2:   "your-key-2",
        EnvVarPrefix:       "MY_PROJECT",

        Databases: []resetdb.DatabaseConfig{
            {
                NameId:      "main",
                DisplayName: "Main Database",
                Model:       models.MainDB,
                CreateDDL:   models.MainDB.CreateDDL,
                GetSQLContent: assets.GetSQLContent,
                DDLOutputFileName: "db_main.sql",
            },
        },

        OnDefineConfiguration: infrastructure.DefineConfiguration,
        OnDefineSetVariables:  infrastructure.DoOnDefineSetVariables,
        OnSeed:                seed.Seed,
    }

    resetdb.Run(config)
}
```

2. **Build and run:**
```bash
go build -o my-project-reset
IS_LOCAL=true ./my-project-reset
```

## For AI Agents: Automated Generation

See **AI_INSTRUCTIONS.md** for complete instructions on:
- Reading project configuration from `reset-tool-config.json`
- Validating configuration against `reset-config.schema.json`
- Generating reset tool code automatically
- Common issues and solutions

## Features

- ✅ **Multi-database support**: Reset multiple databases in one operation
- ✅ **All database types**: PostgreSQL, MySQL/MariaDB, SQL Server, Oracle
- ✅ **Safety checks**: Production safeguards prevent accidental data loss
- ✅ **Confirmation prompts**: Two-key confirmation before destructive operations
- ✅ **Error reporting**: Clear, actionable error messages with visual banners
- ✅ **Flexible seeding**: Custom seed functions for initial data
- ✅ **DDL export**: Automatically exports DDL for documentation

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `IS_LOCAL` | Enable development mode (allows DROP) | `false` |
| `IS_{PREFIX}_RESET_DELETE_AND_CREATE_DB` | Drop and recreate databases | `true` (dev), `false` (prod) |
| `{PREFIX}_RESET_BYPASS_CONFIRMATION` | Skip confirmation prompts | `1` (dev), `0` (prod) |
| `DB_{TYPE}_ADDRESS` | Admin database connection address | (from vault) |
| `DB_{TYPE}_USER_NAME` | Admin database username | (from vault) |
| `DB_{TYPE}_USER_PASSWORD` | Admin database password | (from vault) |

Where `{PREFIX}` is your `EnvVarPrefix` and `{TYPE}` is the database type (POSTGRES, MYSQL, SQLSERVER, ORACLE).

## Safety Features

### Production Protection
- When `IS_LOCAL=false`:
  - Database DROP is **BLOCKED** (cannot be overridden)
  - Confirmation prompts are **REQUIRED** (cannot be bypassed)
  - Attempts to override trigger critical safety violation errors

### Development Flexibility
- When `IS_LOCAL=true`:
  - Database DROP is allowed (default: enabled)
  - Confirmation can be bypassed (default: enabled)
  - Full control over reset behavior

## Configuration Types

### Config
Main configuration for the reset tool.

### DatabaseConfig
Configuration for a single database.

### DatabaseModel Interface
```go
type DatabaseModel interface {
    Init() error
}
```

Your database models must implement this interface.

## License

Same as dxlib framework.
