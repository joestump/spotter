package database

// Governing: SPEC-0014 REQ "Driver Registration", ADR-0023
import (
	"context"
	"database/sql"
	"fmt"

	"spotter/ent"
	"spotter/internal/crypto"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

const (
	driverPostgres = "postgres"
	driverMySQL    = "mysql"
	driverSQLite   = "sqlite3"
)

func NewClient(driver, source string, encryptor *crypto.Encryptor) (*ent.Client, error) {
	client, err := ent.Open(driver, source)
	if err != nil {
		return nil, fmt.Errorf("failed opening connection to %s: %v", driver, err)
	}

	// Register encryption/decryption hooks if encryptor is provided
	if encryptor != nil {
		RegisterEncryptionHooks(client, encryptor)
	}

	// Governing: SPEC-0014 REQ "Schema Migration", ADR-0004 (Ent ORM handles DDL for all dialects)
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		// Attempt to close the client on schema creation failure
		_ = client.Close()
		return nil, fmt.Errorf("failed creating schema resources: %v", err)
	}

	// Governing: SPEC-0014 REQ "Denormalized Entity Tags Table"
	// Open a raw database connection for the entity_tags custom migration.
	db, err := sql.Open(driverToStdlib(driver), source)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed opening raw db for entity_tags migration: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := CreateEntityTagsTable(ctx, driver, db); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed creating entity_tags table: %v", err)
	}

	return client, nil
}

// driverToStdlib maps Ent dialect names to database/sql driver names.
func driverToStdlib(driver string) string {
	switch driver {
	case driverPostgres:
		return driverPostgres
	case driverMySQL:
		return driverMySQL
	default:
		return driverSQLite
	}
}

// OpenRawDB opens a persistent *sql.DB connection using the same driver/source
// as NewClient. Callers are responsible for closing the returned db.
func OpenRawDB(driver, source string) (*sql.DB, error) {
	db, err := sql.Open(driverToStdlib(driver), source)
	if err != nil {
		return nil, fmt.Errorf("failed opening raw db (%s): %w", driver, err)
	}
	return db, nil
}
