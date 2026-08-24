// Governing: SPEC-0014 REQ "Denormalized Entity Tags Table", REQ "Driver Registration", ADR-0023
package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// TestEntityTagsDDLDialects is the CI-friendly unit test: it asserts the
// generated DDL per dialect without needing a live server.
func TestEntityTagsDDLDialects(t *testing.T) {
	tests := []struct {
		driver      string
		wantTokens  []string
		rejectToken string
	}{
		{
			driver:      "mysql",
			wantTokens:  []string{"BIGINT AUTO_INCREMENT PRIMARY KEY", "ENGINE=InnoDB", "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP", "idx_entity_tags_lookup", "idx_entity_tags_entity", "entity_tags_unique"},
			rejectToken: "AUTOINCREMENT", // SQLite spelling must not leak into MySQL DDL
		},
		{
			driver:      "postgres",
			wantTokens:  []string{"BIGSERIAL PRIMARY KEY", "TIMESTAMPTZ", "idx_entity_tags_lookup", "idx_entity_tags_entity", "entity_tags_unique"},
			rejectToken: "AUTO_INCREMENT",
		},
		{
			driver:      "sqlite3",
			wantTokens:  []string{"INTEGER PRIMARY KEY AUTOINCREMENT", "idx_entity_tags_lookup", "idx_entity_tags_entity"},
			rejectToken: "ENGINE=InnoDB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.driver, func(t *testing.T) {
			ddl := strings.Join(entityTagsDDL(tt.driver), ";\n")
			for _, tok := range tt.wantTokens {
				if !strings.Contains(ddl, tok) {
					t.Errorf("%s DDL missing %q:\n%s", tt.driver, tok, ddl)
				}
			}
			if strings.Contains(ddl, tt.rejectToken) {
				t.Errorf("%s DDL must not contain %q:\n%s", tt.driver, tt.rejectToken, ddl)
			}
		})
	}

	// Unknown drivers fall back to SQLite, matching driverToStdlib.
	if got := strings.Join(entityTagsDDL("bogus"), ";"); !strings.Contains(got, "AUTOINCREMENT") {
		t.Errorf("unknown driver should fall back to SQLite DDL, got:\n%s", got)
	}
}

