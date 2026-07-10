package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"strings"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/navidromeauth"
	"spotter/internal/crypto"

	_ "github.com/mattn/go-sqlite3"
)

func TestEncryptionHooks(t *testing.T) {
	// Create a test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database
	client, err := ent.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Register encryption hooks
	RegisterEncryptionHooks(client, encryptor)

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create a test user
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create NavidromeAuth with password
	plainPassword := "mysecretpassword123"
	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(plainPassword).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create navidrome auth: %v", err)
	}

	// Verify password is decrypted when read
	if auth.Password != plainPassword {
		t.Errorf("password after create = %q, want %q", auth.Password, plainPassword)
	}

	// Query the auth back from database
	authFromDB, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query navidrome auth: %v", err)
	}

	// Verify password is properly decrypted
	if authFromDB.Password != plainPassword {
		t.Errorf("password after query = %q, want %q", authFromDB.Password, plainPassword)
	}

	// The fact that we got the correct plaintext back proves encryption is working
	// (password was encrypted on save, decrypted on load)

	// Test password update
	newPassword := "newpassword456"
	_, err = client.NavidromeAuth.UpdateOne(auth).
		SetPassword(newPassword).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to update password: %v", err)
	}

	// Query updated auth
	updatedAuth, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}

	// Verify new password is decrypted correctly
	if updatedAuth.Password != newPassword {
		t.Errorf("updated password = %q, want %q", updatedAuth.Password, newPassword)
	}

	// Successfully updated and retrieved password proves encryption is still working
}

func TestBackwardCompatibility(t *testing.T) {
	// Create a test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database without hooks
	client, err := ent.Open("sqlite3", "file:ent_compat?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create a test user
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create auth with plaintext password (simulating old data before encryption)
	// We do this by creating the auth without hooks registered yet
	plaintextPassword := "oldplaintextpassword"
	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(plaintextPassword).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	// Now register hooks (simulating app restart with encryption enabled)
	RegisterEncryptionHooks(client, encryptor)

	// Query the auth - should still work with plaintext password
	authFromDB, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query auth: %v", err)
	}

	// Should read the plaintext password correctly (backward compatibility)
	if authFromDB.Password != plaintextPassword {
		t.Errorf("password = %q, want %q", authFromDB.Password, plaintextPassword)
	}

	// Update the password (will now encrypt it)
	newPassword := "newencryptedpassword"
	_, err = client.NavidromeAuth.UpdateOne(authFromDB).
		SetPassword(newPassword).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to update password: %v", err)
	}

	// Verify we can still read the updated password
	updatedAuth, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}

	if updatedAuth.Password != newPassword {
		t.Errorf("updated password = %q, want %q", updatedAuth.Password, newPassword)
	}

	// Successfully migrated from plaintext to encrypted storage
}

// TestSpotifyAuthEncryption tests that SpotifyAuth tokens are encrypted
func TestSpotifyAuthEncryption(t *testing.T) {
	// Create test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database
	client, err := ent.Open("sqlite3", "file:ent_spotify?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Register encryption hooks
	RegisterEncryptionHooks(client, encryptor)

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create a user
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create SpotifyAuth with plaintext tokens
	accessToken := "test_access_token_12345"
	refreshToken := "test_refresh_token_67890"

	auth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken(accessToken).
		SetRefreshToken(refreshToken).
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create spotify auth: %v", err)
	}

	// Verify tokens are decrypted when read
	if auth.AccessToken != accessToken {
		t.Errorf("access_token after create = %q, want %q", auth.AccessToken, accessToken)
	}
	if auth.RefreshToken != refreshToken {
		t.Errorf("refresh_token after create = %q, want %q", auth.RefreshToken, refreshToken)
	}

	// Query from database
	authFromDB, err := client.SpotifyAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query spotify auth: %v", err)
	}

	// Verify tokens are properly decrypted
	if authFromDB.AccessToken != accessToken {
		t.Errorf("access_token after query = %q, want %q", authFromDB.AccessToken, accessToken)
	}
	if authFromDB.RefreshToken != refreshToken {
		t.Errorf("refresh_token after query = %q, want %q", authFromDB.RefreshToken, refreshToken)
	}

	// The fact that we got correct plaintext back proves encryption is working
	// (tokens were encrypted on save, decrypted on load)

	// Test update
	newAccessToken := "new_access_token_xyz"
	newRefreshToken := "new_refresh_token_abc"

	_, err = client.SpotifyAuth.UpdateOne(auth).
		SetAccessToken(newAccessToken).
		SetRefreshToken(newRefreshToken).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to update spotify auth: %v", err)
	}

	// Query updated auth
	updatedAuth, err := client.SpotifyAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}

	// Verify new tokens are decrypted
	if updatedAuth.AccessToken != newAccessToken {
		t.Errorf("updated access_token = %q, want %q", updatedAuth.AccessToken, newAccessToken)
	}
	if updatedAuth.RefreshToken != newRefreshToken {
		t.Errorf("updated refresh_token = %q, want %q", updatedAuth.RefreshToken, newRefreshToken)
	}
}

