// Governing: ADR-0005 (Navidrome auth), ADR-0006 (AES-256-GCM encryption), SPEC user-authentication (Spotify OAuth flow)
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"spotter/ent/user"
	"spotter/internal/events"
	"spotter/internal/providers/spotify"
)

const (
	spotifyStateCookie = "spotify_oauth_state"
	spotifyStateTTL    = 10 * time.Minute
)

// generateState creates a random state string for OAuth CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// SpotifyLogin initiates the Spotify OAuth flow.
// Governing: ADR-0005 (Navidrome primary identity), ADR-0006 (AES-256-GCM), ADR-0007 (event bus), SPEC user-authentication REQ "SPOTIFY-001" through "SPOTIFY-006"
func (h *Handler) SpotifyLogin(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		h.Logger.Error("Spotify login: no user in session",
			"path", r.URL.Path,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_required", http.StatusSeeOther)
		return
	}

	// Check if Spotify is configured
	if h.Config.Spotify.ClientID == "" || h.Config.Spotify.ClientSecret == "" {
		h.Logger.Error("Spotify OAuth not configured")
		http.Error(w, "Spotify integration is not configured", http.StatusServiceUnavailable)
		return
	}

	// Generate state for CSRF protection
	state, err := generateState()
	if err != nil {
		h.Logger.Error("failed to generate OAuth state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Encrypt user ID to embed in state for session recovery
	encryptedUserID, err := h.Encryptor.EncryptInt(u.ID)
	if err != nil {
		h.Logger.Error("failed to encrypt user ID for OAuth state", "error", err, "username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Combine state and encrypted user ID (format: "state:encrypted_user_id")
	stateWithSession := fmt.Sprintf("%s:%s", state, encryptedUserID)

	// Store state in cookie for validation (without encrypted user ID)
	http.SetCookie(w, &http.Cookie{
		Name:     spotifyStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		// Governing: SPEC user-authentication REQ "OAuth Secure Cookie Flag"
		Secure:   h.Config.Security.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(spotifyStateTTL),
	})

	// Create authenticator and get auth URL with state containing session info
	authFactory := spotify.NewAuthenticator(h.Logger, h.Config)
	authenticator := authFactory()
	authURL := authenticator.GetAuthURL(stateWithSession)

	h.Logger.Info("redirecting user to Spotify OAuth", "username", u.Username, "user_id", u.ID)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// SpotifyCallback handles the OAuth callback from Spotify.
// Governing: ADR-0005, ADR-0006 (AES-256-GCM), ADR-0007 (event bus), SPEC user-authentication REQ "SPOTIFY-007", REQ "SPOTIFY-008"
func (h *Handler) SpotifyCallback(w http.ResponseWriter, r *http.Request) {
	// Check for error from Spotify
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.Logger.Warn("Spotify OAuth error", "error", errParam, "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/preferences/providers?error=spotify_denied", http.StatusSeeOther)
		return
	}

	// Get state parameter from URL (format: "state:encrypted_user_id")
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		h.Logger.Error("Spotify callback: missing state parameter", "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return
	}

	// Parse state to extract CSRF token and encrypted user ID.
	//
	// Split on the FIRST colon, not the last. The encrypted half is produced by
	// Encryptor.EncryptInt, which returns "enc:v1:<ciphertext>" -- it contains
	// colons of its own. Scanning backwards therefore split inside the marker,
	// yielding csrfState="<csrf>:enc:v1" and an encryptedUserID missing its
	// marker, so every Spotify callback failed the CSRF comparison with
	// error=invalid_state. The CSRF token is URL-safe base64 and never contains
	// a colon, so the first colon is always the real delimiter.
	csrfState, encryptedUserID, found := strings.Cut(stateParam, ":")
	if !found {
		h.Logger.Error("Spotify callback: invalid state format (missing colon)", "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return
	}

	// Verify CSRF state against cookie
	stateCookie, err := r.Cookie(spotifyStateCookie)
	if err != nil {
		h.Logger.Warn("Spotify callback: missing OAuth state cookie", "error", err, "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	if csrfState != stateCookie.Value {
		h.Logger.Warn("Spotify callback: OAuth state mismatch",
			"expected", stateCookie.Value,
			"got", csrfState,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return
	}

	// Decrypt user ID from state
	userID, err := h.Encryptor.DecryptInt(encryptedUserID)
	if err != nil {
		h.Logger.Error("Spotify callback: failed to decrypt user ID from state",
			"error", err,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	// Load user from database
	u, err := h.Client.User.Query().
		Where(user.ID(userID)).
		Only(r.Context())
	if err != nil {
		h.Logger.Error("Spotify callback: failed to load user from database",
			"error", err,
			"user_id", userID,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	// Clear the state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     spotifyStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(-time.Hour),
	})

	// Get authorization code
	code := r.URL.Query().Get("code")
	if code == "" {
		h.Logger.Warn("missing authorization code in callback")
		http.Redirect(w, r, "/preferences/providers?error=missing_code", http.StatusSeeOther)
		return
	}

	// Exchange code for tokens
	authFactory := spotify.NewAuthenticator(h.Logger, h.Config)
	authenticator := authFactory()
	authResult, err := authenticator.ExchangeCode(r.Context(), code)
	if err != nil {
		h.Logger.Error("failed to exchange Spotify code", "error", err, "username", u.Username)
		http.Redirect(w, r, "/preferences/providers?error=exchange_failed", http.StatusSeeOther)
		return
	}

	// Check if user already has Spotify auth (update vs create)
	u, err = h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if u.Edges.SpotifyAuth != nil {
		// Update existing auth
		_, err = h.Client.SpotifyAuth.UpdateOneID(u.Edges.SpotifyAuth.ID).
			SetAccessToken(authResult.AccessToken).
			SetRefreshToken(authResult.RefreshToken).
			SetExpiry(authResult.Expiry).
			SetDisplayName(authResult.DisplayName).
			Save(r.Context())
	} else {
		// Create new auth
		_, err = h.Client.SpotifyAuth.Create().
			SetUser(u).
			SetAccessToken(authResult.AccessToken).
			SetRefreshToken(authResult.RefreshToken).
			SetExpiry(authResult.Expiry).
			SetDisplayName(authResult.DisplayName).
			Save(r.Context())
	}

	if err != nil {
		h.Logger.Error("failed to save Spotify auth", "error", err, "username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Governing: SPEC-0015 REQ "Cooldown Reset on Recovery" — Provider reconnected via OAuth
	if h.Notifier != nil {
		if err := h.Notifier.ClearCooldown(r.Context(), u.ID, "spotify"); err != nil {
			h.Logger.Error("failed to clear spotify notification cooldown", "error", err)
		}
	}

	h.Logger.Info("successfully connected Spotify account",
		"username", u.Username,
		"spotify_display_name", authResult.DisplayName)

	// Send notification
	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Spotify Connected",
			Message:  fmt.Sprintf("Successfully connected as %s", authResult.DisplayName),
			IconType: "success",
		},
	})

	// Trigger sync in background
	go func(userID int, username string) {
		ctx := context.Background()

		// Re-fetch user with all auth edges for sync
		syncUser, err := h.Client.User.Query().
			Where(user.ID(userID)).
			WithSpotifyAuth().
			WithNavidromeAuth().
			WithLastfmAuth().
			Only(ctx)
		if err != nil {
			h.Logger.Error("failed to fetch user for sync", "error", err, "username", username)
			return
		}

		h.Logger.Info("starting Spotify sync after OAuth", "username", username)

		// Send notification that sync is starting
		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Syncing Spotify",
				Message:  "Fetching your recent listens and playlists...",
				IconType: "info",
			},
		})

		if err := h.Syncer.Sync(ctx, syncUser); err != nil {
			h.Logger.Error("Spotify sync failed", "error", err, "username", username)
			h.Bus.Publish(userID, events.Event{
				Type: events.EventTypeNotification,
				Payload: events.NotificationPayload{
					Title:    "Sync Failed",
					Message:  "Failed to sync Spotify data. Please try again later.",
					IconType: "error",
				},
			})
		}
	}(u.ID, u.Username)

	http.Redirect(w, r, "/preferences/providers", http.StatusSeeOther)
}
