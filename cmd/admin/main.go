// Governing: ADR-0021 (encryption key rotation), ADR-0006 (AES-256-GCM encryption), ADR-0023 (multi-database support), SPEC key-rotation
package main

import (
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"spotter/internal/config"
	"spotter/internal/crypto"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// Database driver names (ADR-0023 multi-database support). Kept in sync with
// the drivers accepted by internal/config validation.
const (
	driverSQLite3  = "sqlite3"
	driverPostgres = "postgres"
	driverMySQL    = "mysql"
)

// selectEncryptedFieldSQL is the Sprintf format used to read encrypted
// columns: args are the column list and the table name.
const selectEncryptedFieldSQL = "SELECT id, %s FROM %s"

// encryptedField describes a single encrypted column in the database.
type encryptedField struct {
	table  string
	column string
}

// allEncryptedFields lists every encrypted column that must be re-encrypted.
// Governing: SPEC key-rotation REQ "ROT-011"
var allEncryptedFields = []encryptedField{
	{table: "navidrome_auths", column: "password"},
	{table: "spotify_auths", column: "access_token"},
	{table: "spotify_auths", column: "refresh_token"},
	{table: "last_fm_auths", column: "session_key"},
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "rotate-key" {
		fmt.Fprintln(os.Stderr, "Usage: spotter-admin rotate-key --old-key=<hex> --new-key=<hex> [--db=<dsn>] [--force]")
		os.Exit(1)
	}

	// Parse flags after the subcommand.
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	oldKeyHex := fs.String("old-key", "", "Current 64-char hex encryption key (required)")
	newKeyHex := fs.String("new-key", "", "New 64-char hex encryption key (required)")
	dbDSNFlag := fs.String("db", "", "Database DSN (overrides SPOTTER_DATABASE_SOURCE env var)")
	force := fs.Bool("force", false, "Proceed even when no stored value verifies the old key (DANGEROUS: with a wrong old key this irrecoverably corrupts all credentials; only use when the stored values really are plaintext)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	// Governing: ADR-0023 (multi-database support), ADR-0009 (Viper configuration), SPEC key-rotation
	// Determine database driver and DSN via config.LoadDatabase() — the same
	// Viper-backed loader the server uses (SPOTTER_DATABASE_DRIVER /
	// SPOTTER_DATABASE_SOURCE), including driver validation and driver-specific
	// default sources, but scoped to database config only so rotate-key can run
	// standalone without full server configuration (SPEC key-rotation Scenario 1).
	// The --db flag still overrides the configured DSN.
	cfg, err := config.LoadDatabase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load database config: %v\n", err)
		os.Exit(1)
	}
	driver := cfg.Database.Driver
	dsn := cfg.Database.Source
	if *dbDSNFlag != "" {
		dsn = *dbDSNFlag
	}

	if err := run(*oldKeyHex, *newKeyHex, driver, dsn, *force); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(oldKeyHex, newKeyHex, driver, dbDSN string, force bool) error {
	// --- REQ "ROT-001": Validate required flags ---
	if oldKeyHex == "" {
		return fmt.Errorf("--old-key is required")
	}
	if newKeyHex == "" {
		return fmt.Errorf("--new-key is required")
	}

	// --- REQ "ROT-002", "ROT-030": Validate key format ---
	oldKeyBytes, err := parseHexKey(oldKeyHex)
	if err != nil {
		return fmt.Errorf("--old-key: %w", err)
	}
	newKeyBytes, err := parseHexKey(newKeyHex)
	if err != nil {
		return fmt.Errorf("--new-key: %w", err)
	}

	// --- REQ "ROT-003": Keys must differ ---
	if oldKeyHex == newKeyHex {
		return fmt.Errorf("old key and new key must not be identical")
	}

	// --- REQ "ROT-031": Create encryptors ---
	oldEnc, err := crypto.NewEncryptor(oldKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid old key: %w", err)
	}
	newEnc, err := crypto.NewEncryptor(newKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid new key: %w", err)
	}

	// --- REQ "ROT-005": Check database connectivity and lock (driver-aware) ---
	// Governing: ADR-0023 (multi-database support)
	db, err := sql.Open(driver, dbDSN)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Pin the pool to a single connection so session-scoped state (the SQLite
	// exclusive-lock probe, the postgres/mysql advisory locks below) is held
	// on the same connection every statement runs on.
	db.SetMaxOpenConns(1)

	if driver == driverSQLite3 {
		// SQLite-specific: attempt to acquire an exclusive lock to check if the server is running.
		if _, err := db.Exec("PRAGMA locking_mode=EXCLUSIVE"); err != nil {
			return fmt.Errorf("database appears to be locked (is the server running?): %w", err)
		}
		if _, err := db.Exec("BEGIN EXCLUSIVE"); err != nil {
			return fmt.Errorf("database is locked (is the server running?): %w", err)
		}
		if _, err := db.Exec("ROLLBACK"); err != nil {
			return fmt.Errorf("failed to release test lock: %w", err)
		}
	} else {
		// PostgreSQL/MySQL: verify connectivity with a ping.
		if err := db.Ping(); err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		// Governing: ADR-0021 — "server not running" pre-rotation guard.
		// Client-server databases have no equivalent of the SQLite BEGIN
		// EXCLUSIVE probe, so we (a) take a session-scoped advisory lock to
		// serialize rotations, and (b) refuse to run while any other session
		// is connected to this database — the strongest available signal
		// that the Spotter server is still running. Note the connection
		// check is advisory: a server that connects lazily could slip past
		// it, so operators MUST still stop the server before rotating.
		if err := acquireRotationGuard(db, driver); err != nil {
			return err
		}
	}

	// --- REQ "ROT-004": Pre-rotation validation ---
	found, verified, err := verifyOldKey(db, oldEnc)
	if err != nil {
		return fmt.Errorf("pre-rotation validation failed: %w", err)
	}
	if !found {
		fmt.Println("Warning: no encrypted fields found in the database. Nothing to rotate.")
		return nil
	}
	if !verified {
		// No stored value positively proved the old key is correct. On an
		// all-legacy (pre-marker) database this is exactly what a WRONG old
		// key looks like: proceeding would wrap garbage under the new key
		// and report success, corrupting every credential. Hard-abort unless
		// the operator explicitly forces it (e.g. a pre-encryption database
		// whose values really are plaintext).
		if !force {
			return fmt.Errorf("no stored value could be decrypted with --old-key; refusing to rotate because a wrong old key would irrecoverably corrupt all credentials. If the stored values really are unencrypted plaintext, re-run with --force")
		}
		fmt.Fprintln(os.Stderr, "Warning: --force given; values not decryptable with the old key will be treated as plaintext and encrypted with the new key.")
	}

	// --- REQ "ROT-010": All re-encryption in one transaction ---
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	counts, totalFields, err := reencryptAll(tx, oldEnc, newEnc, driver)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: rollback failed: %v\n", rbErr)
		}
		return err
	}

	// --- REQ "ROT-042": Handle commit failure ---
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("COMMIT failed: %w\nPlease check database integrity before retrying", err)
	}

	// --- REQ "ROT-020", "ROT-021": Post-commit verification ---
	if err := verifyNewKey(db, newEnc); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Verification failed after commit: %v\n", err)
		fmt.Fprintln(os.Stderr, "Consider restoring from backup and retrying.")
		return fmt.Errorf("verification failed: %w", err)
	}

	// --- REQ "ROT-050": Print summary ---
	// REQ "ROT-051": NEVER log old or new key values — only print the new key in the env instruction.
	fmt.Println("Key rotation complete.")
	fmt.Printf("  NavidromeAuth: %d rows re-encrypted\n", counts["navidrome_auths"])
	fmt.Printf("  SpotifyAuth:   %d rows re-encrypted (access_token + refresh_token)\n", counts["spotify_auths"])
	fmt.Printf("  LastFMAuth:    %d rows re-encrypted\n", counts["last_fm_auths"])
	fmt.Printf("  Total fields:  %d\n", totalFields)
	fmt.Println("  Verification:  PASSED")
	fmt.Println()
	fmt.Println("Update your environment variable:")
	fmt.Printf("  SPOTTER_SECURITY_ENCRYPTION_KEY=%s\n", newKeyHex)

	return nil
}

