package database

import (
	"context"
	"fmt"
	"log/slog"

	"spotter/ent"
	"spotter/ent/hook"
	"spotter/ent/lastfmauth"
	"spotter/ent/navidromeauth"
	"spotter/ent/spotifyauth"
	"spotter/internal/crypto"
)

// selfHealCtxKey marks a context as belonging to an interceptor self-heal
// write-back, whose value is already marked ciphertext and must be stored
// as-is. Every other mutation carries user input and is ALWAYS encrypted —
// even input that happens to start with the enc:v1: marker. Deciding by
// value shape in the write hooks would re-open the bricking bug this
// package exists to fix: a password literally starting with "enc:v1:"
// would be stored as plaintext and then fail GCM decryption on every read
// (issue #335).
type selfHealCtxKey struct{}

// withSelfHeal returns a context that tells the write hooks to store the
// (already marked-ciphertext) value without re-encrypting it.
func withSelfHeal(ctx context.Context) context.Context {
	return context.WithValue(ctx, selfHealCtxKey{}, true)
}

// isSelfHeal reports whether ctx belongs to an interceptor self-heal write-back.
func isSelfHeal(ctx context.Context) bool {
	v, _ := ctx.Value(selfHealCtxKey{}).(bool)
	return v
}

// RegisterEncryptionHooks registers hooks to encrypt/decrypt sensitive data
// Governing: ADR-0006 (application-layer AES-256-GCM encryption via Ent hooks)
func RegisterEncryptionHooks(client *ent.Client, encryptor *crypto.Encryptor) {
	// Hook for encrypting password on NavidromeAuth create/update
	client.NavidromeAuth.Use(encryptPasswordHook(encryptor))

	// Hook for decrypting password on NavidromeAuth query
	client.NavidromeAuth.Intercept(decryptPasswordInterceptor(client, encryptor))

	// Hook for encrypting tokens on SpotifyAuth create/update
	client.SpotifyAuth.Use(encryptSpotifyAuthHook(encryptor))

	// Hook for decrypting tokens on SpotifyAuth query
	client.SpotifyAuth.Intercept(decryptSpotifyAuthInterceptor(client, encryptor))

	// Hook for encrypting session key on LastFMAuth create/update
	client.LastFMAuth.Use(encryptLastFMAuthHook(encryptor))

	// Hook for decrypting session key on LastFMAuth query
	client.LastFMAuth.Intercept(decryptLastFMAuthInterceptor(client, encryptor))
}