// TestSpotifyAuthBackwardCompatibility tests backward compatibility for SpotifyAuth
func TestSpotifyAuthBackwardCompatibility(t *testing.T) {
	// Create test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database without hooks
	client, err := ent.Open("sqlite3", "file:ent_spotify_compat?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create a user
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create auth with plaintext tokens (before encryption enabled)
	plaintextAccess := "legacy_plaintext_access"
	plaintextRefresh := "legacy_plaintext_refresh"
	auth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken(plaintextAccess).
		SetRefreshToken(plaintextRefresh).
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	// Now register hooks (simulating app restart with encryption enabled)
	RegisterEncryptionHooks(client, encryptor)

	// Query the auth - should still work with plaintext tokens
	authFromDB, err := client.SpotifyAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query auth: %v", err)
	}

	// Should read plaintext tokens correctly (backward compatibility)
	if authFromDB.AccessToken != plaintextAccess {
		t.Errorf("access_token = %q, want %q", authFromDB.AccessToken, plaintextAccess)
	}
	if authFromDB.RefreshToken != plaintextRefresh {
		t.Errorf("refresh_token = %q, want %q", authFromDB.RefreshToken, plaintextRefresh)
	}

	// Update the tokens (will now encrypt them)
	newAccessToken := "new_encrypted_access"
	newRefreshToken := "new_encrypted_refresh"
	_, err = client.SpotifyAuth.UpdateOne(authFromDB).
		SetAccessToken(newAccessToken).
		SetRefreshToken(newRefreshToken).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to update tokens: %v", err)
	}

	// Verify we can still read the updated tokens
	updatedAuth, err := client.SpotifyAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}

	if updatedAuth.AccessToken != newAccessToken {
		t.Errorf("updated access_token = %q, want %q", updatedAuth.AccessToken, newAccessToken)
	}
	if updatedAuth.RefreshToken != newRefreshToken {
		t.Errorf("updated refresh_token = %q, want %q", updatedAuth.RefreshToken, newRefreshToken)
	}
}

// TestLastFMAuthEncryption tests that LastFMAuth session_key is encrypted
func TestLastFMAuthEncryption(t *testing.T) {
	// Create test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database
	client, err := ent.Open("sqlite3", "file:ent_lastfm?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Register encryption hooks
	RegisterEncryptionHooks(client, encryptor)

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create a user
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create LastFMAuth with plaintext session_key
	sessionKey := "test_session_key_12345"
	username := "lastfm_user"

	auth, err := client.LastFMAuth.Create().
		SetUser(user).
		SetSessionKey(sessionKey).
		SetUsername(username).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create lastfm auth: %v", err)
	}

	// Verify session_key is decrypted
	if auth.SessionKey != sessionKey {
		t.Errorf("session_key after create = %q, want %q", auth.SessionKey, sessionKey)
	}

	// Query from database
	authFromDB, err := client.LastFMAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query lastfm auth: %v", err)
	}

	// Verify session_key is properly decrypted
	if authFromDB.SessionKey != sessionKey {
		t.Errorf("session_key after query = %q, want %q", authFromDB.SessionKey, sessionKey)
	}

	// The fact that we got correct plaintext back proves encryption is working

	// Test update
	newSessionKey := "new_session_key_xyz"
	_, err = client.LastFMAuth.UpdateOne(auth).
		SetSessionKey(newSessionKey).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to update session_key: %v", err)
	}

	// Query updated auth
	updatedAuth, err := client.LastFMAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}

	// Verify new session_key is decrypted
	if updatedAuth.SessionKey != newSessionKey {
		t.Errorf("updated session_key = %q, want %q", updatedAuth.SessionKey, newSessionKey)
	}
}

