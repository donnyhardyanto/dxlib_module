# AI Instructions: Creating Database Reset Tool for Your Project

This guide helps AI agents automatically create a database reset tool for any project using the `reset-databases` library.

## Prerequisites Check

Before creating a reset tool, verify:
1. ✅ Project uses `dxlib` framework with `databases` package
2. ✅ Database models implement `.Init() error` method
3. ✅ Embedded SQL assets are available via `GetSQLContent(path string) (string, error)`
4. ✅ Project has infrastructure configuration callbacks

## Step 1: Locate Project Configuration File

Look for `reset-tool-config.json` in the project root or ask user for:
- Project name
- List of databases (nameId, display name, model package path)
- Confirmation keys (or generate random secure ones)
- Seed package path (if exists)
- Infrastructure package path

## Step 2: Read Configuration Schema

The configuration file follows this schema (see `reset-config.schema.json`):

```json
{
  "project_name": "my-project-reset",
  "project_description": "My Project Database Reset Tool",
  "confirmation_key_1": "are you sure?",
  "confirmation_key_2": "absolutely",
  "env_var_prefix": "MY_PROJECT",
  "ddl_output_folder": ".",
  "databases": [
    {
      "name_id": "main",
      "display_name": "Main Database",
      "model_package": "github.com/myorg/myproject/models",
      "model_name": "MainDB",
      "ddl_output_filename": "db_main.sql"
    }
  ],
  "infrastructure_package": "github.com/myorg/myproject/infrastructure",
  "assets_package": "github.com/myorg/myproject/assets",
  "seed_package": "github.com/myorg/myproject/cmd/tool-reset/seed",
  "disable_redis_connections": true
}
```

## Step 3: Generate main.go

Use this template to generate `cmd/tool-{project-name}-reset/main.go`:

```go
package main

import (
    resetdb "github.com/donnyhardyanto/dxlib_module/tools/reset-databases"
    "{{.AssetsPackage}}"
    "{{.InfrastructurePackage}}"
    "{{.ModelPackage}}"
    {{if .SeedPackage}}"{{.SeedPackage}}"{{end}}
)

func main() {
    config := &resetdb.Config{
        ProjectName:        "{{.ProjectName}}",
        ProjectDescription: "{{.ProjectDescription}}",
        ConfirmationKey1:   "{{.ConfirmationKey1}}",
        ConfirmationKey2:   "{{.ConfirmationKey2}}",
        EnvVarPrefix:       "{{.EnvVarPrefix}}",
        DDLOutputFolder:    "{{.DDLOutputFolder}}",

        Databases: []resetdb.DatabaseConfig{
            {{range .Databases}}
            {
                NameId:            "{{.NameId}}",
                DisplayName:       "{{.DisplayName}}",
                Model:             {{.ModelPackageName}}.{{.ModelName}},
                CreateDDL:         {{.ModelPackageName}}.{{.ModelName}}.CreateDDL,
                GetSQLContent:     {{.AssetsPackageName}}.GetSQLContent,
                DDLOutputFileName: "{{.DDLOutputFileName}}",
            },
            {{end}}
        },

        OnDefineConfiguration: {{.InfrastructurePackageName}}.DefineConfiguration,
        OnDefineSetVariables:  {{.InfrastructurePackageName}}.DoOnDefineSetVariables,
        {{if .SeedPackage}}OnSeed: {{.SeedPackageName}}.Seed,{{else}}OnSeed: nil,{{end}}

        DisableRedisConnections: {{.DisableRedisConnections}},
    }

    resetdb.Run(config)
}
```

## Step 4: Generate Documentation Files

Generate README.md and HOW_TO_USE.md from templates to provide comprehensive project documentation.

### README.md Generation

Read the template from `templates/README.template.md` and replace all placeholders:

**Template Variables:**
- `{{.ProjectName}}` - From config: `project_name`
- `{{.ProjectDescription}}` - From config: `project_description`
- `{{.EnvVarPrefix}}` - From config: `env_var_prefix`
- `{{.ConfirmationKey1}}` - From config: `confirmation_key_1`
- `{{.ConfirmationKey2}}` - From config: `confirmation_key_2`
- `{{range .Databases}}...{{end}}` - Loop through databases array
  - `{{.NameId}}` - Database `name_id`
  - `{{.DisplayName}}` - Database `display_name`
  - `{{.ModelName}}` - Database `model_name`
  - `{{.DDLOutputFileName}}` - Database `ddl_output_filename`

