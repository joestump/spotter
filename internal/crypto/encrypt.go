// Package crypto implements application-layer AES-256-GCM encryption for
// credentials at rest.
//
// Governing: ADR-0006 (application-layer AES-256-GCM encryption for OAuth
// credentials at rest), ADR-0021 (encryption key rotation via admin
// subcommand).
//
// New ciphertext is written as EncryptedMarker + base64(nonce || ciphertext ||
// GCM tag). The explicit versioned marker makes encrypted values
// unambiguously identifiable: IsEncrypted is a marker check, never a
// heuristic. Values written before the marker existed ("legacy" ciphertext,
// bare base64) are still decryptable; LooksLikeLegacyCiphertext identifies
// candidates for the migration paths in internal/database/hooks.go and
// cmd/admin (rotate-key).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// EncryptedMarker is the versioned prefix prepended to all new ciphertext.
// Its alphabet (':') is disjoint from std base64, so a marked value can never
// be mistaken for legacy ciphertext or plaintext that decodes as base64.
// Bump the version (enc:v2:) if the on-disk format ever changes.
// Governing: ADR-0006
const EncryptedMarker = "enc:v1:"

var (
	// ErrInvalidKey is returned when the encryption key is invalid
	ErrInvalidKey = errors.New("encryption key must be 32 bytes for AES-256")
	// ErrInvalidCiphertext is returned when attempting to decrypt invalid data
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	// ErrEncryptionKeyNotSet is returned when encryption key is not configured
	ErrEncryptionKeyNotSet = errors.New("encryption key not configured")
)

// Encryptor handles encryption and decryption of sensitive data
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new encryptor with the given 32-byte key for AES-256
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	return &Encryptor{key: key}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns marked, base64-encoded ciphertext
// Format: EncryptedMarker + base64(nonce || ciphertext || tag)
// Governing: ADR-0006
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create a nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and authenticate
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64 for storage, with the explicit versioned marker
	return EncryptedMarker + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts marked or legacy (unmarked) base64-encoded ciphertext using AES-256-GCM.
// Values written before the marker was introduced are bare base64; both forms
// are accepted so legacy rows remain readable during migration.
// Governing: ADR-0006, ADR-0021
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	// Strip the versioned marker if present (legacy ciphertext has none)
	encoded := strings.TrimPrefix(ciphertext, EncryptedMarker)

	// Decode from base64
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: not base64 encoded", ErrInvalidCiphertext)
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("%w: ciphertext too short", ErrInvalidCiphertext)
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}

	return string(plaintext), nil
}

// IsEncrypted reports whether data carries the explicit versioned ciphertext
// marker. This is an exact check, not a heuristic: only values produced by
// Encrypt (or re-encrypted by the migration/rotation paths) match.
//
// This replaces the old base64-length heuristic, which false-positived on
// plaintext such as a 40-char alphanumeric password — the write hook then
// skipped encryption while the read interceptor attempted GCM decryption,
// bricking the auth row (issue #335).
// Governing: ADR-0006
func IsEncrypted(data string) bool {
	return strings.HasPrefix(data, EncryptedMarker)
}

// LooksLikeLegacyCiphertext reports whether data matches the shape of
// pre-marker ciphertext: std-base64 decodable with at least
// nonce(12) + tag(16) + 1 = 29 decoded bytes.
//
// This is a HEURISTIC — plaintext such as a 40-char base64-alphabet password
// also matches — so callers MUST treat a match only as "attempt decryption"
// and fall back to treating the value as plaintext when GCM authentication
// fails. Used by the read-path self-healing migration in
// internal/database/hooks.go and by the rotate-key admin subcommand.
// Governing: ADR-0006, ADR-0021
func LooksLikeLegacyCiphertext(data string) bool {
	if data == "" {
		return false
	}

	// Try to decode as base64
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return false
	}

	// GCM nonce is 12 bytes by default, tag is 16 bytes
	// Minimum encrypted data would be nonce(12) + tag(16) + at least 1 byte = 29 bytes
	return len(decoded) >= 29
}

// EncryptInt encrypts an integer value and returns marked, base64-encoded ciphertext
func (e *Encryptor) EncryptInt(value int) (string, error) {
	return e.Encrypt(fmt.Sprintf("%d", value))
}

// DecryptInt decrypts marked or legacy base64-encoded ciphertext and returns an integer value
func (e *Encryptor) DecryptInt(ciphertext string) (int, error) {
	plaintext, err := e.Decrypt(ciphertext)
	if err != nil {
		return 0, err
	}

	var value int
	if _, err := fmt.Sscanf(plaintext, "%d", &value); err != nil {
		return 0, fmt.Errorf("failed to parse decrypted integer: %w", err)
	}

	return value, nil
}
