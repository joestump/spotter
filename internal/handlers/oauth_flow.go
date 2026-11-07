// Governing: ADR-0005 (Navidrome primary identity), ADR-0006 (AES-256-GCM), ADR-0007 (event bus),
// SPEC user-authentication (OAuth login redirect and callback flow)
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/providers"
)

// oauthFlow is one provider's OAuth flow, expressed as the parts that differ from
// the shared implementation below: where the grant arrives, whether the provider
// echoes the CSRF state back, how its auth row is written, and the copy the user
// sees. Declaring one is what adding a provider means; the flow itself is written
// once.
//
// Governing: ADR-0016 (pluggable provider factory)
type oauthFlow struct {
	// providerName is the name shown to the user ("Spotify", "Last.fm").
	providerName string
	// providerKey is the provider key the notification cooldown is filed under.
	providerKey string
	// stateCookie names the cookie that carries the CSRF state and/or the
	// encrypted user ID.
	stateCookie string
	// stateTTL is how long that cookie stays valid.
	stateTTL time.Duration
	// stateParam names the query key the provider echoes "<csrf>:<encryptedUserID>"
	// back in. Empty means the provider does not carry state, so the cookie alone
	// identifies the session and there is nothing to compare.
	stateParam string
	// grantParam names the query key carrying the authorization code or token.
	grantParam string
	// errorParam, when set, names the query key that carries a provider-side denial.
	errorParam string
	// deniedRedirect is where a provider-side denial sends the user.
	deniedRedirect string
	// grantMissingRedirect is where a callback that carries no grant sends them.
	grantMissingRedirect string
	// exchangeFailedRedirect is where a failed code/token exchange sends them.
	exchangeFailedRedirect string
	// syncMessage tells the user what the post-connect sync is about to fetch.
	syncMessage string
	// configured reports whether this installation can run the flow at all.
	configured func(cfg *config.Config) bool
	// newAuthenticator builds the provider's authenticator for this request.
	newAuthenticator func(logger *slog.Logger, cfg *config.Config) providers.AuthenticatorFactory
	// saveAuth writes the result to this provider's own auth row.
	saveAuth func(ctx context.Context, client *ent.Client, u *ent.User, result *providers.AuthResult) error
}

// echoesState reports whether the provider carries the CSRF state through its
// side of the round trip, which is what makes the cookie comparable.
func (f oauthFlow) echoesState() bool { return f.stateParam != "" }

