package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// TokenExpiry is the default JWT expiration time
	TokenExpiry = 24 * time.Hour
	// Issuer identifies this application
	Issuer = "spotter"
)

var (
	// ErrInvalidToken indicates the token is malformed or has an invalid signature
	ErrInvalidToken = errors.New("invalid token")
	// ErrExpiredToken indicates the token has expired
	ErrExpiredToken = errors.New("token has expired")
	// ErrInvalidClaims indicates the token claims are invalid
	ErrInvalidClaims = errors.New("invalid token claims")
)

// SpotterClaims contains the custom claims for Spotter JWTs
type SpotterClaims struct {
	UserID   int    `json:"uid"`
	Username string `json:"usr"`
	jwt.RegisteredClaims
}

// JWTManager handles JWT generation and validation
type JWTManager struct {
	secret []byte
}

// NewJWTManager creates a new JWT manager with the given secret
func NewJWTManager(secret string) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
	}
}

// GenerateToken creates a new JWT for the given user
func (m *JWTManager) GenerateToken(userID int, username string) (string, error) {
	now := time.Now()
	claims := SpotterClaims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenExpiry)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ValidateToken parses and validates a JWT, returning the claims if valid
func (m *JWTManager) ValidateToken(tokenString string) (*SpotterClaims, error) {
	if tokenString == "" {
		return nil, ErrInvalidToken
	}

	token, err := jwt.ParseWithClaims(tokenString, &SpotterClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Ensure the signing method is HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*SpotterClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	// Additional validation
	if claims.UserID <= 0 {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}