// TestMultipleAuthRecords tests decryption with multiple records
func TestMultipleAuthRecords(t *testing.T) {
	// Create test encryption key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	// Create in-memory database
	client, err := ent.Open("sqlite3", "file:ent_multiple?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	// Register encryption hooks
	RegisterEncryptionHooks(client, encryptor)

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	// Create multiple users with SpotifyAuth
	for i := 0; i < 3; i++ {
		user, err := client.User.Create().
			SetUsername("testuser" + string(rune('0'+i))).
			Save(ctx)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		_, err = client.SpotifyAuth.Create().
			SetUser(user).
			SetAccessToken("access_token_" + string(rune('0'+i))).
			SetRefreshToken("refresh_token_" + string(rune('0'+i))).
			SetExpiry(time.Now().Add(time.Hour)).
			Save(ctx)
		if err != nil {
			t.Fatalf("failed to create spotify auth: %v", err)
		}
	}

	// Query all SpotifyAuth records
	auths, err := client.SpotifyAuth.Query().All(ctx)
	if err != nil {
		t.Fatalf("failed to query all auths: %v", err)
	}

	// Verify we got all 3
	if len(auths) != 3 {
		t.Fatalf("expected 3 auth records, got %d", len(auths))
	}

	// Verify all tokens are decrypted
	for i, auth := range auths {
		expectedAccess := "access_token_" + string(rune('0'+i))
		expectedRefresh := "refresh_token_" + string(rune('0'+i))

		if auth.AccessToken != expectedAccess {
			t.Errorf("auth[%d]: expected access_token %q, got %q", i, expectedAccess, auth.AccessToken)
		}
		if auth.RefreshToken != expectedRefresh {
			t.Errorf("auth[%d]: expected refresh_token %q, got %q", i, expectedRefresh, auth.RefreshToken)
		}
	}
}

// rawColumnValue reads the raw stored value of a column, bypassing Ent and
// the decrypt interceptors, via a second connection to the same shared-cache
// in-memory database.
func rawColumnValue(t *testing.T, dsn, query string, args ...interface{}) string {
	t.Helper()
	rawDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	defer rawDB.Close()

	var value string
	if err := rawDB.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("failed to read raw value: %v", err)
	}
	return value
}

// TestBase64LookalikePasswordRoundTrip covers the auth-row bricking bug from
// issue #335: a 40-char base64-alphabet password false-positived the old
// IsEncrypted heuristic, so the write hook skipped encryption and the read
// interceptor then failed GCM authentication, erroring every query for the
// row. With the explicit marker the password must round-trip and be stored
// encrypted.
func TestBase64LookalikePasswordRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	dsn := "file:ent_lookalike?mode=memory&cache=shared&_fk=1"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	RegisterEncryptionHooks(client, encryptor)

	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 40 chars, pure base64 alphabet, decodes to 30 bytes — the old
	// heuristic treated this plaintext as ciphertext.
	password := "abcdefghijklmnopqrstuvwxyzABCDEF01234567"

	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(password).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create navidrome auth: %v", err)
	}
	if auth.Password != password {
		t.Errorf("password after create = %q, want %q", auth.Password, password)
	}

	// This read used to fail with a GCM authentication error.
	authFromDB, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query navidrome auth (bricked row, issue #335): %v", err)
	}
	if authFromDB.Password != password {
		t.Errorf("password after query = %q, want %q", authFromDB.Password, password)
	}

	// Verify the stored value is marked ciphertext, not plaintext.
	raw := rawColumnValue(t, dsn, "SELECT password FROM navidrome_auths WHERE id = ?", auth.ID)
	if raw == password {
		t.Errorf("password stored as plaintext, want marked ciphertext")
	}
	if !crypto.IsEncrypted(raw) {
		t.Errorf("stored password %q does not carry the encryption marker", raw)
	}
	decrypted, err := encryptor.Decrypt(raw)
	if err != nil {
		t.Fatalf("failed to decrypt stored password: %v", err)
	}
	if decrypted != password {
		t.Errorf("decrypted stored password = %q, want %q", decrypted, password)
	}
}

