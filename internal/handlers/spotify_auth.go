// Governing: ADR-0005 (Navidrome auth), ADR-0006 (AES-256-GCM encryption), ADR-0016 (pluggable provider flow),
// SPEC user-authentication (Spotify OAuth flow)
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
	"spotter/internal/providers/spotify"
)

const (
	spotifyStateCookie = "spotify_oauth_state"
	spotifyStateTTL    = 10 * time.Minute
)

// Governing: SPEC user-authentication REQ "SPOTIFY-001" through "SPOTIFY-008"
// Spotify echoes the state back, so its callback compares it against the cookie
// before touching the authorization code.
var spotifyOAuthFlow = oauthFlow{
	providerName:           "Spotify",
	providerKey:            string(providers.TypeSpotify),
	stateCookie:            spotifyStateCookie,
	stateTTL:               spotifyStateTTL,
	stateParam:             "state",
	grantParam:             "code",
	errorParam:             "error",
	deniedRedirect:         "/preferences/providers?error=spotify_denied",
	grantMissingRedirect:   "/preferences/providers?error=missing_code",
	exchangeFailedRedirect: "/preferences/providers?error=exchange_failed",
	syncMessage:            "Fetching your recent listens and playlists...",
	configured: func(cfg *config.Config) bool {
		return cfg.Spotify.ClientID != "" && cfg.Spotify.ClientSecret != ""
	},
	newAuthenticator: spotify.NewAuthenticator,
	saveAuth:         saveSpotifyAuth,
}

// SpotifyLogin initiates the Spotify OAuth flow.
func (h *Handler) SpotifyLogin(w http.ResponseWriter, r *http.Request) {
	h.startOAuthLogin(w, r, spotifyOAuthFlow)
}

// SpotifyCallback handles the OAuth callback from Spotify.
func (h *Handler) SpotifyCallback(w http.ResponseWriter, r *http.Request) {
	h.completeOAuthCallback(w, r, spotifyOAuthFlow)
}

// saveSpotifyAuth upserts this user's Spotify auth row with the exchanged tokens.
func saveSpotifyAuth(ctx context.Context, client *ent.Client, u *ent.User, result *providers.AuthResult) error {
	withAuth, err := client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		Only(ctx)
	if err != nil {
		return fmt.Errorf("failed to load spotify auth state: %w", err)
	}

	if withAuth.Edges.SpotifyAuth != nil {
		_, err = client.SpotifyAuth.UpdateOneID(withAuth.Edges.SpotifyAuth.ID).
			SetAccessToken(result.AccessToken).
			SetRefreshToken(result.RefreshToken).
			SetExpiry(result.Expiry).
			SetDisplayName(result.DisplayName).
			Save(ctx)
	} else {
		_, err = client.SpotifyAuth.Create().
			SetUser(withAuth).
			SetAccessToken(result.AccessToken).
			SetRefreshToken(result.RefreshToken).
			SetExpiry(result.Expiry).
			SetDisplayName(result.DisplayName).
			Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to save spotify auth: %w", err)
	}
	return nil
}