// parseHexKey validates a hex key string and converts it to a 32-byte slice.
// Governing: SPEC key-rotation REQ "ROT-002", REQ "ROT-030", REQ "ROT-031"
func parseHexKey(hexKey string) ([]byte, error) {
	if len(hexKey) != 64 {
		return nil, fmt.Errorf("key must be exactly 64 hex characters, got %d", len(hexKey))
	}
	for _, c := range hexKey {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return nil, fmt.Errorf("key must contain only hexadecimal characters [0-9a-fA-F]")
		}
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex key: %w", err)
	}
	return key, nil
}

// rotationLockID and rotationLockName identify the cross-process advisory
// lock that serializes key rotations on client-server databases.
const (
	rotationLockID   = int64(0x53504f54524f5431) // ASCII "SPOTROT1"
	rotationLockName = "spotter_key_rotation"
)

// acquireRotationGuard is the "server not running" pre-rotation check for
// client-server databases: it takes a session-scoped advisory lock (so two
// rotations cannot run concurrently) and refuses to proceed while any other
// session is connected to this database. The lock lives on the pool's single
// pinned connection (SetMaxOpenConns(1)) and is released automatically when
// the process exits.
//
// Known caveats — the guard is best-effort, stopping the server before
// rotation remains mandatory (ADR-0021):
//   - A server that opens its connections lazily can slip past the
//     other-sessions check.
//   - MySQL: information_schema.processlist only shows other users' sessions
//     when the rotation user has the PROCESS privilege; without it the check
//     can pass while another user's server session exists.
//   - If the pinned connection drops and database/sql reconnects, the
//     advisory lock is silently lost for the remainder of the run.
//
// Governing: ADR-0021, SPEC key-rotation REQ "ROT-005"
func acquireRotationGuard(db *sql.DB, driver string) error {
	switch driver {
	case driverPostgres:
		var locked bool
		if err := db.QueryRow("SELECT pg_try_advisory_lock($1)", rotationLockID).Scan(&locked); err != nil {
			return fmt.Errorf("failed to acquire rotation advisory lock: %w", err)
		}
		if !locked {
			return fmt.Errorf("another key rotation is already in progress (advisory lock %d is held)", rotationLockID)
		}
		var others int
		if err := db.QueryRow("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()").Scan(&others); err != nil {
			return fmt.Errorf("failed to check for other database sessions: %w", err)
		}
		if others > 0 {
			return fmt.Errorf("%d other session(s) connected to the database (is the server running?); stop the server before rotating", others)
		}
	case driverMySQL:
		var locked sql.NullInt64
		if err := db.QueryRow("SELECT GET_LOCK(?, 0)", rotationLockName).Scan(&locked); err != nil {
			return fmt.Errorf("failed to acquire rotation advisory lock: %w", err)
		}
		if !locked.Valid || locked.Int64 != 1 {
			return fmt.Errorf("another key rotation is already in progress (lock %q is held)", rotationLockName)
		}
		var others int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.processlist WHERE id <> CONNECTION_ID() AND db = DATABASE()").Scan(&others); err != nil {
			return fmt.Errorf("failed to check for other database sessions: %w", err)
		}
		if others > 0 {
			return fmt.Errorf("%d other session(s) connected to the database (is the server running?); stop the server before rotating", others)
		}
	}
	return nil
}