// TestCreateEntityTagsTable_Matrix runs the real DDL against each driver.
//
// SQLite always runs (in-memory). Postgres and MySQL/MariaDB run only when
// a live server is available, signalled via environment variables:
//
//	SPOTTER_TEST_POSTGRES_DSN — e.g. "host=localhost port=5432 user=spotter password=spotter dbname=spotter sslmode=disable"
//	SPOTTER_TEST_MYSQL_DSN   — e.g. "spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4"
//
// If the variable is unset, the sub-test skips gracefully so CI without
// databases stays green. To run locally with throwaway containers:
//
//	docker run --rm -d --name spotter-pg -e POSTGRES_USER=spotter -e POSTGRES_PASSWORD=spotter -e POSTGRES_DB=spotter -p 5432:5432 postgres:16
//	docker run --rm -d --name spotter-maria -e MARIADB_USER=spotter -e MARIADB_PASSWORD=spotter -e MARIADB_DATABASE=spotter -e MARIADB_ROOT_PASSWORD=root -p 3306:3306 mariadb:11
//	SPOTTER_TEST_POSTGRES_DSN="host=localhost port=5432 user=spotter password=spotter dbname=spotter sslmode=disable" \
//	SPOTTER_TEST_MYSQL_DSN="spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4" \
//	go test ./internal/database/ -run TestCreateEntityTagsTable_Matrix -v
func TestCreateEntityTagsTable_Matrix(t *testing.T) {
	t.Run("sqlite3", func(t *testing.T) {
		db, err := sql.Open("sqlite3", "file:entity_tags_matrix?mode=memory&cache=shared&_fk=1")
		if err != nil {
			t.Fatalf("failed to open sqlite: %v", err)
		}
		defer func() { _ = db.Close() }()
		runEntityTagsMatrix(t, "sqlite3", db,
			`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
			`CREATE TABLE tags (id INTEGER PRIMARY KEY AUTOINCREMENT)`)
		assertSQLiteColumns(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("SPOTTER_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("SPOTTER_TEST_POSTGRES_DSN not set; skipping live PostgreSQL test")
		}
		db := openLiveDB(t, "postgres", dsn)
		dropEntityTagsTables(t, db)
		runEntityTagsMatrix(t, "postgres", db,
			`CREATE TABLE users (id BIGSERIAL PRIMARY KEY)`,
			`CREATE TABLE tags (id BIGSERIAL PRIMARY KEY)`)
		assertInformationSchemaColumns(t, db, "postgres", map[string]string{
			"id":         "bigint",
			"user_id":    "bigint",
			"entity_id":  "bigint",
			"tag_type":   "character varying",
			"created_at": "timestamp with time zone",
		})
	})

	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("SPOTTER_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("SPOTTER_TEST_MYSQL_DSN not set; skipping live MariaDB/MySQL test")
		}
		db := openLiveDB(t, "mysql", dsn)
		dropEntityTagsTables(t, db)
		runEntityTagsMatrix(t, "mysql", db,
			`CREATE TABLE users (id BIGINT AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB`,
			`CREATE TABLE tags (id BIGINT AUTO_INCREMENT PRIMARY KEY) ENGINE=InnoDB`)
		assertInformationSchemaColumns(t, db, "mysql", map[string]string{
			"id":         "bigint",
			"user_id":    "bigint",
			"entity_id":  "bigint",
			"tag_type":   "varchar",
			"created_at": "datetime",
		})
	})
}

// openLiveDB connects and pings; if the server is unreachable the test is
// skipped (not failed) so a stale env var doesn't break local runs.
func openLiveDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("failed to open %s: %v", driver, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("%s server not reachable (%v); skipping", driver, err)
	}
	return db
}

// dropEntityTagsTables resets state on live servers so re-runs are clean.
func dropEntityTagsTables(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS entity_tags`,
		`DROP TABLE IF EXISTS tags`,
		`DROP TABLE IF EXISTS users`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("cleanup %q failed: %v", stmt, err)
		}
	}
}

// runEntityTagsMatrix creates minimal parent tables (entity_tags carries
// FKs to users and tags, which Ent normally creates before this migration
// runs — see NewClient in db.go), runs CreateEntityTagsTable twice to
// prove idempotency, and round-trips an insert/select including the
// unique-constraint check required by SPEC-0014.
func runEntityTagsMatrix(t *testing.T, driver string, db *sql.DB, usersDDL, tagsDDL string) {
	t.Helper()
	ctx := context.Background()

	for _, stmt := range []string{usersDDL, tagsDDL} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("failed to create parent table: %v", err)
		}
	}

	if err := CreateEntityTagsTable(ctx, driver, db); err != nil {
		t.Fatalf("CreateEntityTagsTable(%s) failed: %v", driver, err)
	}
	// Idempotency: the migration runs on every startup.
	if err := CreateEntityTagsTable(ctx, driver, db); err != nil {
		t.Fatalf("CreateEntityTagsTable(%s) second run failed: %v", driver, err)
	}

	// Sanity insert/select round-trip.
	seed := []string{
		`INSERT INTO users (id) VALUES (1)`,
		`INSERT INTO tags (id) VALUES (1)`,
		`INSERT INTO entity_tags (user_id, tag_id, tag_type, tag_name, entity_type, entity_id) VALUES (1, 1, 'genre', 'jazz', 'artist', 42)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q failed: %v", stmt, err)
		}
	}

	var tagName string
	row := db.QueryRowContext(ctx, `SELECT tag_name FROM entity_tags WHERE entity_type = 'artist' AND entity_id = 42`)
	if err := row.Scan(&tagName); err != nil {
		t.Fatalf("round-trip select failed: %v", err)
	}
	if tagName != "jazz" {
		t.Errorf("round-trip tag_name = %q, want %q", tagName, "jazz")
	}

	// The (tag_id, entity_type, entity_id) unique constraint must hold.
	if _, err := db.ExecContext(ctx, `INSERT INTO entity_tags (user_id, tag_id, tag_type, tag_name, entity_type, entity_id) VALUES (1, 1, 'genre', 'jazz', 'artist', 42)`); err == nil {
		t.Error("duplicate (tag_id, entity_type, entity_id) insert succeeded; unique constraint missing")
	}
}

// assertSQLiteColumns verifies column types via PRAGMA table_info.
func assertSQLiteColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `PRAGMA table_info(entity_tags)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]string{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		got[name] = strings.ToUpper(ctype)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	for col, want := range map[string]string{"id": "INTEGER", "user_id": "INTEGER", "entity_id": "INTEGER", "tag_name": "VARCHAR(255)", "created_at": "DATETIME"} {
		if got[col] != want {
			t.Errorf("sqlite column %s type = %q, want %q", col, got[col], want)
		}
	}
}

// assertInformationSchemaColumns verifies column types on live servers.
func assertInformationSchemaColumns(t *testing.T, db *sql.DB, driver string, want map[string]string) {
	t.Helper()
	var query string
	if driver == "postgres" {
		query = `SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'entity_tags' AND table_schema = current_schema()`
	} else {
		query = `SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'entity_tags' AND table_schema = DATABASE()`
	}
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]string{}
	for rows.Next() {
		var name, dtype string
		if err := rows.Scan(&name, &dtype); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		got[strings.ToLower(name)] = strings.ToLower(dtype)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("entity_tags not found in information_schema.columns")
	}
	for col, wantType := range want {
		if got[col] != wantType {
			t.Errorf("%s column %s type = %q, want %q", driver, col, got[col], wantType)
		}
	}
}
