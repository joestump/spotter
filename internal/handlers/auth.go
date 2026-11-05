// Governing: ADR-0005 (Navidrome primary identity), ADR-0006 (AES-256-GCM encryption), ADR-0002 (Chi router), SPEC user-authentication
package handlers

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"spotter/ent"
	"spotter/ent/user"
	"spotter/internal/vibes"
	"spotter/internal/views/auth"
)

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.GetUser(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	errorMsg := r.URL.Query().Get("error")
	h.Render(w, r, auth.Login(h.Config.Navidrome.BaseURL, errorMsg))
}

func (h *Handler) PostLogin(w http.ResponseWriter, r *http.Request) {
	// Governing: ADR-0005 (Navidrome primary identity), ADR-0006 (AES-256-GCM), ADR-0002 (Chi router), SPEC user-authentication REQ "Navidrome Login Flow"
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" || password == "" {
		http.Error(w, "Username and password required", http.StatusBadRequest)
		return
	}

	// Validate input lengths to prevent abuse
	if err := ValidateMaxLength("username", username, MaxNameLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	// 1. Authenticate against Navidrome
	if err := h.authenticateNavidrome(username, password); err != nil {
		h.Logger.Error("Navidrome authentication failed", "error", err)
		w.WriteHeader(http.StatusUnauthorized)
		h.Render(w, r, auth.Login(h.Config.Navidrome.BaseURL, "Invalid username or password. Please try again."))
		return
	}

	// 2. Create or Update User
	u, err := h.Client.User.Query().
		Where(user.Username(username)).
		WithNavidromeAuth().
		Only(r.Context())

	if err != nil {
		if !ent.IsNotFound(err) {
			h.Logger.Error("Failed to query user", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		// Create
		u, err = h.Client.User.Create().
			SetUsername(username).
			SetLastLoginAt(time.Now()).
			Save(r.Context())
		if err != nil {
			h.Logger.Error("Failed to create user", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Create Navidrome Auth
		_, err = h.Client.NavidromeAuth.Create().
			SetUser(u).
			SetPassword(password).
			Save(r.Context())
		if err != nil {
			h.Logger.Error("Failed to create navidrome auth", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Seed built-in starter DJ personas for the new user
		if err := vibes.SeedDefaultDJs(r.Context(), h.Client, u); err != nil {
			// Non-fatal: log and continue; the user can create DJs manually
			h.Logger.Error("failed to seed default DJs", "user", username, "error", err)
		}
	} else {
		// Store the existing NavidromeAuth before updating user (edges are lost after Save)
		existingNavidromeAuth := u.Edges.NavidromeAuth

		// Update
		u, err = u.Update().
			SetLastLoginAt(time.Now()).
			Save(r.Context())
		if err != nil {
			h.Logger.Error("Failed to update user", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Update Navidrome Auth
		if existingNavidromeAuth != nil {
			_, err = h.Client.NavidromeAuth.UpdateOne(existingNavidromeAuth).
				SetPassword(password).
				Save(r.Context())
			if err != nil {
				h.Logger.Error("failed to update navidrome auth", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}
	}

	// Governing: SPEC-0015 REQ "Cooldown Reset on Recovery" — NavidromeAuth refreshed on login
	if h.Notifier != nil {
		if err := h.Notifier.ClearCooldown(r.Context(), u.ID, "navidrome"); err != nil {
			h.Logger.Error("failed to clear navidrome notification cooldown", "error", err)
		}
	}

	// Trigger initial sync
	go func(user *ent.User) {
		if err := h.Syncer.Sync(context.Background(), user); err != nil {
			h.Logger.Error("failed to sync user data", "error", err)
		}
	}(u)

	// 3. Generate JWT token
	token, err := h.JWTManager.GenerateToken(u.ID, u.Username)
	if err != nil {
		h.Logger.Error("Failed to generate JWT token", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 3. Refuse to hand out a cookie the browser will throw away: a Secure-flagged
	// cookie over a plain-HTTP response is dropped without a word to the user, so
	// a valid login looked like a no-op (issue #375). The Secure default stays on;
	// the operator names plain HTTP in config instead of discovering it by silence.
	// Governing: ADR-0034 (plain-HTTP cookie failures are surfaced, not silent)
	if h.Config.Security.SecureCookies && !requestIsSecure(r) {
		h.Logger.Warn("not setting session cookie: plain-HTTP request with secure cookies enabled",
			"remediation", "serve over HTTPS, or set security.secure_cookies=false for local HTTP use")
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", insecureCookieLoginURL())
			return
		}
		http.Redirect(w, r, insecureCookieLoginURL(), http.StatusSeeOther)
		return
	}

	// Set JWT cookie
	// Governing: ADR-0028 (CSRF protection: SameSite=Lax), ADR-0022 T3 (session cookie theft)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.Config.Security.SecureCookies,
		Expires:  time.Now().Add(24 * time.Hour),
		// Governing: ADR-0028 (SameSite=Lax is CSRF protection for POST requests),
		// issue #161 — Lax (not Strict) allows session cookie to be sent in
		// OAuth cross-site redirect chains (Spotify/LastFM → our callback → authenticated route)
		SameSite: http.SameSiteLaxMode,
	})

	// Check if this is an HTMX request
	if r.Header.Get("HX-Request") == "true" {
		// HTMX-enhanced request: use HX-Redirect header
		w.Header().Set("HX-Redirect", "/")
	} else {
		// Regular form submission: use standard HTTP redirect
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// Governing: SPEC user-authentication REQ "SESSION-002", REQ "SESSION-003", ADR-0028 (CSRF protection: SameSite=Lax)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.Config.Security.SecureCookies,
		Expires:  time.Now().Add(-1 * time.Hour),
		// Governing: ADR-0028 (SameSite=Lax is CSRF protection for POST requests),
		// issue #161 — Lax (not Strict) allows session cookie to be sent in
		// OAuth cross-site redirect chains (Spotify/LastFM → our callback → authenticated route)
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *Handler) authenticateNavidrome(username, password string) error {
	// Governing: ADR-0005 (Navidrome primary identity), SPEC user-authentication REQ "AUTH-005", REQ "AUTH-006"
	baseURL := h.Config.Navidrome.BaseURL
	if baseURL == "" {
		// If base URL is not set, we might want to fail or allow bypass for dev?
		// For this strict requirement, we fail.
		return fmt.Errorf("navidrome base url not configured")
	}

	// Generate random salt for Subsonic authentication (16 bytes = 32 hex chars)
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return fmt.Errorf("failed to generate random salt: %w", err)
	}
	salt := hex.EncodeToString(saltBytes)

	hash := md5.New()
	hash.Write([]byte(password + salt))
	token := hex.EncodeToString(hash.Sum(nil))

	params := url.Values{}
	params.Set("u", username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")

	// Handle trailing slash in base URL
	apiURL := fmt.Sprintf("%s/rest/ping.view?%s", baseURL, params.Encode())

	// Governing: SPEC user-authentication REQ "Sanitize Navidrome Auth Debug Logs"
	// Log sanitized URL (strip password hash params t= and s=)
	if sanitizedURL, err := url.Parse(apiURL); err == nil {
		q := sanitizedURL.Query()
		q.Del("t")
		q.Del("s")
		sanitizedURL.RawQuery = q.Encode()
		h.Logger.Debug("authenticating against Navidrome", "url", sanitizedURL.String())
	}

	// Governing: SPEC user-authentication REQ "Auth HTTP Client Timeout"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			h.Logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("navidrome returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.SubsonicResponse.Status != "ok" {
		return fmt.Errorf("navidrome error: %s", result.SubsonicResponse.Error.Message)
	}

	return nil
}
