# {{.ProjectName}}

{{.ProjectDescription}}

## Overview

This tool is a database reset and initialization utility built using the [dxlib reset-databases](../../dxlib_module/tools/reset-databases) framework. It handles:

- Database drop and recreation (development mode)
- Schema initialization from DDL
- Data seeding
- Safety controls for production environments

## Databases Managed

This tool manages the following databases:

{{range .Databases}}
- **{{.DisplayName}}** (`{{.NameId}}`)
  - Model: `{{.ModelName}}`
  - DDL Output: `{{.DDLOutputFileName}}`
{{end}}

## Safety Features

### Production Protection (IS_LOCAL=false)
- ❌ Database DROP is **HARD BLOCKED** (cannot be overridden)
- ❌ Confirmation bypass is **HARD BLOCKED** (cannot be overridden)
- ✅ Attempts to override trigger critical safety violation errors
- ✅ Manual confirmation required for all operations

### Development Mode (IS_LOCAL=true)
- ✅ Database DROP allowed (default: enabled, can be disabled)
- ✅ Confirmation can be bypassed (default: enabled)
- ✅ Full control over reset behavior via environment variables

## Quick Start

### Development Mode (Local)
```bash
# Build the tool
make app={{.ProjectName}} build-tool

# Run with database DROP and auto-confirmation
IS_LOCAL=true {{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 ./{{.ProjectName}}

# Run with confirmation prompts
IS_LOCAL=true {{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=0 ./{{.ProjectName}}
```

### Production/Staging Mode
```bash
# Production mode (no DROP, requires confirmation)
IS_LOCAL=false ./{{.ProjectName}}

# This will FAIL with safety violation:
IS_LOCAL=false IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=true ./{{.ProjectName}}
```

## Environment Variables

| Variable | Description | Dev Default | Prod Default |
|----------|-------------|-------------|--------------|
| `IS_LOCAL` | Enable development mode | `false` | `false` |
| `IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB` | Drop and recreate databases | `true` | `false` (blocked) |
| `{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION` | Skip confirmation prompts | `1` | `0` (blocked) |
| `VAULT_ADDRESS` | Vault server address | `http://127.0.0.1:8200/` | (required) |
| `VAULT_TOKEN` | Vault authentication token | `dev-vault-token` | (required) |
| `VAULT_PATH` | Vault secret path | `kv/data/...` | (required) |

## Confirmation Keys

When `{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=0`, you will be prompted for two confirmation keys:

1. **Key 1:** `{{.ConfirmationKey1}}`
2. **Key 2:** `{{.ConfirmationKey2}}`

These keys prevent accidental database wipes.

## Build

```bash
# Build for current platform
make app={{.ProjectName}} build-tool

# Build for all platforms
make app={{.ProjectName}} build-tool-all

# Manual build
cd src/cmd/{{.ProjectName}}
go build -o {{.ProjectName}}
```

## Usage Scenarios

### Scenario 1: Fresh Development Setup
```bash
IS_LOCAL=true {{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 ./{{.ProjectName}}
```
- Drops all databases
- Creates fresh databases
- Runs all DDL scripts
- Seeds initial data

### Scenario 2: Schema Update (No DROP)
```bash
IS_LOCAL=true IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=false ./{{.ProjectName}}
```
- Skips database DROP
- Runs DDL scripts (may fail if tables exist)
- Seeds data

### Scenario 3: Production Schema Initialization
```bash
IS_LOCAL=false ./{{.ProjectName}}
# Enter confirmation keys when prompted
```
- No database DROP (hard blocked)
- Runs DDL scripts on existing databases
- Seeds data
- Requires manual confirmation

## Output Files

The tool generates DDL files for documentation:

{{range .Databases}}
- `{{.DDLOutputFileName}}` - DDL for {{.DisplayName}}
{{end}}

## Configuration

This tool uses the generic reset-databases module. Configuration is in:
- `main.go` - Database list and project settings
- `reset-tool-config.json` - Configuration for AI code generation
- `seed/` - Seed data functions

## Seed Data

The seed package initializes:
- Default roles and privileges
- Initial users and organizations
- Master data and lookup tables
- Default configuration values

See `seed/seed.go` for details.

## Troubleshooting

### "Admin Database Configuration Not Found"
**Cause:** Database configuration not loaded from vault

**Fix:** Check vault connection:
```bash
VAULT_ADDRESS=... VAULT_TOKEN=... VAULT_PATH=... ./{{.ProjectName}}
```

### "Database Creation Failed"
**Cause:** Admin database credentials incorrect

**Fix:** Verify vault secrets:
- `DB_POSTGRES_ADDRESS` (or DB_MYSQL_*, DB_SQLSERVER_*, DB_ORACLE_*)
- `DB_POSTGRES_USER_NAME`
- `DB_POSTGRES_USER_PASSWORD`

### "Confirmation Key Wrong"
**Cause:** Incorrect confirmation key entered

**Fix:** Enter the exact keys:
1. `{{.ConfirmationKey1}}`
2. `{{.ConfirmationKey2}}`

### "CRITICAL SAFETY VIOLATION"
**Cause:** Attempted to DROP databases in production mode

**Fix:** This is intentional. Use `IS_LOCAL=true` for development or remove the `IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB` override.

## Related Documentation

- [Generic Reset Tool Module](../../dxlib_module/tools/reset-databases/README.md)
- [AI Instructions for Code Generation](../../dxlib_module/tools/reset-databases/AI_INSTRUCTIONS.md)
- [How to Use This Tool](./HOW_TO_USE.md)

## License

Same as parent project.
