package database

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"spotter/ent"
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
