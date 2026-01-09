package database

import (
	"context"
	"fmt"

	"spotter/ent"
	"spotter/ent/hook"
	"spotter/internal/crypto"
)

// RegisterEncryptionHooks registers hooks to encrypt/decrypt sensitive data
func RegisterEncryptionHooks(client *ent.Client, encryptor *crypto.Encryptor) {
	// Hook for encrypting password on NavidromeAuth create/update
	client.NavidromeAuth.Use(encryptPasswordHook(encryptor))

	// Hook for decrypting password on NavidromeAuth query
	client.NavidromeAuth.Intercept(decryptPasswordInterceptor(encryptor))

	// Hook for encrypting tokens on SpotifyAuth create/update
	client.SpotifyAuth.Use(encryptSpotifyAuthHook(encryptor))

	// Hook for decrypting tokens on SpotifyAuth query
	client.SpotifyAuth.Intercept(decryptSpotifyAuthInterceptor(encryptor))

	// Hook for encrypting session key on LastFMAuth create/update
	client.LastFMAuth.Use(encryptLastFMAuthHook(encryptor))

	// Hook for decrypting session key on LastFMAuth query
	client.LastFMAuth.Intercept(decryptLastFMAuthInterceptor(encryptor))
}

// encryptPasswordHook encrypts the password field before saving to database
// and decrypts it in the returned entity
func encryptPasswordHook(encryptor *crypto.Encryptor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return hook.NavidromeAuthFunc(func(ctx context.Context, m *ent.NavidromeAuthMutation) (ent.Value, error) {
			// Remember original password for decryption after save
			var originalPassword string
			if password, exists := m.Password(); exists {
				originalPassword = password
				// Check if already encrypted (for idempotency)
				if !crypto.IsEncrypted(password) {
					// Encrypt the password
					encrypted, err := encryptor.Encrypt(password)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt password: %w", err)
					}
					m.SetPassword(encrypted)
				}
			}

			// Execute the mutation
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}

			// Decrypt the password in the returned entity
			if auth, ok := value.(*ent.NavidromeAuth); ok && originalPassword != "" {
				auth.Password = originalPassword
			}

			return value, nil
		})
	}
}

// decryptPasswordInterceptor decrypts password fields after loading from database
func decryptPasswordInterceptor(encryptor *crypto.Encryptor) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			// Execute the query
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			// Decrypt passwords in the results
			switch result := v.(type) {
			case *ent.NavidromeAuth:
				if err := decryptNavidromeAuth(encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.NavidromeAuth:
				for _, auth := range result {
					if err := decryptNavidromeAuth(encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptNavidromeAuth decrypts the password field in a NavidromeAuth entity
func decryptNavidromeAuth(encryptor *crypto.Encryptor, auth *ent.NavidromeAuth) error {
	if auth == nil || auth.Password == "" {
		return nil
	}

	// Check if password is encrypted (backward compatibility)
	if crypto.IsEncrypted(auth.Password) {
		decrypted, err := encryptor.Decrypt(auth.Password)
		if err != nil {
			return fmt.Errorf("failed to decrypt password for user %d: %w", auth.ID, err)
		}
		auth.Password = decrypted
	}
	// If not encrypted, leave as-is (will be encrypted on next update)

	return nil
}

// encryptSpotifyAuthHook encrypts the access_token and refresh_token fields before saving to database
// and decrypts them in the returned entity
func encryptSpotifyAuthHook(encryptor *crypto.Encryptor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return hook.SpotifyAuthFunc(func(ctx context.Context, m *ent.SpotifyAuthMutation) (ent.Value, error) {
			// Remember original tokens for decryption after save
			var originalAccessToken, originalRefreshToken string

			// Encrypt access_token if being set
			if accessToken, exists := m.AccessToken(); exists {
				originalAccessToken = accessToken
				// Check if already encrypted (for idempotency)
				if !crypto.IsEncrypted(accessToken) {
					encrypted, err := encryptor.Encrypt(accessToken)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt access_token: %w", err)
					}
					m.SetAccessToken(encrypted)
				}
			}

			// Encrypt refresh_token if being set
			if refreshToken, exists := m.RefreshToken(); exists {
				originalRefreshToken = refreshToken
				// Check if already encrypted (for idempotency)
				if !crypto.IsEncrypted(refreshToken) {
					encrypted, err := encryptor.Encrypt(refreshToken)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt refresh_token: %w", err)
					}
					m.SetRefreshToken(encrypted)
				}
			}

			// Execute the mutation
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}

			// Decrypt the tokens in the returned entity
			if auth, ok := value.(*ent.SpotifyAuth); ok {
				if originalAccessToken != "" {
					auth.AccessToken = originalAccessToken
				}
				if originalRefreshToken != "" {
					auth.RefreshToken = originalRefreshToken
				}
			}

			return value, nil
		})
	}
}

