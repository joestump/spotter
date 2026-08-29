// Governing: ADR-0005 (Navidrome auth), ADR-0006 (AES-256-GCM encryption), SPEC user-authentication (Last.fm OAuth flow)
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
	"spotter/internal/providers/lastfm"
)

const (
	lastfmStateCookie = "lastfm_oauth_state"
	lastfmStateTTL    = 10 * time.Minute
)

// LastFMLogin initiates the Last.fm authentication flow.
// Governing: ADR-0005 (Navidrome primary identity), ADR-0006 (AES-256-GCM), ADR-0007 (event bus), SPEC user-authentication REQ "LASTFM-001" through "LASTFM-004"
func (h *Handler) LastFMLogin(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		h.Logger.Error("Last.fm login: no user in session",
			"path", r.URL.Path,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_required", http.StatusSeeOther)
		return
	}

	// Check if Last.fm is configured
	if h.Config.LastFM.APIKey == "" {
		h.Logger.Error("Last.fm API key not configured")
		http.Error(w, "Last.fm integration is not configured", http.StatusServiceUnavailable)
		return
	}

	// Generate state for session tracking (Last.fm doesn't use it for CSRF, but we use it for session recovery)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		h.Logger.Error("failed to generate OAuth state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	state := base64.URLEncoding.EncodeToString(b)

	// Encrypt user ID to store in cookie for session recovery
	encryptedUserID, err := h.Encryptor.EncryptInt(u.ID)
	if err != nil {
		h.Logger.Error("failed to encrypt user ID for OAuth state", "error", err, "username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Store state and encrypted user ID in cookie (format: "state:encrypted_user_id")
	stateWithSession := fmt.Sprintf("%s:%s", state, encryptedUserID)
	http.SetCookie(w, &http.Cookie{
		Name:     lastfmStateCookie,
		Value:    stateWithSession,
		Path:     "/",
		HttpOnly: true,
		// Governing: SPEC user-authentication REQ "OAuth Secure Cookie Flag"
		Secure:   h.Config.Security.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(lastfmStateTTL),
	})

	// Create authenticator and get auth URL (Last.fm doesn't use state parameter)
	authFactory := lastfm.NewAuthenticator(h.Logger, h.Config)
	authenticator := authFactory()
	authURL := authenticator.GetAuthURL("")

	h.Logger.Info("redirecting user to Last.fm Auth", "username", u.Username, "user_id", u.ID)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// LastFMCallback handles the callback from Last.fm.
// Governing: ADR-0005, ADR-0006 (AES-256-GCM), ADR-0007 (event bus), SPEC user-authentication REQ "LASTFM-005", REQ "LASTFM-006"
func (h *Handler) LastFMCallback(w http.ResponseWriter, r *http.Request) {
	// Get authorization token
	token := r.URL.Query().Get("token")
	if token == "" {
		h.Logger.Warn("Last.fm callback: missing authorization token", "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=missing_token", http.StatusSeeOther)
		return
	}

	// Get session state from cookie
	stateCookie, err := r.Cookie(lastfmStateCookie)
	if err != nil {
		h.Logger.Warn("Last.fm callback: missing OAuth state cookie", "error", err, "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	// Parse state cookie to extract encrypted user ID (format: "state:encrypted_user_id").
	//
	// Split on the FIRST colon, for the same reason as the Spotify handler: the
	// encrypted half is "enc:v1:<ciphertext>" and contains colons. Scanning
	// backwards dropped the marker, and this path only kept working because
	// Decrypt falls back to treating unmarked input as legacy base64. That
	// fallback is not something to rely on.
	_, encryptedUserID, found := strings.Cut(stateCookie.Value, ":")
	if !found {
		h.Logger.Error("Last.fm callback: invalid state format (missing colon)", "remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return
	}

	// Decrypt user ID from state
	userID, err := h.Encryptor.DecryptInt(encryptedUserID)
	if err != nil {
		h.Logger.Error("Last.fm callback: failed to decrypt user ID from state",
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
		h.Logger.Error("Last.fm callback: failed to load user from database",
			"error", err,
			"user_id", userID,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	// Clear the state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     lastfmStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(-time.Hour),
	})

	// Exchange token for session
	authFactory := lastfm.NewAuthenticator(h.Logger, h.Config)
	authenticator := authFactory()
	authResult, err := authenticator.ExchangeCode(r.Context(), token)
	if err != nil {
		h.Logger.Error("failed to exchange Last.fm token", "error", err, "username", u.Username)
		http.Redirect(w, r, "/preferences/providers?error=exchange_failed", http.StatusSeeOther)
		return
	}

	// Check if user already has Last.fm auth (update vs create)
	u, err = h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithLastfmAuth().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if u.Edges.LastfmAuth != nil {
		// Update existing auth
		_, err = h.Client.LastFMAuth.UpdateOneID(u.Edges.LastfmAuth.ID).
			SetSessionKey(authResult.AccessToken). // AuthResult stores session key in AccessToken field
			SetUsername(authResult.DisplayName).
			Save(r.Context())
	} else {
		// Create new auth
		_, err = h.Client.LastFMAuth.Create().
			SetUser(u).
			SetSessionKey(authResult.AccessToken).
			SetUsername(authResult.DisplayName).
			Save(r.Context())
	}

	if err != nil {
		h.Logger.Error("failed to save Last.fm auth", "error", err, "username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Governing: SPEC-0015 REQ "Cooldown Reset on Recovery" — Provider reconnected via OAuth
	if h.Notifier != nil {
		if err := h.Notifier.ClearCooldown(r.Context(), u.ID, "lastfm"); err != nil {
			h.Logger.Error("failed to clear lastfm notification cooldown", "error", err)
		}
	}

	h.Logger.Info("successfully connected Last.fm account",
		"username", u.Username,
		"lastfm_username", authResult.DisplayName)

	// Send notification
	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Last.fm Connected",
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

		h.Logger.Info("starting Last.fm sync after auth", "username", username)

		// Send notification that sync is starting
		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Syncing Last.fm",
				Message:  "Fetching your recent listens...",
				IconType: "info",
			},
		})

		if err := h.Syncer.Sync(ctx, syncUser); err != nil {
			h.Logger.Error("Last.fm sync failed", "error", err, "username", username)
			h.Bus.Publish(userID, events.Event{
				Type: events.EventTypeNotification,
				Payload: events.NotificationPayload{
					Title:    "Sync Failed",
					Message:  "Failed to sync Last.fm data. Please try again later.",
					IconType: "error",
				},
			})
		}
	}(u.ID, u.Username)

	http.Redirect(w, r, "/preferences/providers", http.StatusSeeOther)
}