**Output:** Write to `cmd/tool-{project-name}-reset/README.md`

### HOW_TO_USE.md Generation

Read the template from `templates/HOW_TO_USE.template.md` and replace the same placeholders.

**Output:** Write to `cmd/tool-{project-name}-reset/HOW_TO_USE.md`

### Template Processing Example

If config has:
```json
{
  "project_name": "pgn-partner-reset",
  "env_var_prefix": "PGN_PARTNER",
  "databases": [
    {"name_id": "config", "display_name": "Config Database", ...}
  ]
}
```

Template with:
```markdown
# {{.ProjectName}}
Environment: {{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION

{{range .Databases}}
- {{.DisplayName}} ({{.NameId}})
{{end}}
```

Generates:
```markdown
# pgn-partner-reset
Environment: PGN_PARTNER_RESET_BYPASS_CONFIRMATION

- Config Database (config)
```

## Step 5: Create Seed Package (If Needed)

If `seed_package` is specified but doesn't exist, create:

```go
package seed

func Seed() error {
    // TODO: Implement seed data insertion
    // Example:
    // - Insert default roles
    // - Insert default users
    // - Insert default configuration
    return nil
}
```

## Step 6: Verify Generated Code

Check:
1. ✅ All imports resolve correctly
2. ✅ Model names match actual struct names
3. ✅ Package paths are correct
4. ✅ `go build` succeeds without errors
5. ✅ README.md generated with all placeholders replaced
6. ✅ HOW_TO_USE.md generated with all placeholders replaced
7. ✅ Documentation matches project configuration

## Step 7: Test the Tool

Run basic tests:
```bash
# Test help/version
./tool-{project-name}-reset --help

# Test in development mode
IS_LOCAL=true ./tool-{project-name}-reset

# Test production safeguard
IS_LOCAL=false IS_{ENV_VAR_PREFIX}_RESET_DELETE_AND_CREATE_DB=true ./tool-{project-name}-reset
# Expected: Safety violation error
```

## Common Issues & Solutions

### Issue: Model doesn't implement Init()
**Solution:** Ensure database model has:
```go
func (m *MyDatabaseModel) Init() error {
    // initialization logic
    return nil
}
```

### Issue: GetSQLContent not found
**Solution:** Ensure assets package has embedded SQL with:
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

### Issue: Wrong package names
**Solution:** Use `go list -f '{{.ImportPath}}'` to get correct import paths

## Example Interaction with User

```
AI: I found your project uses 2 databases: "users" and "transactions".
    Should I create a reset tool for both?

User: Yes

AI: What confirmation keys should I use? (Press Enter for random secure keys)

User: [Enter]

AI: Generated confirmation keys:
    Key 1: "confirm-db-wipe-7x9k"
    Key 2: "proceed-reset-4m2n"

    Creating tool at: cmd/tool-myproject-reset/
    [✓] Generated main.go
    [✓] Generated README.md
    [✓] Generated HOW_TO_USE.md
    [✓] Created seed package stub
    [✓] Build successful

    Your reset tool is ready! Documentation:
    - README.md: Quick start and overview
    - HOW_TO_USE.md: Complete usage guide

    Run with:
    IS_LOCAL=true ./tool-myproject-reset
```

## Template Variables Reference

| Variable | Description | Example |
|----------|-------------|---------|
| `{{.ProjectName}}` | Tool binary name | `"my-project-reset"` |
| `{{.ProjectDescription}}` | Tool description | `"My Project Reset CLI"` |
| `{{.ConfirmationKey1}}` | First confirmation prompt | `"are you sure?"` |
| `{{.ConfirmationKey2}}` | Second confirmation prompt | `"absolutely"` |
| `{{.EnvVarPrefix}}` | Prefix for env vars | `"MY_PROJECT"` |
| `{{.DDLOutputFolder}}` | Where to write DDL files | `"."` or `"./build"` |
| `{{.ModelPackage}}` | Import path for models | `"github.com/org/proj/models"` |
| `{{.InfrastructurePackage}}` | Import path for infrastructure | `"github.com/org/proj/infrastructure"` |
| `{{.AssetsPackage}}` | Import path for embedded assets | `"github.com/org/proj/assets"` |
| `{{.SeedPackage}}` | Import path for seed package | `"github.com/org/proj/cmd/tool-reset/seed"` |
| `{{.DisableRedisConnections}}` | Disable Redis during reset | `true` or `false` |