// decryptSpotifyAuthInterceptor decrypts access_token and refresh_token fields after loading from database
func decryptSpotifyAuthInterceptor(encryptor *crypto.Encryptor) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			// Execute the query
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			// Decrypt tokens in the results
			switch result := v.(type) {
			case *ent.SpotifyAuth:
				if err := decryptSpotifyAuth(encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.SpotifyAuth:
				for _, auth := range result {
					if err := decryptSpotifyAuth(encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptSpotifyAuth decrypts the access_token and refresh_token fields in a SpotifyAuth entity
func decryptSpotifyAuth(encryptor *crypto.Encryptor, auth *ent.SpotifyAuth) error {
	if auth == nil {
		return nil
	}

	// Decrypt access_token if encrypted (backward compatibility)
	if auth.AccessToken != "" && crypto.IsEncrypted(auth.AccessToken) {
		decrypted, err := encryptor.Decrypt(auth.AccessToken)
		if err != nil {
			return fmt.Errorf("failed to decrypt access_token for user %d: %w", auth.ID, err)
		}
		auth.AccessToken = decrypted
	}

	// Decrypt refresh_token if encrypted (backward compatibility)
	if auth.RefreshToken != "" && crypto.IsEncrypted(auth.RefreshToken) {
		decrypted, err := encryptor.Decrypt(auth.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to decrypt refresh_token for user %d: %w", auth.ID, err)
		}
		auth.RefreshToken = decrypted
	}

	// If not encrypted, leave as-is (will be encrypted on next update)
	return nil
}

// encryptLastFMAuthHook encrypts the session_key field before saving to database
// and decrypts it in the returned entity
func encryptLastFMAuthHook(encryptor *crypto.Encryptor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return hook.LastFMAuthFunc(func(ctx context.Context, m *ent.LastFMAuthMutation) (ent.Value, error) {
			// Remember original session_key for decryption after save
			var originalSessionKey string

			// Encrypt session_key if being set
			if sessionKey, exists := m.SessionKey(); exists {
				originalSessionKey = sessionKey
				// Check if already encrypted (for idempotency)
				if !crypto.IsEncrypted(sessionKey) {
					encrypted, err := encryptor.Encrypt(sessionKey)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt session_key: %w", err)
					}
					m.SetSessionKey(encrypted)
				}
			}

			// Execute the mutation
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}

			// Decrypt the session_key in the returned entity
			if auth, ok := value.(*ent.LastFMAuth); ok && originalSessionKey != "" {
				auth.SessionKey = originalSessionKey
			}

			return value, nil
		})
	}
}

// decryptLastFMAuthInterceptor decrypts session_key field after loading from database
func decryptLastFMAuthInterceptor(encryptor *crypto.Encryptor) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			// Execute the query
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			// Decrypt session_key in the results
			switch result := v.(type) {
			case *ent.LastFMAuth:
				if err := decryptLastFMAuth(encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.LastFMAuth:
				for _, auth := range result {
					if err := decryptLastFMAuth(encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptLastFMAuth decrypts the session_key field in a LastFMAuth entity
func decryptLastFMAuth(encryptor *crypto.Encryptor, auth *ent.LastFMAuth) error {
	if auth == nil || auth.SessionKey == "" {
		return nil
	}

	// Check if session_key is encrypted (backward compatibility)
	if crypto.IsEncrypted(auth.SessionKey) {
		decrypted, err := encryptor.Decrypt(auth.SessionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt session_key for user %d: %w", auth.ID, err)
		}
		auth.SessionKey = decrypted
	}
	// If not encrypted, leave as-is (will be encrypted on next update)

	return nil
}
