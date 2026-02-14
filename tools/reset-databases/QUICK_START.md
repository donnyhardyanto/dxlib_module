# Quick Start Guide

## For Project Developers

### Step 1: Create main.go

Create `src/cmd/tool-{your-project}-reset/main.go`:

```go
package main

import (
    resetdb "github.com/donnyhardyanto/dxlib_module/tools/reset-databases"
    "your-project/assets"
    "your-project/infrastructure"
    "your-project/models"
    "your-project/seed"
)

// Wrapper if your DefineConfiguration doesn't return error
func defineConfiguration() error {
    infrastructure.DefineConfiguration()
    return nil
}

func main() {
    config := &resetdb.Config{
        ProjectName:        "your-project-reset",
        ProjectDescription: "Your Project Reset CLI",
        ConfirmationKey1:   "are you sure?",
        ConfirmationKey2:   "yes delete everything",
        EnvVarPrefix:       "YOUR_PROJECT",
        DDLOutputFolder:    ".",

        Databases: []resetdb.DatabaseConfig{
            {
                NameId:            "main",
                DisplayName:       "Main Database",
                Model:             models.MainDB,
                CreateDDL:         models.MainDB.CreateDDL,
                GetSQLContent:     assets.GetSQLContent,
                DDLOutputFileName: "db_main.sql",
            },
        },

        OnDefineConfiguration: defineConfiguration,
        OnDefineSetVariables:  infrastructure.DoOnDefineSetVariables,
        OnSeed:                seed.Seed,

        DisableRedisConnections: true,
    }

    resetdb.Run(config)
}
```

### Step 2: Build

```bash
cd src/cmd/tool-your-project-reset
go build -o tool-your-project-reset
```

### Step 3: Run

**Development mode (with database DROP):**
```bash
IS_LOCAL=true ./tool-your-project-reset
```

**Production mode (without database DROP):**
```bash
IS_LOCAL=false ./tool-your-project-reset
```

**Skip confirmation (development only):**
```bash
IS_LOCAL=true YOUR_PROJECT_RESET_BYPASS_CONFIRMATION=1 ./tool-your-project-reset
```

**Disable database DROP (even in development):**
```bash
IS_LOCAL=true IS_YOUR_PROJECT_RESET_DELETE_AND_CREATE_DB=false ./tool-your-project-reset
```

## Environment Variables

| Variable | Description | Dev Default | Prod Default |
|----------|-------------|-------------|--------------|
| `IS_LOCAL` | Development mode toggle | `false` | `false` |
| `IS_{PREFIX}_RESET_DELETE_AND_CREATE_DB` | Drop and recreate databases | `true` | `false` (blocked) |
| `{PREFIX}_RESET_BYPASS_CONFIRMATION` | Skip confirmation prompts | `1` | `0` (blocked) |

Replace `{PREFIX}` with your `EnvVarPrefix`.

## Safety Features

### Production Protection (IS_LOCAL=false)
- ❌ Database DROP is **HARD BLOCKED** (cannot be overridden)
- ❌ Confirmation bypass is **HARD BLOCKED** (cannot be overridden)
- ✅ Attempts to override trigger safety violation errors

### Development Flexibility (IS_LOCAL=true)
- ✅ Database DROP allowed (default: enabled, can be disabled)
- ✅ Confirmation can be bypassed (default: enabled, can be disabled)
- ✅ Full control over reset behavior

## Requirements

Your project must have:

1. **Database models** implementing:
```go
type YourDB struct { ... }
func (db *YourDB) Init() error { ... }
func (db *YourDB) CreateDDL(dbType base.DXDatabaseType) (string, error) { ... }
```

2. **Assets package** with embedded SQL:
```go
//go:embed sql/**/*.sql
var sqlFS embed.FS

func GetSQLContent(path string) (string, error) {
    content, err := sqlFS.ReadFile(path)
    if err != nil {
        return "", err
    }
    return string(content), nil
}
```

3. **Infrastructure package** with configuration callbacks:
```go
func DefineConfiguration() { ... }
func DoOnDefineSetVariables() error { ... }
```

4. **Seed package** (optional):
```go
func Seed() error { ... }
```

## Common Issues

### "cannot use ... as func() error"
**Cause:** Your callback doesn't return an error

**Fix:** Create a wrapper:
```go
func defineConfiguration() error {
    infrastructure.DefineConfiguration()
    return nil
}
```

### "Admin Database Configuration Not Found"
**Cause:** Database configuration not loaded

**Fix:** Ensure your `DefineConfiguration()` loads database configs from vault/env

### "Database Creation Failed"
**Cause:** Admin database credentials incorrect or insufficient permissions

**Fix:** Check `DB_{TYPE}_ADDRESS`, `DB_{TYPE}_USER_NAME`, `DB_{TYPE}_USER_PASSWORD` environment variables or vault configuration

## Example: PGN Partner

See `src/cmd/tool-pgn-partner-reset/main.go` for a complete working example with:
- 3 databases (config, auditlog, task-dispatcher)
- Custom confirmation keys
- Seed function
- Infrastructure callbacks

## Support

- Documentation: See README.md
- AI Instructions: See AI_INSTRUCTIONS.md
- Schema: See reset-config.schema.json