// resolveEncryptedField returns the plaintext for a stored credential value
// and, when the stored form is legacy, a marked ciphertext to write back
// (self-heal). Resolution order:
//
//  1. Explicit marker (enc:v1:) → decrypt; a failure here is a real error
//     (wrong key or corrupted ciphertext) and is returned to the caller.
//  2. Legacy ciphertext shape (bare base64, >= 29 decoded bytes) → attempt
//     decryption. Success means it is pre-marker ciphertext; GCM failure
//     means it is plaintext that merely looks like ciphertext (e.g. a
//     40-char alphanumeric password) — reported via treatedAsPlaintext so
//     callers can log it, since it is also the only signal an operator gets
//     when the encryption key is wrong for legacy rows. Either way the row
//     self-heals: the plaintext is re-encrypted with the marker and returned
//     as healed. This path NEVER returns an error — previously it bricked
//     the auth row by failing the whole query (issue #335).
//  3. Anything else is plaintext from before encryption was enabled; it is
//     left as-is and will be encrypted on the next write.
//
// Governing: ADR-0006
func resolveEncryptedField(encryptor *crypto.Encryptor, stored string) (plaintext, healed string, treatedAsPlaintext bool, err error) {
	if stored == "" {
		return "", "", false, nil
	}

	if crypto.IsEncrypted(stored) {
		decrypted, err := encryptor.Decrypt(stored)
		if err != nil {
			// Marked values are always ciphertext (the write hooks encrypt
			// unconditionally), so this can only be a wrong key or corruption.
			return "", "", false, err
		}
		return decrypted, "", false, nil
	}

	if crypto.LooksLikeLegacyCiphertext(stored) {
		if decrypted, err := encryptor.Decrypt(stored); err == nil {
			// Legacy ciphertext: migrate to the marked format.
			if reencrypted, err := encryptor.Encrypt(decrypted); err == nil {
				return decrypted, reencrypted, false, nil
			}
			return decrypted, "", false, nil
		}
		// GCM failure: the value is plaintext that merely looks like
		// ciphertext. Treat it as plaintext and self-heal by encrypting
		// it with the marker so future reads are unambiguous.
		if reencrypted, err := encryptor.Encrypt(stored); err == nil {
			return stored, reencrypted, true, nil
		}
		return stored, "", true, nil
	}

	// Plaintext from before encryption was enabled (will be encrypted on next write)
	return stored, "", false, nil
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
				// ALWAYS encrypt user input — even input that happens to
				// start with the enc:v1: marker (issue #335). Only the
				// interceptor self-heal path, which writes back values that
				// are already marked ciphertext, may skip encryption.
				if !isSelfHeal(ctx) {
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
func decryptPasswordInterceptor(client *ent.Client, encryptor *crypto.Encryptor) ent.Interceptor {
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
				if err := decryptNavidromeAuth(ctx, client, encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.NavidromeAuth:
				for _, auth := range result {
					if err := decryptNavidromeAuth(ctx, client, encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptNavidromeAuth decrypts the password field in a NavidromeAuth entity,
// self-healing legacy-format rows to the marked ciphertext format.
func decryptNavidromeAuth(ctx context.Context, client *ent.Client, encryptor *crypto.Encryptor, auth *ent.NavidromeAuth) error {
	if auth == nil || auth.Password == "" {
		return nil
	}

	plaintext, healed, treatedAsPlaintext, err := resolveEncryptedField(encryptor, auth.Password)
	if err != nil {
		return fmt.Errorf("failed to decrypt password for user %d: %w", auth.ID, err)
	}
	if treatedAsPlaintext {
		slog.Warn("stored navidrome password matches legacy ciphertext shape but fails decryption; treating as plaintext and re-encrypting (wrong SPOTTER_SECURITY_ENCRYPTION_KEY?)",
			"navidrome_auth_id", auth.ID)
	}
	if healed != "" {
		// Best-effort self-heal: persist the marked ciphertext. The
		// self-heal context tells the write hook to store it as-is, and
		// the Where guard makes it a compare-and-swap so a concurrent
		// legitimate credential update is never clobbered. Failures are
		// ignored — the read must never break; the next read retries.
		_, _ = client.NavidromeAuth.Update().
			Where(navidromeauth.ID(auth.ID), navidromeauth.Password(auth.Password)).
			SetPassword(healed).
			Save(withSelfHeal(ctx))
	}
	auth.Password = plaintext

	return nil
}

// encryptSpotifyAuthHook encrypts the access_token and refresh_token fields before saving to database
// and decrypts them in the returned entity
func encryptSpotifyAuthHook(encryptor *crypto.Encryptor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return hook.SpotifyAuthFunc(func(ctx context.Context, m *ent.SpotifyAuthMutation) (ent.Value, error) {
			// Remember original tokens for decryption after save
			var originalAccessToken, originalRefreshToken string

			// Encrypt access_token if being set. ALWAYS encrypt user input —
			// even input starting with the enc:v1: marker (issue #335); only
			// the interceptor self-heal path may skip encryption.
			if accessToken, exists := m.AccessToken(); exists {
				originalAccessToken = accessToken
				if !isSelfHeal(ctx) {
					encrypted, err := encryptor.Encrypt(accessToken)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt access_token: %w", err)
					}
					m.SetAccessToken(encrypted)
				}
			}

			// Encrypt refresh_token if being set (same self-heal rule as above)
			if refreshToken, exists := m.RefreshToken(); exists {
				originalRefreshToken = refreshToken
				if !isSelfHeal(ctx) {
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
func decryptSpotifyAuthInterceptor(client *ent.Client, encryptor *crypto.Encryptor) ent.Interceptor {
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
				if err := decryptSpotifyAuth(ctx, client, encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.SpotifyAuth:
				for _, auth := range result {
					if err := decryptSpotifyAuth(ctx, client, encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptSpotifyAuth decrypts the access_token and refresh_token fields in a
// SpotifyAuth entity, self-healing legacy-format values to the marked
// ciphertext format.
func decryptSpotifyAuth(ctx context.Context, client *ent.Client, encryptor *crypto.Encryptor, auth *ent.SpotifyAuth) error {
	if auth == nil {
		return nil
	}

	accessPlain, accessHealed, accessAsPlaintext, err := resolveEncryptedField(encryptor, auth.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to decrypt access_token for user %d: %w", auth.ID, err)
	}
	if accessAsPlaintext {
		slog.Warn("stored spotify access_token matches legacy ciphertext shape but fails decryption; treating as plaintext and re-encrypting (wrong SPOTTER_SECURITY_ENCRYPTION_KEY?)",
			"spotify_auth_id", auth.ID)
	}

	refreshPlain, refreshHealed, refreshAsPlaintext, err := resolveEncryptedField(encryptor, auth.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to decrypt refresh_token for user %d: %w", auth.ID, err)
	}
	if refreshAsPlaintext {
		slog.Warn("stored spotify refresh_token matches legacy ciphertext shape but fails decryption; treating as plaintext and re-encrypting (wrong SPOTTER_SECURITY_ENCRYPTION_KEY?)",
			"spotify_auth_id", auth.ID)
	}

	// Best-effort self-heal: persist the marked ciphertexts. The self-heal
	// context tells the write hook to store them as-is, and the Where guards
	// make each write a compare-and-swap so a concurrent legitimate
	// credential update is never clobbered. Failures are ignored — the read
	// must never break; the next read retries.
	if accessHealed != "" {
		_, _ = client.SpotifyAuth.Update().
			Where(spotifyauth.ID(auth.ID), spotifyauth.AccessToken(auth.AccessToken)).
			SetAccessToken(accessHealed).
			Save(withSelfHeal(ctx))
	}
	if refreshHealed != "" {
		_, _ = client.SpotifyAuth.Update().
			Where(spotifyauth.ID(auth.ID), spotifyauth.RefreshToken(auth.RefreshToken)).
			SetRefreshToken(refreshHealed).
			Save(withSelfHeal(ctx))
	}

	auth.AccessToken = accessPlain
	auth.RefreshToken = refreshPlain

	return nil
}

// encryptLastFMAuthHook encrypts the session_key field before saving to database
// and decrypts it in the returned entity
func encryptLastFMAuthHook(encryptor *crypto.Encryptor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return hook.LastFMAuthFunc(func(ctx context.Context, m *ent.LastFMAuthMutation) (ent.Value, error) {
			// Remember original session_key for decryption after save
			var originalSessionKey string

			// Encrypt session_key if being set. ALWAYS encrypt user input —
			// even input starting with the enc:v1: marker (issue #335); only
			// the interceptor self-heal path may skip encryption.
			if sessionKey, exists := m.SessionKey(); exists {
				originalSessionKey = sessionKey
				if !isSelfHeal(ctx) {
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
func decryptLastFMAuthInterceptor(client *ent.Client, encryptor *crypto.Encryptor) ent.Interceptor {
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
				if err := decryptLastFMAuth(ctx, client, encryptor, result); err != nil {
					return nil, err
				}
			case []*ent.LastFMAuth:
				for _, auth := range result {
					if err := decryptLastFMAuth(ctx, client, encryptor, auth); err != nil {
						return nil, err
					}
				}
			}

			return v, nil
		})
	})
}

// decryptLastFMAuth decrypts the session_key field in a LastFMAuth entity,
// self-healing legacy-format rows to the marked ciphertext format.
func decryptLastFMAuth(ctx context.Context, client *ent.Client, encryptor *crypto.Encryptor, auth *ent.LastFMAuth) error {
	if auth == nil || auth.SessionKey == "" {
		return nil
	}

	plaintext, healed, treatedAsPlaintext, err := resolveEncryptedField(encryptor, auth.SessionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt session_key for user %d: %w", auth.ID, err)
	}
	if treatedAsPlaintext {
		slog.Warn("stored lastfm session_key matches legacy ciphertext shape but fails decryption; treating as plaintext and re-encrypting (wrong SPOTTER_SECURITY_ENCRYPTION_KEY?)",
			"lastfm_auth_id", auth.ID)
	}
	if healed != "" {
		// Best-effort self-heal: persist the marked ciphertext. The
		// self-heal context tells the write hook to store it as-is, and
		// the Where guard makes it a compare-and-swap so a concurrent
		// legitimate credential update is never clobbered. Failures are
		// ignored — the read must never break; the next read retries.
		_, _ = client.LastFMAuth.Update().
			Where(lastfmauth.ID(auth.ID), lastfmauth.SessionKey(auth.SessionKey)).
			SetSessionKey(healed).
			Save(withSelfHeal(ctx))
	}
	auth.SessionKey = plaintext

	return nil
}
