// Governing: ADR-0005 (Navidrome auth), ADR-0006 (AES-256-GCM encryption), ADR-0016 (pluggable provider flow),
// SPEC user-authentication (Last.fm OAuth flow)
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"spotter/ent"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/providers"
	"spotter/internal/providers/lastfm"
)

const (
	lastfmStateCookie = "lastfm_oauth_state"
	lastfmStateTTL    = 10 * time.Minute
)

// Governing: SPEC user-authentication REQ "LASTFM-005", REQ "LASTFM-006"
// Last.fm's protocol does not echo state, so this flow carries only the encrypted
// user ID in the cookie: the token is what the callback exchanges, and there is
// nothing else for the browser to prove.
var lastfmOAuthFlow = oauthFlow{
	providerName:           "Last.fm",
	providerKey:            string(providers.TypeLastFM),
	stateCookie:            lastfmStateCookie,
	stateTTL:               lastfmStateTTL,
	grantParam:             "token",
	grantMissingRedirect:   "/auth/login?error=missing_token",
	exchangeFailedRedirect: "/preferences/providers?error=exchange_failed",
	syncMessage:            "Fetching your recent listens...",
	configured: func(cfg *config.Config) bool {
		return cfg.LastFM.APIKey != ""
	},
	newAuthenticator: lastfm.NewAuthenticator,
	saveAuth:         saveLastFMAuth,
}

// LastFMLogin initiates the Last.fm authentication flow.
func (h *Handler) LastFMLogin(w http.ResponseWriter, r *http.Request) {
	h.startOAuthLogin(w, r, lastfmOAuthFlow)
}

// LastFMCallback handles the callback from Last.fm.
func (h *Handler) LastFMCallback(w http.ResponseWriter, r *http.Request) {
	h.completeOAuthCallback(w, r, lastfmOAuthFlow)
}

// saveLastFMAuth upserts this user's Last.fm auth row with the session key.
func saveLastFMAuth(ctx context.Context, client *ent.Client, u *ent.User, result *providers.AuthResult) error {
	withAuth, err := client.User.Query().
		Where(user.ID(u.ID)).
		WithLastfmAuth().
		Only(ctx)
	if err != nil {
		return fmt.Errorf("failed to load lastfm auth state: %w", err)
	}

	if withAuth.Edges.LastfmAuth != nil {
		_, err = client.LastFMAuth.UpdateOneID(withAuth.Edges.LastfmAuth.ID).
			SetSessionKey(result.AccessToken). // AuthResult stores the session key in the AccessToken field
			SetUsername(result.DisplayName).
			Save(ctx)
	} else {
		_, err = client.LastFMAuth.Create().
			SetUser(withAuth).
			SetSessionKey(result.AccessToken).
			SetUsername(result.DisplayName).
			Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to save lastfm auth: %w", err)
	}
	return nil
}
