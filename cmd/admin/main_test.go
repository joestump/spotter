package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"spotter/internal/crypto"

	_ "github.com/mattn/go-sqlite3"
)

// testDB creates a temporary SQLite database with the auth tables and returns
// the db handle and a cleanup function.
func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	f, err := os.CreateTemp("", "spotter-rotate-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	path := f.Name()
	f.Close()

	dsn := "file:" + path + "?cache=shared&_fk=1"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		os.Remove(path)
		t.Fatalf("failed to open db: %v", err)
	}

	// Create the tables matching Ent schema.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS navidrome_auths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			password TEXT NOT NULL DEFAULT '',
			last_synced_at DATETIME,
			user_navidrome_auth INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS spotify_auths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			display_name TEXT,
			last_synced_at DATETIME,
			access_token TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			expiry DATETIME,
			user_spotify_auth INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS last_fm_auths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			last_synced_at DATETIME,
			session_key TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			user_lastfm_auth INTEGER
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			db.Close()
			os.Remove(path)
			t.Fatalf("failed to create table: %v", err)
		}
	}

	t.Cleanup(func() {
		db.Close()
		os.Remove(path)
	})

	return db, dsn
}

func randomHexKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}
	return hex.EncodeToString(key)
}

func mustEncryptor(t *testing.T, hexKey string) *crypto.Encryptor {
	t.Helper()
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("bad hex key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyBytes)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}
	return enc
}

func TestParseHexKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid lowercase", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"valid uppercase", "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", false},
		{"valid mixed", "0123456789AbCdEf0123456789aBcDeF0123456789AbCdEf0123456789aBcDeF", false},
		{"too short", "0123456789abcdef", true},
		{"too long", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef00", true},
		{"non-hex chars", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseHexKey(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHexKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunIdenticalKeys(t *testing.T) {
	key := randomHexKey(t)
	_, dsn := testDB(t)
	err := run(key, key, "sqlite3", dsn, false)
	if err == nil {
		t.Fatal("expected error for identical keys")
	}
	if err.Error() != "old key and new key must not be identical" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunNoEncryptedFields(t *testing.T) {
	oldKey := randomHexKey(t)
	newKey := randomHexKey(t)
	_, dsn := testDB(t)

	// No rows in any table => should warn and exit cleanly.
	err := run(oldKey, newKey, "sqlite3", dsn, false)
	if err != nil {
		t.Fatalf("expected no error for empty database, got: %v", err)
	}
}

func TestRunFullRotation(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	oldEnc := mustEncryptor(t, oldKeyHex)

	// Insert encrypted data using old key.
	navPass, err := oldEnc.Encrypt("navidrome-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	spotAccess, err := oldEnc.Encrypt("spotify-access-token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	spotRefresh, err := oldEnc.Encrypt("spotify-refresh-token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	lastFMKey, err := oldEnc.Encrypt("lastfm-session-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", navPass); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec("INSERT INTO spotify_auths (access_token, refresh_token) VALUES (?, ?)", spotAccess, spotRefresh); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec("INSERT INTO last_fm_auths (session_key, username) VALUES (?, ?)", lastFMKey, "testuser"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Run rotation.
	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	// Verify all fields decrypt with new key.
	newEnc := mustEncryptor(t, newKeyHex)

	var encVal string
	db.QueryRow("SELECT password FROM navidrome_auths WHERE id = 1").Scan(&encVal)
	dec, err := newEnc.Decrypt(encVal)
	if err != nil {
		t.Fatalf("decrypt navidrome password with new key: %v", err)
	}
	if dec != "navidrome-password" {
		t.Errorf("navidrome password = %q, want %q", dec, "navidrome-password")
	}

	db.QueryRow("SELECT access_token FROM spotify_auths WHERE id = 1").Scan(&encVal)
	dec, err = newEnc.Decrypt(encVal)
	if err != nil {
		t.Fatalf("decrypt spotify access_token with new key: %v", err)
	}
	if dec != "spotify-access-token" {
		t.Errorf("spotify access_token = %q, want %q", dec, "spotify-access-token")
	}

	db.QueryRow("SELECT refresh_token FROM spotify_auths WHERE id = 1").Scan(&encVal)
	dec, err = newEnc.Decrypt(encVal)
	if err != nil {
		t.Fatalf("decrypt spotify refresh_token with new key: %v", err)
	}
	if dec != "spotify-refresh-token" {
		t.Errorf("spotify refresh_token = %q, want %q", dec, "spotify-refresh-token")
	}

	db.QueryRow("SELECT session_key FROM last_fm_auths WHERE id = 1").Scan(&encVal)
	dec, err = newEnc.Decrypt(encVal)
	if err != nil {
		t.Fatalf("decrypt lastfm session_key with new key: %v", err)
	}
	if dec != "lastfm-session-key" {
		t.Errorf("lastfm session_key = %q, want %q", dec, "lastfm-session-key")
	}

	// Verify old key can no longer decrypt.
	db.QueryRow("SELECT password FROM navidrome_auths WHERE id = 1").Scan(&encVal)
	_, err = oldEnc.Decrypt(encVal)
	if err == nil {
		t.Error("old key should NOT be able to decrypt rotated data")
	}
}

func TestRunWrongOldKey(t *testing.T) {
	correctKeyHex := randomHexKey(t)
	wrongKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	correctEnc := mustEncryptor(t, correctKeyHex)

	// Insert data encrypted with the correct key.
	navPass, _ := correctEnc.Encrypt("secret")
	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", navPass); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Try to rotate with the wrong old key.
	err := run(wrongKeyHex, newKeyHex, "sqlite3", dsn, false)
	if err == nil {
		t.Fatal("expected error when old key is wrong")
	}
}

func TestRunMultipleRows(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	oldEnc := mustEncryptor(t, oldKeyHex)

	// Insert multiple rows.
	for i := 0; i < 5; i++ {
		enc, _ := oldEnc.Encrypt("password-" + string(rune('A'+i)))
		if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", enc); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	// Verify all rows.
	newEnc := mustEncryptor(t, newKeyHex)
	rows, err := db.Query("SELECT id, password FROM navidrome_auths")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var encVal string
		if err := rows.Scan(&id, &encVal); err != nil {
			t.Fatalf("scan: %v", err)
		}
		dec, err := newEnc.Decrypt(encVal)
		if err != nil {
			t.Fatalf("decrypt row %d: %v", id, err)
		}
		expected := "password-" + string(rune('A'+count))
		if dec != expected {
			t.Errorf("row %d: got %q, want %q", id, dec, expected)
		}
		count++
	}
	if count != 5 {
		t.Errorf("expected 5 rows, got %d", count)
	}
}

func TestRunSkipsEmptyFields(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	oldEnc := mustEncryptor(t, oldKeyHex)

	// Insert a row with encrypted password, and another with empty password.
	enc, _ := oldEnc.Encrypt("real-password")
	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", enc); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES ('')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	// Empty field should stay empty.
	var emptyVal string
	db.QueryRow("SELECT password FROM navidrome_auths WHERE id = 2").Scan(&emptyVal)
	if emptyVal != "" {
		t.Errorf("expected empty password for row 2, got %q", emptyVal)
	}
}

func TestRunInvalidOldKeyFormat(t *testing.T) {
	err := run("notahexkey", randomHexKey(t), "sqlite3", "file::memory:", false)
	if err == nil {
		t.Fatal("expected error for invalid old key format")
	}
}

func TestRunInvalidNewKeyFormat(t *testing.T) {
	err := run(randomHexKey(t), "tooshort", "sqlite3", "file::memory:", false)
	if err == nil {
		t.Fatal("expected error for invalid new key format")
	}
}

func TestRunMissingKeys(t *testing.T) {
	err := run("", randomHexKey(t), "sqlite3", "file::memory:", false)
	if err == nil {
		t.Fatal("expected error for missing old key")
	}

	err = run(randomHexKey(t), "", "sqlite3", "file::memory:", false)
	if err == nil {
		t.Fatal("expected error for missing new key")
	}
}

// TestRunMixedMarkedLegacyAndPlaintextRows verifies rotation handles all
// three stored formats in one database (issue #335): marked ciphertext,
// legacy unmarked ciphertext, and plaintext (including plaintext that merely
// looks like base64 ciphertext). After rotation every value must be in the
// marked format under the new key.
func TestRunMixedMarkedLegacyAndPlaintextRows(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	oldEnc := mustEncryptor(t, oldKeyHex)

	// Row 1: marked (current-format) ciphertext.
	marked, err := oldEnc.Encrypt("marked-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !crypto.IsEncrypted(marked) {
		t.Fatalf("expected marked ciphertext, got %q", marked)
	}

	// Row 2: legacy (pre-marker) ciphertext.
	legacy := strings.TrimPrefix(marked2(t, oldEnc, "legacy-password"), crypto.EncryptedMarker)

	// Row 3: plaintext that looks like base64 ciphertext (old-heuristic
	// false positive; stored raw by the buggy write hook).
	lookalike := "abcdefghijklmnopqrstuvwxyzABCDEF01234567"

	// Row 4: ordinary plaintext from before encryption was enabled.
	plain := "just a plain password"

	for _, pw := range []string{marked, legacy, lookalike, plain} {
		if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", pw); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	newEnc := mustEncryptor(t, newKeyHex)
	want := map[int]string{
		1: "marked-password",
		2: "legacy-password",
		3: lookalike,
		4: plain,
	}
	for id, expected := range want {
		var stored string
		if err := db.QueryRow("SELECT password FROM navidrome_auths WHERE id = ?", id).Scan(&stored); err != nil {
			t.Fatalf("select row %d: %v", id, err)
		}
		if !crypto.IsEncrypted(stored) {
			t.Errorf("row %d: stored value %q is not in the marked format", id, stored)
		}
		dec, err := newEnc.Decrypt(stored)
		if err != nil {
			t.Fatalf("row %d: decrypt with new key: %v", id, err)
		}
		if dec != expected {
			t.Errorf("row %d: decrypted = %q, want %q", id, dec, expected)
		}
	}
}

// marked2 encrypts a plaintext and fails the test on error.
func marked2(t *testing.T, enc *crypto.Encryptor, plaintext string) string {
	t.Helper()
	out, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return out
}

// TestRunLegacyCiphertextRotation verifies rotation of a database whose
// values were all written before the marker existed (bare base64).
func TestRunLegacyCiphertextRotation(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	oldEnc := mustEncryptor(t, oldKeyHex)

	legacyPass := strings.TrimPrefix(marked2(t, oldEnc, "navidrome-password"), crypto.EncryptedMarker)
	legacyAccess := strings.TrimPrefix(marked2(t, oldEnc, "spotify-access"), crypto.EncryptedMarker)
	legacyRefresh := strings.TrimPrefix(marked2(t, oldEnc, "spotify-refresh"), crypto.EncryptedMarker)
	legacySession := strings.TrimPrefix(marked2(t, oldEnc, "lastfm-session"), crypto.EncryptedMarker)

	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", legacyPass); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec("INSERT INTO spotify_auths (access_token, refresh_token) VALUES (?, ?)", legacyAccess, legacyRefresh); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec("INSERT INTO last_fm_auths (session_key, username) VALUES (?, ?)", legacySession, "testuser"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	newEnc := mustEncryptor(t, newKeyHex)
	checks := []struct {
		query    string
		expected string
	}{
		{"SELECT password FROM navidrome_auths WHERE id = 1", "navidrome-password"},
		{"SELECT access_token FROM spotify_auths WHERE id = 1", "spotify-access"},
		{"SELECT refresh_token FROM spotify_auths WHERE id = 1", "spotify-refresh"},
		{"SELECT session_key FROM last_fm_auths WHERE id = 1", "lastfm-session"},
	}
	for _, c := range checks {
		var stored string
		if err := db.QueryRow(c.query).Scan(&stored); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if !crypto.IsEncrypted(stored) {
			t.Errorf("%s: value %q is not in the marked format", c.query, stored)
		}
		dec, err := newEnc.Decrypt(stored)
		if err != nil {
			t.Fatalf("%s: decrypt with new key: %v", c.query, err)
		}
		if dec != c.expected {
			t.Errorf("%s: decrypted = %q, want %q", c.query, dec, c.expected)
		}
	}
}

// TestRunAbortsOnUnverifiedOldKey covers the wrong-key-on-all-legacy-database
// hazard: every value is legacy (pre-marker) ciphertext and the operator
// passes a wrong old key. No value positively verifies the key, so rotation
// must hard-abort instead of wrapping garbage under the new key and printing
// a success message.
func TestRunAbortsOnUnverifiedOldKey(t *testing.T) {
	correctKeyHex := randomHexKey(t)
	wrongKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	correctEnc := mustEncryptor(t, correctKeyHex)

	// All-legacy database: bare base64 ciphertext, no markers.
	legacy := strings.TrimPrefix(marked2(t, correctEnc, "secret"), crypto.EncryptedMarker)
	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", legacy); err != nil {
		t.Fatalf("insert: %v", err)
	}

	err := run(wrongKeyHex, newKeyHex, "sqlite3", dsn, false)
	if err == nil {
		t.Fatal("expected hard abort when no value verifies the old key")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should mention --force escape hatch, got: %v", err)
	}

	// The stored value must be untouched.
	var stored string
	if err := db.QueryRow("SELECT password FROM navidrome_auths WHERE id = 1").Scan(&stored); err != nil {
		t.Fatalf("select: %v", err)
	}
	if stored != legacy {
		t.Errorf("stored value was modified despite abort: %q", stored)
	}
	// And still decryptable with the correct key.
	if dec, err := correctEnc.Decrypt(stored); err != nil || dec != "secret" {
		t.Errorf("stored value no longer decrypts with the correct key: %q, %v", dec, err)
	}
}

// TestRunForceTreatsUnverifiedAsPlaintext verifies the --force escape hatch:
// a pre-encryption database whose values are genuinely plaintext refuses to
// rotate by default but proceeds with --force, encrypting every value with
// the new key.
func TestRunForceTreatsUnverifiedAsPlaintext(t *testing.T) {
	oldKeyHex := randomHexKey(t)
	newKeyHex := randomHexKey(t)
	db, dsn := testDB(t)

	plain := "just a plain password"
	if _, err := db.Exec("INSERT INTO navidrome_auths (password) VALUES (?)", plain); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Without --force: hard abort.
	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, false); err == nil {
		t.Fatal("expected abort for unverifiable plaintext-only database without --force")
	}

	// With --force: proceed and encrypt with the new key.
	if err := run(oldKeyHex, newKeyHex, "sqlite3", dsn, true); err != nil {
		t.Fatalf("run() with force error: %v", err)
	}

	newEnc := mustEncryptor(t, newKeyHex)
	var stored string
	if err := db.QueryRow("SELECT password FROM navidrome_auths WHERE id = 1").Scan(&stored); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !crypto.IsEncrypted(stored) {
		t.Errorf("stored value %q is not in the marked format", stored)
	}
	dec, err := newEnc.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt with new key: %v", err)
	}
	if dec != plain {
		t.Errorf("decrypted = %q, want %q", dec, plain)
	}
}
