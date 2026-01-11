package auth_test

import (
	"testing"

	"spotter/internal/auth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-key-at-least-32-chars"

func TestGenerateToken(t *testing.T) {
	manager := auth.NewJWTManager(testSecret)

	token, err := manager.GenerateToken(123, "testuser")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestValidateToken_Success(t *testing.T) {
	manager := auth.NewJWTManager(testSecret)

	token, err := manager.GenerateToken(123, "testuser")
	require.NoError(t, err)

	claims, err := manager.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, 123, claims.UserID)
	assert.Equal(t, "testuser", claims.Username)
	assert.Equal(t, "spotter", claims.Issuer)
	assert.Equal(t, "123", claims.Subject)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	manager1 := auth.NewJWTManager("secret-key-one-at-least-32-chars")
	manager2 := auth.NewJWTManager("secret-key-two-at-least-32-chars")

	token, err := manager1.GenerateToken(123, "testuser")
	require.NoError(t, err)

	_, err = manager2.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_MalformedToken(t *testing.T) {
	manager := auth.NewJWTManager(testSecret)

	_, err := manager.ValidateToken("not.a.valid.token")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_EmptyToken(t *testing.T) {
	manager := auth.NewJWTManager(testSecret)

	_, err := manager.ValidateToken("")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_PartialToken(t *testing.T) {
	manager := auth.NewJWTManager(testSecret)

	// Token with only header
	_, err := manager.ValidateToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}