// TestPlaintextLookalikeSelfHealsOnRead simulates a row bricked by the old
// heuristic: plaintext that looks like base64 ciphertext was stored raw
// (write hook skipped encryption). Reading it must not error; instead the
// value is treated as plaintext and the row self-heals to marked ciphertext.
func TestPlaintextLookalikeSelfHealsOnRead(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	dsn := "file:ent_selfheal?mode=memory&cache=shared&_fk=1"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Store the lookalike plaintext raw (no hooks yet), simulating a row
	// written while the old heuristic skipped encryption.
	password := "abcdefghijklmnopqrstuvwxyzABCDEF01234567"
	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(password).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	RegisterEncryptionHooks(client, encryptor)

	// This read used to error the whole query (GCM failure on plaintext).
	authFromDB, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("read of plaintext-lookalike row errored instead of self-healing: %v", err)
	}
	if authFromDB.Password != password {
		t.Errorf("password = %q, want %q", authFromDB.Password, password)
	}

	// The row must have self-healed: stored value is now marked ciphertext.
	raw := rawColumnValue(t, dsn, "SELECT password FROM navidrome_auths WHERE id = ?", auth.ID)
	if !crypto.IsEncrypted(raw) {
		t.Fatalf("row did not self-heal: stored value %q has no marker", raw)
	}
	decrypted, err := encryptor.Decrypt(raw)
	if err != nil {
		t.Fatalf("failed to decrypt self-healed value: %v", err)
	}
	if decrypted != password {
		t.Errorf("self-healed value decrypts to %q, want %q", decrypted, password)
	}

	// Subsequent reads keep working.
	again, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to re-read self-healed row: %v", err)
	}
	if again.Password != password {
		t.Errorf("password after self-heal = %q, want %q", again.Password, password)
	}
}

// TestLegacyCiphertextSelfHealsOnRead verifies rows encrypted before the
// marker existed (bare base64 ciphertext) still decrypt on read and are
// migrated to the marked format.
func TestLegacyCiphertextSelfHealsOnRead(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	dsn := "file:ent_legacyheal?mode=memory&cache=shared&_fk=1"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Build legacy (unmarked) ciphertext and store it raw (no hooks yet).
	sessionKey := "legacy-session-key"
	marked, err := encryptor.Encrypt(sessionKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	legacy := strings.TrimPrefix(marked, crypto.EncryptedMarker)

	auth, err := client.LastFMAuth.Create().
		SetUser(user).
		SetSessionKey(legacy).
		SetUsername("lastfm_user").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	RegisterEncryptionHooks(client, encryptor)

	authFromDB, err := client.LastFMAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query legacy-ciphertext row: %v", err)
	}
	if authFromDB.SessionKey != sessionKey {
		t.Errorf("session_key = %q, want %q", authFromDB.SessionKey, sessionKey)
	}

	// The row must have migrated to the marked format.
	raw := rawColumnValue(t, dsn, "SELECT session_key FROM last_fm_auths WHERE id = ?", auth.ID)
	if !crypto.IsEncrypted(raw) {
		t.Fatalf("legacy row did not migrate: stored value %q has no marker", raw)
	}
	decrypted, err := encryptor.Decrypt(raw)
	if err != nil {
		t.Fatalf("failed to decrypt migrated value: %v", err)
	}
	if decrypted != sessionKey {
		t.Errorf("migrated value decrypts to %q, want %q", decrypted, sessionKey)
	}
}

