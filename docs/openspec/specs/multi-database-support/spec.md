---
id: SPEC-0014
status: accepted
date: 2026-02-22
authors: joestump
governing-adrs:
  - ADR-0023 (multi-database support)
  - ADR-0004 (Ent ORM)
  - ADR-0009 (Viper configuration)
---

# SPEC-0014: Multi-Database Support

## Overview

Spotter MUST support three database backends selectable at runtime via environment variables: SQLite (default, zero-infrastructure), PostgreSQL, and MariaDB/MySQL. The operator selects the backend by setting `SPOTTER_DATABASE_DRIVER` and `SPOTTER_DATABASE_SOURCE`. No code changes or recompilation are required to switch backends.

## Requirements

### Requirement: Driver Registration

The application MUST register Go database drivers for all three supported backends at startup. Registration is performed via blank imports in `internal/database/db.go`:

- `_ "github.com/mattn/go-sqlite3"` for SQLite
- `_ "github.com/lib/pq"` for PostgreSQL
- `_ "github.com/go-sql-driver/mysql"` for MariaDB/MySQL

**MUST NOT** attempt to register drivers conditionally at runtime — all three MUST be compiled into the binary.

### Requirement: Driver Validation

`internal/config/config.go` MUST validate `database.driver` at load time. Accepted values are `sqlite3`, `postgres`, and `mysql`. Any other value MUST cause `config.Load()` to return an error with the message: `"unsupported database driver %q: must be one of sqlite3, postgres, mysql"`.

### Requirement: Driver-Specific Default Source

When `SPOTTER_DATABASE_SOURCE` is not set, the application MUST apply a sensible default based on the configured driver:

| Driver | Default Source |
|--------|---------------|
| `sqlite3` | `file:spotter.db?cache=shared&_fk=1` |
| `postgres` | `host=localhost port=5432 dbname=spotter sslmode=disable` |
| `mysql` | `spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4` |

The existing `sqlite3` default MUST be preserved unchanged for backwards compatibility.

### Requirement: Schema Migration

`internal/database/db.go` MUST call `client.Schema.Create(ctx)` for all three drivers. Ent ORM's `Schema.Create()` handles DDL for all supported dialects. No driver-specific migration branching is required in application code.

### Requirement: go.mod Dependencies

`go.mod` MUST declare direct dependencies on:
- `github.com/lib/pq` (PostgreSQL driver)
- `github.com/go-sql-driver/mysql` (MariaDB/MySQL driver)

The existing `github.com/mattn/go-sqlite3` dependency MUST be retained.

### Requirement: Docker Compose Examples

The repository MUST include example `docker-compose` configurations for PostgreSQL and MariaDB deployments:

- `docker-compose.postgres.yml` — Spotter + PostgreSQL service
- `docker-compose.mariadb.yml` — Spotter + MariaDB service

Each MUST include:
- A database service with health check
- A `depends_on` condition so Spotter waits for the database to be healthy
- Environment variables showing the required `SPOTTER_DATABASE_DRIVER` and `SPOTTER_DATABASE_SOURCE` values
- A named volume for database persistence

### Requirement: End-User Documentation

The docs site MUST document multi-database support with:
- A configuration reference table covering all three drivers
- Example `SPOTTER_DATABASE_SOURCE` DSN formats for each driver
- A note that `lib/pq` and `go-sql-driver/mysql` are pure Go (no CGO), while SQLite requires CGO
- Links to the relevant Docker Compose example files

### Requirement: Test Coverage

`internal/config/config_test.go` MUST include tests verifying:
- Valid driver values (`sqlite3`, `postgres`, `mysql`) pass validation
- Invalid driver values return an appropriate error
- Driver-specific default sources are applied when `SPOTTER_DATABASE_SOURCE` is empty

## Key Scenarios

### Scenario: Operator switches from SQLite to PostgreSQL

GIVEN an operator has been running Spotter with `SPOTTER_DATABASE_DRIVER=sqlite3`
WHEN they set `SPOTTER_DATABASE_DRIVER=postgres` and `SPOTTER_DATABASE_SOURCE=postgres://user:pass@localhost/spotter`
THEN Spotter MUST start successfully, run `Schema.Create()` against PostgreSQL, and operate normally
AND no application code changes or recompilation are required

### Scenario: Invalid driver rejected at startup

GIVEN an operator sets `SPOTTER_DATABASE_DRIVER=cockroachdb`
WHEN Spotter starts
THEN `config.Load()` MUST return an error before any database connection is attempted
AND the error message MUST identify the invalid value and list valid options

### Scenario: PostgreSQL default DSN applied

GIVEN an operator sets `SPOTTER_DATABASE_DRIVER=postgres` but does NOT set `SPOTTER_DATABASE_SOURCE`
WHEN Spotter starts
THEN the connection MUST use `host=localhost port=5432 dbname=spotter sslmode=disable` as the DSN

### Scenario: MariaDB health-checked compose startup

GIVEN an operator runs `docker compose -f docker-compose.mariadb.yml up`
WHEN the MariaDB service passes its health check
THEN the Spotter service MUST start and successfully connect to MariaDB

## Out of Scope

- Connection pooling configuration (max connections, idle connections) — deferred to a future spec
- Database migration tooling (e.g., Atlas, Goose) for production schema versioning — `Schema.Create()` is sufficient for now
- Read replicas or multi-master setups
- CockroachDB, TiDB, or other PostgreSQL-compatible databases
