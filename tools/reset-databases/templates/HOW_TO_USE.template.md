# How to Use {{.ProjectName}}

Complete guide for using the {{.ProjectDescription}}.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Installation](#installation)
3. [Understanding Modes](#understanding-modes)
4. [Common Tasks](#common-tasks)
5. [Safety Features](#safety-features)
6. [Troubleshooting](#troubleshooting)
7. [Advanced Usage](#advanced-usage)

## Prerequisites

Before running this tool, ensure you have:

- [ ] Go 1.21 or higher installed
- [ ] Access to Vault server (or vault secrets available)
- [ ] Database admin credentials configured in Vault
- [ ] Understanding of what this tool does (it can DROP databases!)

### Vault Configuration Required

The tool needs these vault secrets:

```bash
# Vault connection
VAULT_ADDRESS=http://127.0.0.1:8200/
VAULT_TOKEN=your-vault-token
VAULT_PATH=kv/data/your-project

# Database admin credentials (in vault)
DB_POSTGRES_ADDRESS=localhost:5432       # Or DB_MYSQL_*, etc.
DB_POSTGRES_USER_NAME=postgres           # Admin user
DB_POSTGRES_USER_PASSWORD=your-password  # Admin password
```

## Installation

### Option 1: Build from Source

```bash
# Clone repository
git clone <repository-url>
cd <project-root>

# Build the tool
make app={{.ProjectName}} build-tool

# Tool will be at: build/local/app/{{.ProjectName}}
```

### Option 2: Direct Build

```bash
cd src/cmd/{{.ProjectName}}
go build -o {{.ProjectName}}
```

## Understanding Modes

This tool operates in two distinct modes based on the `IS_LOCAL` environment variable.

### Development Mode (IS_LOCAL=true)

**When to use:** Local development, testing, CI/CD pipelines

**Features:**
- ✅ Can DROP and recreate databases
- ✅ Can bypass confirmation prompts
- ✅ Fast iteration for development

**Example:**
```bash
IS_LOCAL=true ./{{.ProjectName}}
```

### Production Mode (IS_LOCAL=false)

**When to use:** Staging, production, any shared environment

**Features:**
- ❌ Cannot DROP databases (hard blocked)
- ❌ Cannot bypass confirmation (hard blocked)
- ✅ Safe for shared environments
- ✅ Manual confirmation required

**Example:**
```bash
IS_LOCAL=false ./{{.ProjectName}}
```

## Common Tasks

### Task 1: Fresh Development Setup (Most Common)

**Goal:** Wipe everything and start fresh

**Command:**
```bash
IS_LOCAL=true \
{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 \
./{{.ProjectName}}
```

**What happens:**
1. Drops all databases ({{range $index, $db := .Databases}}{{if $index}}, {{end}}{{$db.NameId}}{{end}})
2. Creates fresh databases
3. Runs all DDL scripts
4. Seeds initial data
5. Completes in ~30 seconds

**When to use:**
- Starting new feature development
- Switching branches with schema changes
- Cleaning up test data
- Daily development workflow

---

### Task 2: Schema Update Without Data Loss

**Goal:** Run DDL scripts without dropping databases

**Command:**
```bash
IS_LOCAL=true \
IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=false \
{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 \
./{{.ProjectName}}
```

**What happens:**
1. Skips database DROP
2. Runs DDL scripts (CREATE TABLE IF NOT EXISTS, etc.)
3. Seeds data (may skip existing records)

**When to use:**
- Adding new tables to existing database
- Testing DDL scripts
- Updating schema without losing data

**Note:** May fail if DDL scripts don't use `IF NOT EXISTS`

---

### Task 3: Production Database Initialization

**Goal:** Initialize fresh production databases safely

**Command:**
```bash
IS_LOCAL=false \
./{{.ProjectName}}
```

**Prompts:**
```
Input confirmation key 1?
> {{.ConfirmationKey1}}

Input the input confirmation key 2 to confirm:
> {{.ConfirmationKey2}}
```

**What happens:**
1. Requires manual confirmation (cannot bypass)
2. Does NOT drop databases (hard blocked)
3. Runs DDL scripts
4. Seeds initial data

**When to use:**
- First-time production setup
- Disaster recovery
- Staging environment setup

---

### Task 4: Seed Data Only (No Schema Changes)

**Goal:** Re-run seed data without touching schema

**Command:**
```bash
# Not directly supported - modify main.go to skip DDL execution
# Or manually run seed functions from code
```

**Alternative:** Use database migration tools for this use case

---

### Task 5: Generate DDL Files Only

**Goal:** Export DDL for documentation without running reset

**Command:**
```bash
IS_LOCAL=true \
IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=false \
{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 \
./{{.ProjectName}}
```

**Output files:**
{{range .Databases}}
- `{{.DDLOutputFileName}}`
{{end}}

**When to use:**
- Documentation
- Schema review
- Version control commits

## Safety Features

### 1. IS_LOCAL Protection

```bash
# This is SAFE ✅
IS_LOCAL=true ./{{.ProjectName}}

# This is BLOCKED ❌
IS_LOCAL=false IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=true ./{{.ProjectName}}
# Error: CRITICAL SAFETY VIOLATION
```

**Why:** Prevents accidental production data loss

### 2. Confirmation Keys

Two separate confirmation keys required:
1. `{{.ConfirmationKey1}}`
2. `{{.ConfirmationKey2}}`

**Why:** Prevents accidental execution

### 3. Error Banners

All errors display clear, actionable banners:
```
╔══════════════════════════════════════════════════════════════════╗
║  ❌ ERROR: Database Creation Failed
╠══════════════════════════════════════════════════════════════════╣
║  What happened: Connection refused
║  Expected: Database server should be running
║  Action: Start PostgreSQL server
╚══════════════════════════════════════════════════════════════════╝
```

## Troubleshooting

### Error: "Admin Database Configuration Not Found"

**Symptoms:**
```
❌ ERROR: Admin Database Configuration Not Found
Could not find admin database configuration (postgres/mysql/master/system)
```

**Cause:** Vault configuration not loaded

**Solution:**
```bash
# Check vault connectivity
curl $VAULT_ADDRESS/v1/sys/health

# Verify vault token
vault login $VAULT_TOKEN

# Check secrets exist
vault kv get $VAULT_PATH

# Run with explicit vault vars
VAULT_ADDRESS=http://127.0.0.1:8200/ \
VAULT_TOKEN=your-token \
VAULT_PATH=kv/data/your-path \
./{{.ProjectName}}
```

---

### Error: "Database Creation Failed"

**Symptoms:**
```
❌ ERROR: Database Creation Failed: Config Database
Database: pgn_partner_config, Error: permission denied
```

**Cause:** Admin user lacks CREATE DATABASE permission

**Solution:**
```sql
-- Connect to database as superuser
psql -U postgres

-- Grant permission
ALTER USER your_admin_user CREATEDB;

-- Or use superuser credentials in vault
```

---

### Error: "Confirmation Key Wrong"

**Symptoms:**
```
❌ ERROR: Confirmation Key 1 Wrong
You entered: 'yes' - this is wrong
```

**Cause:** Incorrect confirmation key

**Solution:** Enter exact keys:
1. `{{.ConfirmationKey1}}`
2. `{{.ConfirmationKey2}}`

Or bypass in development:
```bash
{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 ./{{.ProjectName}}
```

---

### Error: "CRITICAL SAFETY VIOLATION"

**Symptoms:**
```
⛔ CRITICAL SAFETY VIOLATION: Database DROP Blocked in Production
Attempted to DROP databases with IS_LOCAL=false
```

**Cause:** Attempted to DROP databases in production mode

**Solution:** This is intentional. Options:
1. Use `IS_LOCAL=true` for development
2. Remove `IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=true` override
3. Accept that DROP is blocked in production

---

### Error: "SQL Schema Creation Failed"

**Symptoms:**
```
❌ ERROR: SQL Schema Creation Failed: Task Dispatcher Database
Error: table "users" already exists
```

**Cause:** DDL scripts don't use `IF NOT EXISTS`

**Solution:**
```bash
# Option 1: Drop databases first (development only)
IS_LOCAL=true IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB=true ./{{.ProjectName}}

# Option 2: Manually drop conflicting tables
psql -U postgres -d your_database -c "DROP TABLE IF EXISTS users CASCADE;"

# Option 3: Update DDL scripts to use IF NOT EXISTS
```

## Advanced Usage

### Running in CI/CD Pipeline

```bash
#!/bin/bash
# ci-reset-database.sh

set -e  # Exit on error

# Load secrets from CI environment
export VAULT_ADDRESS=$CI_VAULT_ADDRESS
export VAULT_TOKEN=$CI_VAULT_TOKEN
export VAULT_PATH=$CI_VAULT_PATH

# Run reset with bypass (CI is considered local)
IS_LOCAL=true \
{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1 \
./{{.ProjectName}}

echo "✅ Database reset complete"
```

### Docker Integration

```dockerfile
# Dockerfile.reset
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o {{.ProjectName}} ./src/cmd/{{.ProjectName}}

FROM alpine:latest
COPY --from=builder /app/{{.ProjectName}} /usr/local/bin/
ENTRYPOINT ["{{.ProjectName}}"]
```

```bash
# Run in Docker
docker run --rm \
  -e IS_LOCAL=true \
  -e VAULT_ADDRESS=http://vault:8200 \
  -e VAULT_TOKEN=your-token \
  -e VAULT_PATH=kv/data/your-path \
  {{.ProjectName}}:latest
```

### Kubernetes Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{.ProjectName}}
spec:
  template:
    spec:
      containers:
      - name: reset
        image: {{.ProjectName}}:latest
        env:
        - name: IS_LOCAL
          value: "false"  # Production mode
        - name: VAULT_ADDRESS
          valueFrom:
            secretKeyRef:
              name: vault-config
              key: address
        - name: VAULT_TOKEN
          valueFrom:
            secretKeyRef:
              name: vault-config
              key: token
      restartPolicy: Never
```

### Environment-Specific Configuration

```bash
# Development
export IS_LOCAL=true
export {{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION=1

# Staging
export IS_LOCAL=false
# No bypass - requires manual confirmation

# Production
export IS_LOCAL=false
# No bypass - requires manual confirmation
# DROP is hard blocked
```

## Environment Variables Reference

### Core Variables

| Variable | Type | Dev Default | Prod Default | Description |
|----------|------|-------------|--------------|-------------|
| `IS_LOCAL` | boolean | `false` | `false` | Enable development mode |
| `IS_{{.EnvVarPrefix}}_RESET_DELETE_AND_CREATE_DB` | boolean | `true` | `false` (blocked) | Drop and recreate databases |
| `{{.EnvVarPrefix}}_RESET_BYPASS_CONFIRMATION` | int | `1` | `0` (blocked) | Skip confirmation prompts |

### Vault Variables

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `VAULT_ADDRESS` | string | `http://127.0.0.1:8200/` | Vault server address |
| `VAULT_TOKEN` | string | `dev-vault-token` | Vault authentication token |
| `VAULT_PATH` | string | - | Vault secret path |
| `ENCRYPTION_VAULT_ADDRESS` | string | (fallback to VAULT_ADDRESS) | Encryption vault address |
| `ENCRYPTION_VAULT_TOKEN` | string | (fallback to VAULT_TOKEN) | Encryption vault token |
| `ENCRYPTION_VAULT_PATH` | string | `db_field_vault/data/db_field` | Encryption vault path |

### Database Variables (in Vault)

| Variable | Description |
|----------|-------------|
| `DB_POSTGRES_ADDRESS` | PostgreSQL admin server address |
| `DB_POSTGRES_USER_NAME` | PostgreSQL admin username |
| `DB_POSTGRES_USER_PASSWORD` | PostgreSQL admin password |
| `DB_MYSQL_ADDRESS` | MySQL admin server address |
| `DB_MYSQL_USER_NAME` | MySQL admin username |
| `DB_MYSQL_USER_PASSWORD` | MySQL admin password |

## Best Practices

### ✅ Do

- Run with `IS_LOCAL=true` in development
- Use confirmation bypass in CI/CD pipelines
- Keep vault credentials secure
- Review DDL output files in version control
- Test schema changes in development first
- Use production mode for shared environments

### ❌ Don't

- Never run with `IS_LOCAL=true` in production
- Never commit vault tokens to version control
- Never bypass confirmations in production
- Never run without understanding what it does
- Never ignore safety violation errors
- Never share confirmation keys publicly

## Getting Help

If you encounter issues:

1. Check [Troubleshooting](#troubleshooting) section
2. Review error banner for specific action
3. Check vault connectivity and credentials
4. Verify database admin permissions
5. Consult [README.md](./README.md) for configuration

## Related Documentation

- [Project README](./README.md) - Project overview and quick start
- [Generic Reset Tool](../../dxlib_module/tools/reset-databases/README.md) - Framework documentation
- [AI Instructions](../../dxlib_module/tools/reset-databases/AI_INSTRUCTIONS.md) - Code generation guide

## License

Same as parent project.