// TestMarkerPrefixedPasswordRoundTrip covers the write-path hazard flagged in
// review: a user password that literally starts with the enc:v1: marker must
// still be encrypted on write (never stored as plaintext) and must round-trip
// on read. Deciding by value shape in the write hook would store it raw and
// then hard-error every read on GCM failure — the bricking bug again.
func TestMarkerPrefixedPasswordRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	dsn := "file:ent_markerprefix?mode=memory&cache=shared&_fk=1"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	RegisterEncryptionHooks(client, encryptor)

	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Adversarial user input: plaintext that impersonates the marker.
	password := crypto.EncryptedMarker + "supersecret"

	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(password).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create navidrome auth: %v", err)
	}
	if auth.Password != password {
		t.Errorf("password after create = %q, want %q", auth.Password, password)
	}

	// The read must return the original plaintext, not error.
	authFromDB, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query navidrome auth with marker-prefixed password: %v", err)
	}
	if authFromDB.Password != password {
		t.Errorf("password after query = %q, want %q", authFromDB.Password, password)
	}

	// The stored value must be real ciphertext of the input, not the input itself.
	raw := rawColumnValue(t, dsn, "SELECT password FROM navidrome_auths WHERE id = ?", auth.ID)
	if raw == password {
		t.Fatalf("marker-prefixed password stored as plaintext")
	}
	if !crypto.IsEncrypted(raw) {
		t.Errorf("stored password %q does not carry the encryption marker", raw)
	}
	decrypted, err := encryptor.Decrypt(raw)
	if err != nil {
		t.Fatalf("failed to decrypt stored password: %v", err)
	}
	if decrypted != password {
		t.Errorf("decrypted stored password = %q, want %q", decrypted, password)
	}

	// Update path must behave the same.
	newPassword := crypto.EncryptedMarker + "another-secret"
	if _, err := client.NavidromeAuth.UpdateOne(auth).SetPassword(newPassword).Save(ctx); err != nil {
		t.Fatalf("failed to update password: %v", err)
	}
	updated, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("failed to query updated auth: %v", err)
	}
	if updated.Password != newPassword {
		t.Errorf("updated password = %q, want %q", updated.Password, newPassword)
	}
}

// TestSelfHealDoesNotClobberConcurrentUpdate verifies the compare-and-swap
// guard on heal write-backs: when the stored value changes between the read
// and the heal write, the heal must not overwrite the newer value.
func TestSelfHealDoesNotClobberConcurrentUpdate(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	encryptor, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	dsn := "file:ent_healrace?mode=memory&cache=shared&_fk=1"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer client.Close()

	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	ctx := context.Background()

	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Legacy row stored raw (no hooks yet).
	lookalike := "abcdefghijklmnopqrstuvwxyzABCDEF01234567"
	auth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword(lookalike).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	RegisterEncryptionHooks(client, encryptor)

	// Simulate the race deterministically: resolve the heal value the same
	// way the interceptor does, then change the stored value before the
	// conditional heal write fires.
	_, healed, _, err := resolveEncryptedField(encryptor, lookalike)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if healed == "" {
		t.Fatal("expected a heal value for the lookalike plaintext")
	}

	newPassword := "user-changed-password"
	if _, err := client.NavidromeAuth.UpdateOne(auth).SetPassword(newPassword).Save(ctx); err != nil {
		t.Fatalf("concurrent update: %v", err)
	}
	rawAfterUpdate := rawColumnValue(t, dsn, "SELECT password FROM navidrome_auths WHERE id = ?", auth.ID)

	// The stale heal write: guarded by the old raw value, it must not match.
	n, err := client.NavidromeAuth.Update().
		Where(navidromeauth.ID(auth.ID), navidromeauth.Password(lookalike)).
		SetPassword(healed).
		Save(withSelfHeal(ctx))
	if err != nil {
		t.Fatalf("stale heal write errored: %v", err)
	}
	if n != 0 {
		t.Errorf("stale heal write modified %d row(s), want 0", n)
	}

	raw := rawColumnValue(t, dsn, "SELECT password FROM navidrome_auths WHERE id = ?", auth.ID)
	if raw != rawAfterUpdate {
		t.Errorf("stored value changed by stale heal: %q != %q", raw, rawAfterUpdate)
	}

	// The row still reads back as the user's new password.
	got, err := client.NavidromeAuth.Get(ctx, auth.ID)
	if err != nil {
		t.Fatalf("read after stale heal: %v", err)
	}
	if got.Password != newPassword {
		t.Errorf("password = %q, want %q", got.Password, newPassword)
	}
}
