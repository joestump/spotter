// Governing: SPEC-0014 REQ "Denormalized Entity Tags Table", ADR-0023 (multi-database support), ADR-0004 (Ent ORM)
package database

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateEntityTagsTable creates the denormalized entity_tags query table
// if it does not already exist. This table lives outside the Ent schema
// and is maintained via raw SQL for cross-entity filtered tag lookups.
//
// Governing: SPEC-0014 REQ "Denormalized Entity Tags Table" — DDL is
// emitted per-dialect for all three supported drivers (sqlite3, postgres,
// mysql) per ADR-0023.
func CreateEntityTagsTable(ctx context.Context, driver string, db *sql.DB) error {
	// Statements are executed one at a time rather than as a single
	// multi-statement batch: go-sql-driver/mysql rejects multi-statement
	// Exec calls unless the DSN opts into multiStatements=true, which the
	// SPEC-0014 default MySQL DSN does not.
	for _, stmt := range entityTagsDDL(driver) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create entity_tags table: %w", err)
		}
	}
	return nil
}

// entityTagsDDL returns the dialect-specific DDL statements for the
// entity_tags table. Statements are idempotent (IF NOT EXISTS) so the
// migration is safe to run on every startup.
//
// Governing: ADR-0023 — driver values are "postgres", "mysql", and the
// SQLite default ("sqlite3").
func entityTagsDDL(driver string) []string {
	switch driver {
	case driverPostgres:
		return entityTagsPostgres
	case driverMySQL:
		return entityTagsMySQL
	default:
		return entityTagsSQLite
	}
}

var entityTagsPostgres = []string{
	`CREATE TABLE IF NOT EXISTS entity_tags (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tag_id BIGINT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    tag_type VARCHAR(20) NOT NULL,
    tag_name VARCHAR(255) NOT NULL,
    entity_type VARCHAR(20) NOT NULL,
    entity_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT entity_tags_unique UNIQUE (tag_id, entity_type, entity_id)
)`,
	`CREATE INDEX IF NOT EXISTS idx_entity_tags_lookup ON entity_tags (user_id, tag_type, tag_name, entity_type)`,
	`CREATE INDEX IF NOT EXISTS idx_entity_tags_entity ON entity_tags (entity_type, entity_id)`,
}

// entityTagsMySQL is the MariaDB/MySQL dialect DDL. Differences from the
// other dialects (Governing: ADR-0023, SPEC-0014 REQ "Denormalized Entity
// Tags Table"):
//   - BIGINT AUTO_INCREMENT PRIMARY KEY (MySQL has no SERIAL/AUTOINCREMENT
//     keyword in the SQLite/Postgres sense)
//   - Secondary indexes are declared inline in CREATE TABLE because MySQL
//     (unlike MariaDB 10.1+) does not support CREATE INDEX IF NOT EXISTS,
//     and inline KEY clauses keep the statement idempotent on both.
//   - DATETIME DEFAULT CURRENT_TIMESTAMP instead of TIMESTAMPTZ (and avoids
//     the TIMESTAMP 2038 range limit)
//   - ENGINE=InnoDB pinned explicitly: FK + ON DELETE CASCADE semantics
//     require InnoDB.
var entityTagsMySQL = []string{
	`CREATE TABLE IF NOT EXISTS entity_tags (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    tag_id BIGINT NOT NULL,
    tag_type VARCHAR(20) NOT NULL,
    tag_name VARCHAR(255) NOT NULL,
    entity_type VARCHAR(20) NOT NULL,
    entity_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT entity_tags_unique UNIQUE (tag_id, entity_type, entity_id),
    KEY idx_entity_tags_lookup (user_id, tag_type, tag_name, entity_type),
    KEY idx_entity_tags_entity (entity_type, entity_id),
    CONSTRAINT entity_tags_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT entity_tags_tag_fk FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
) ENGINE=InnoDB`,
}

var entityTagsSQLite = []string{
	`CREATE TABLE IF NOT EXISTS entity_tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    tag_type VARCHAR(20) NOT NULL,
    tag_name VARCHAR(255) NOT NULL,
    entity_type VARCHAR(20) NOT NULL,
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tag_id, entity_type, entity_id)
)`,
	`CREATE INDEX IF NOT EXISTS idx_entity_tags_lookup ON entity_tags (user_id, tag_type, tag_name, entity_type)`,
	`CREATE INDEX IF NOT EXISTS idx_entity_tags_entity ON entity_tags (entity_type, entity_id)`,
}