// generateState creates a random state string for OAuth CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// startOAuthLogin runs the part every provider's login shares: prove there is a
// session and a configured provider, bind the user to the flow through an
// encrypted user ID, and hand the user off to the provider.
func (h *Handler) startOAuthLogin(w http.ResponseWriter, r *http.Request, flow oauthFlow) {
	u := h.GetUser(r.Context())
	if u == nil {
		h.Logger.Error("provider login: no user in session",
			"provider", flow.providerName,
			"path", r.URL.Path,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_required", http.StatusSeeOther)
		return
	}

	if !flow.configured(h.Config) {
		h.Logger.Error("provider OAuth not configured", "provider", flow.providerName)
		http.Error(w, flow.providerName+" integration is not configured", http.StatusServiceUnavailable)
		return
	}

	// The user ID travels encrypted so the callback can recover the session
	// without trusting anything the provider returns.
	encryptedUserID, err := h.Encryptor.EncryptInt(u.ID)
	if err != nil {
		h.Logger.Error("failed to encrypt user ID for OAuth state", "error", err, "username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// A provider that echoes state gets the CSRF token in the cookie and the pair
	// in the URL it redirects with. A provider that does not gets only what it has
	// to bring back, so nothing is generated that no one compares.
	cookieValue := encryptedUserID
	urlState := ""
	if flow.echoesState() {
		state, err := generateState()
		if err != nil {
			h.Logger.Error("failed to generate OAuth state", "error", err, "provider", flow.providerName)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		cookieValue = state
		urlState = fmt.Sprintf("%s:%s", state, encryptedUserID)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     flow.stateCookie,
		Value:    cookieValue,
		Path:     "/",
		HttpOnly: true,
		// Governing: SPEC user-authentication REQ "OAuth Secure Cookie Flag"
		Secure:   h.Config.Security.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(flow.stateTTL),
	})

	authenticator := flow.newAuthenticator(h.Logger, h.Config)()

	h.Logger.Info("redirecting user to provider OAuth",
		"provider", flow.providerName,
		"username", u.Username,
		"user_id", u.ID)
	http.Redirect(w, r, authenticator.GetAuthURL(urlState), http.StatusSeeOther)
}

// completeOAuthCallback runs the part every provider's callback shares: reject a
// provider-side denial, tie the request to the session that started it, exchange
// the grant, hand the result to the provider's own auth writer, and sync.
//
// The state is verified before the grant is looked at, so a request that is not
// tied to this browser's session never reaches the exchange.
func (h *Handler) completeOAuthCallback(w http.ResponseWriter, r *http.Request, flow oauthFlow) {
	if flow.errorParam != "" {
		if denied := r.URL.Query().Get(flow.errorParam); denied != "" {
			h.Logger.Warn("provider OAuth error", "provider", flow.providerName, "error", denied, "remote_ip", r.RemoteAddr)
			http.Redirect(w, r, flow.deniedRedirect, http.StatusSeeOther)
			return
		}
	}

	encryptedUserID, ok := h.resolveOAuthSession(w, r, flow)
	if !ok {
		return
	}

	userID, err := h.Encryptor.DecryptInt(encryptedUserID)
	if err != nil {
		h.Logger.Error("callback: failed to decrypt user ID from state",
			"provider", flow.providerName,
			"error", err,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	u, err := h.Client.User.Query().
		Where(user.ID(userID)).
		Only(r.Context())
	if err != nil {
		h.Logger.Error("callback: failed to load user from database",
			"provider", flow.providerName,
			"error", err,
			"user_id", userID,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return
	}

	// The state has done its job; it must not be replayable.
	http.SetCookie(w, &http.Cookie{
		Name:     flow.stateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(-time.Hour),
	})

	grant := r.URL.Query().Get(flow.grantParam)
	if grant == "" {
		h.Logger.Warn("callback: missing authorization grant",
			"provider", flow.providerName,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, flow.grantMissingRedirect, http.StatusSeeOther)
		return
	}

	authenticator := flow.newAuthenticator(h.Logger, h.Config)()
	result, err := authenticator.ExchangeCode(r.Context(), grant)
	if err != nil {
		h.Logger.Error("failed to exchange provider grant",
			"provider", flow.providerName,
			"error", err,
			"username", u.Username)
		http.Redirect(w, r, flow.exchangeFailedRedirect, http.StatusSeeOther)
		return
	}

	if err := flow.saveAuth(r.Context(), h.Client, u, result); err != nil {
		h.Logger.Error("failed to save provider auth",
			"provider", flow.providerName,
			"error", err,
			"username", u.Username)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Governing: SPEC-0015 REQ "Cooldown Reset on Recovery" — Provider reconnected via OAuth
	if h.Notifier != nil {
		if err := h.Notifier.ClearCooldown(r.Context(), u.ID, flow.providerKey); err != nil {
			h.Logger.Error("failed to clear provider notification cooldown",
				"provider", flow.providerKey,
				"error", err)
		}
	}

	h.Logger.Info("successfully connected provider account",
		"provider", flow.providerName,
		"username", u.Username,
		"provider_display_name", result.DisplayName)

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    fmt.Sprintf("%s Connected", flow.providerName),
			Message:  fmt.Sprintf("Successfully connected as %s", result.DisplayName),
			IconType: "success",
		},
	})

	go h.syncAfterConnect(u.ID, u.Username, flow)

	http.Redirect(w, r, "/preferences/providers", http.StatusSeeOther)
}

// resolveOAuthSession recovers the encrypted user ID this browser was sent with,
// and for a provider that echoes state proves the request came from this browser.
// It answers false once it has sent the user somewhere else.
func (h *Handler) resolveOAuthSession(w http.ResponseWriter, r *http.Request, flow oauthFlow) (string, bool) {
	stateCookie, err := r.Cookie(flow.stateCookie)
	if err != nil {
		h.Logger.Warn("callback: missing OAuth state cookie",
			"provider", flow.providerName,
			"error", err,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=session_expired", http.StatusSeeOther)
		return "", false
	}

	if !flow.echoesState() {
		// The cookie carries the encrypted user ID. A cookie issued before the
		// unused state value was dropped prefixes it with "<state>:", and the
		// marked ciphertext starts after that first colon, so both shapes work.
		if _, encPart, found := strings.Cut(stateCookie.Value, ":"); found && strings.HasPrefix(encPart, crypto.EncryptedMarker) {
			return encPart, true
		}
		return stateCookie.Value, true
	}

	// Split on the FIRST colon, not the last. The encrypted half is produced by
	// Encryptor.EncryptInt, which returns "enc:v1:<ciphertext>" and contains
	// colons of its own; scanning backwards splits inside that marker, so the
	// CSRF half never matches the cookie. The CSRF token is URL-safe base64 and
	// never contains a colon, so the first colon is always the real delimiter.
	stateParam := r.URL.Query().Get(flow.stateParam)
	if stateParam == "" {
		h.Logger.Error("callback: missing state parameter",
			"provider", flow.providerName,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return "", false
	}

	csrfState, encryptedUserID, found := strings.Cut(stateParam, ":")
	if !found {
		h.Logger.Error("callback: invalid state format (missing colon)",
			"provider", flow.providerName,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return "", false
	}

	if csrfState != stateCookie.Value {
		h.Logger.Warn("callback: OAuth state mismatch",
			"provider", flow.providerName,
			"expected", stateCookie.Value,
			"got", csrfState,
			"remote_ip", r.RemoteAddr)
		http.Redirect(w, r, "/auth/login?error=invalid_state", http.StatusSeeOther)
		return "", false
	}

	return encryptedUserID, true
}

// syncAfterConnect refreshes the user and syncs in the background, reporting
// through the event bus where it stands. The syncer loads the auth edges its own
// registered providers ask for, so nothing here names them.
func (h *Handler) syncAfterConnect(userID int, username string, flow oauthFlow) {
	ctx := context.Background()

	u, err := h.Client.User.Query().
		Where(user.ID(userID)).
		Only(ctx)
	if err != nil {
		h.Logger.Error("failed to fetch user for sync", "error", err, "username", username)
		return
	}

	h.Logger.Info("starting provider sync after OAuth", "provider", flow.providerName, "username", username)

	h.Bus.Publish(userID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    fmt.Sprintf("Syncing %s", flow.providerName),
			Message:  flow.syncMessage,
			IconType: "info",
		},
	})

	if err := h.Syncer.Sync(ctx, u); err != nil {
		h.Logger.Error("provider sync failed", "provider", flow.providerName, "error", err, "username", username)
		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Sync Failed",
				Message:  fmt.Sprintf("Failed to sync %s data. Please try again later.", flow.providerName),
				IconType: "error",
			},
		})
	}
}