// verifyOldKey scans encrypted fields for evidence that the old key is
// correct before any data is modified. Values are classified by format
// (ADR-0006):
//   - marked ciphertext (enc:v1:) MUST decrypt with the old key — a failure
//     is definitive proof of a wrong key (or corruption) and aborts rotation
//   - legacy-shaped values (bare base64) are attempted; success verifies the
//     old key, failure is ambiguous (may be plaintext that merely looks like
//     ciphertext — issue #335) and does not abort
//   - anything else is plaintext from before encryption was enabled
//
// Returns (found=false, ...) when no non-empty values exist (nothing to
// rotate), and verified=true only when at least one stored value decrypted
// with the old key. The caller MUST hard-abort on found && !verified unless
// the operator explicitly forces the rotation: on an all-legacy database a
// wrong old key is indistinguishable from all-plaintext data, and rotating
// with a wrong key would irrecoverably corrupt every credential.
// Governing: SPEC key-rotation REQ "ROT-004", ADR-0006, ADR-0021
func verifyOldKey(db *sql.DB, oldEnc *crypto.Encryptor) (found, verified bool, err error) {
	for _, f := range allEncryptedFields {
		query := fmt.Sprintf(selectEncryptedFieldSQL, f.column, f.table)
		rows, err := db.Query(query)
		if err != nil {
			return false, false, fmt.Errorf("failed to query %s.%s: %w", f.table, f.column, err)
		}

		for rows.Next() {
			var id int
			var val sql.NullString
			if err := rows.Scan(&id, &val); err != nil {
				_ = rows.Close()
				return false, false, fmt.Errorf("failed to scan %s.%s: %w", f.table, f.column, err)
			}
			if !val.Valid || val.String == "" {
				continue
			}
			found = true

			if crypto.IsEncrypted(val.String) {
				if _, err := oldEnc.Decrypt(val.String); err != nil {
					_ = rows.Close()
					return true, false, fmt.Errorf("old key cannot decrypt %s.%s (row id=%d): %w", f.table, f.column, id, err)
				}
				_ = rows.Close()
				return true, true, nil
			}
			if crypto.LooksLikeLegacyCiphertext(val.String) {
				if _, err := oldEnc.Decrypt(val.String); err == nil {
					_ = rows.Close()
					return true, true, nil
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return false, false, fmt.Errorf("error iterating %s.%s: %w", f.table, f.column, err)
		}
		_ = rows.Close()
	}

	return found, false, nil
}

// resolveStoredValue recovers the plaintext behind a stored credential value
// during rotation. Marked ciphertext MUST decrypt with the old key;
// legacy-shaped values fall back to being treated as plaintext on GCM
// failure (self-heal, issue #335); anything else is plaintext from before
// encryption was enabled. Every value is subsequently re-encrypted with the
// new key, so rotation also migrates legacy ciphertext and plaintext rows to
// the marked format.
// Governing: ADR-0006, ADR-0021
func resolveStoredValue(oldEnc *crypto.Encryptor, stored string) (plaintext string, treatedAsPlaintext bool, err error) {
	if crypto.IsEncrypted(stored) {
		decrypted, err := oldEnc.Decrypt(stored)
		if err != nil {
			return "", false, err
		}
		return decrypted, false, nil
	}
	if crypto.LooksLikeLegacyCiphertext(stored) {
		if decrypted, err := oldEnc.Decrypt(stored); err == nil {
			return decrypted, false, nil
		}
	}
	return stored, true, nil
}

// reencryptAll re-encrypts all encrypted fields in a single transaction.
// It handles all three stored formats (marked ciphertext, legacy unmarked
// ciphertext, plaintext) via resolveStoredValue and always writes back the
// marked format under the new key, so a rotated database is uniformly
// enc:v1:-marked. Returns per-table row counts and total field count.
// Governing: SPEC key-rotation REQ "ROT-010", REQ "ROT-011", REQ "ROT-012", REQ "ROT-013", ADR-0023 (multi-database support)
func reencryptAll(tx *sql.Tx, oldEnc, newEnc *crypto.Encryptor, driver string) (map[string]int, int, error) {
	counts := make(map[string]int)
	totalFields := 0

	// Group fields by table to count rows per table (not per field).
	type tableFields struct {
		table   string
		columns []string
	}
	grouped := make(map[string]*tableFields)
	for _, f := range allEncryptedFields {
		if _, ok := grouped[f.table]; !ok {
			grouped[f.table] = &tableFields{table: f.table}
		}
		grouped[f.table].columns = append(grouped[f.table].columns, f.column)
	}

	// A buffered row of id + column values.
	type bufferedRow struct {
		id   int
		vals []sql.NullString
	}

	for _, tf := range grouped {
		cols := strings.Join(tf.columns, ", ")
		query := fmt.Sprintf(selectEncryptedFieldSQL, cols, tf.table)
		rows, err := tx.Query(query)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query %s: %w", tf.table, err)
		}

		// Buffer all rows before issuing UPDATEs: with the pool pinned to a
		// single connection, mysql and lib/pq cannot execute tx.Exec while a
		// tx.Query result set is still open on the same connection (SQLite
		// tolerates it, the other drivers error at runtime).
		var buffered []bufferedRow
		for rows.Next() {
			// Build scan destinations: id + N columns.
			scanDest := make([]interface{}, 1+len(tf.columns))
			var id int
			scanDest[0] = &id
			vals := make([]sql.NullString, len(tf.columns))
			for i := range vals {
				scanDest[i+1] = &vals[i]
			}

			if err := rows.Scan(scanDest...); err != nil {
				_ = rows.Close()
				return nil, 0, fmt.Errorf("failed to scan row from %s: %w", tf.table, err)
			}
			buffered = append(buffered, bufferedRow{id: id, vals: vals})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("error iterating %s: %w", tf.table, err)
		}
		_ = rows.Close()

		rowCount := 0
		for _, row := range buffered {
			rowModified := false
			for i, col := range tf.columns {
				if !row.vals[i].Valid || row.vals[i].String == "" {
					continue // REQ "ROT-012": skip null/empty
				}

				// REQ "ROT-040": Recover plaintext (marked, legacy, or plaintext value)
				plaintext, treatedAsPlaintext, err := resolveStoredValue(oldEnc, row.vals[i].String)
				if err != nil {
					return nil, 0, fmt.Errorf("decryption failed for %s.%s (row id=%d): %w", tf.table, col, row.id, err)
				}
				if treatedAsPlaintext {
					fmt.Fprintf(os.Stderr, "Warning: %s.%s (row id=%d) is not decryptable with the old key; treating as plaintext and encrypting with the new key\n", tf.table, col, row.id)
				}

				// REQ "ROT-041": Encrypt with new key (always marked format)
				newCipher, err := newEnc.Encrypt(plaintext)
				if err != nil {
					return nil, 0, fmt.Errorf("encryption failed for %s.%s (row id=%d): %w", tf.table, col, row.id, err)
				}

				var updateSQL string
				if driver == driverPostgres {
					updateSQL = fmt.Sprintf("UPDATE %s SET %s = $1 WHERE id = $2", tf.table, col)
				} else {
					updateSQL = fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", tf.table, col)
				}
				if _, err := tx.Exec(updateSQL, newCipher, row.id); err != nil {
					return nil, 0, fmt.Errorf("update failed for %s.%s (row id=%d): %w", tf.table, col, row.id, err)
				}
				totalFields++
				rowModified = true
			}
			if rowModified {
				rowCount++
			}
		}
		counts[tf.table] = rowCount
	}

	return counts, totalFields, nil
}

// verifyNewKey reads all encrypted fields from the database and verifies
// they can be decrypted with the new key.
// Governing: SPEC key-rotation REQ "ROT-020", REQ "ROT-021"
func verifyNewKey(db *sql.DB, newEnc *crypto.Encryptor) error {
	for _, f := range allEncryptedFields {
		query := fmt.Sprintf(selectEncryptedFieldSQL, f.column, f.table)
		rows, err := db.Query(query)
		if err != nil {
			return fmt.Errorf("verification query failed for %s.%s: %w", f.table, f.column, err)
		}

		for rows.Next() {
			var id int
			var val sql.NullString
			if err := rows.Scan(&id, &val); err != nil {
				_ = rows.Close()
				return fmt.Errorf("verification scan failed for %s.%s: %w", f.table, f.column, err)
			}
			if !val.Valid || val.String == "" {
				continue
			}
			if _, err := newEnc.Decrypt(val.String); err != nil {
				_ = rows.Close()
				return fmt.Errorf("verification decrypt failed for %s.%s (row id=%d): %w", f.table, f.column, id, err)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("verification iteration failed for %s.%s: %w", f.table, f.column, err)
		}
		_ = rows.Close()
	}
	return nil
}
