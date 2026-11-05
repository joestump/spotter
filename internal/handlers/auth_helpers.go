package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"spotter/ent"
)

// RequireUser returns the authenticated user for API/HTMX handlers.
// If no user is authenticated, it writes a 401 Unauthorized response and returns nil.
// Callers MUST check the returned value: if nil, return immediately.
func (h *Handler) RequireUser(w http.ResponseWriter, r *http.Request) *ent.User {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
	return u
}

// RequireUserRedirect returns the authenticated user for page-level handlers.
// If no user is authenticated, it redirects to /auth/login and returns nil.
// Callers MUST check the returned value: if nil, return immediately.
func (h *Handler) RequireUserRedirect(w http.ResponseWriter, r *http.Request) *ent.User {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
	}
	return u
}

// insecureCookieMessage names the knob behind a login that authenticates but
// cannot stick, so the operator gets one actionable error instead of a form
// that silently clears (issue #375).
const insecureCookieMessage = "Login succeeded, but the browser would discard the session cookie: this request reached Spotter over plain HTTP while security.secure_cookies is enabled. Serve Spotter over HTTPS (or a TLS-terminating proxy), or set security.secure_cookies=false (SPOTTER_SECURITY_SECURE_COOKIES=false) to sign in over local HTTP."

// insecureCookieLoginURL is /auth/login carrying insecureCookieMessage, which
// the login page renders as its error alert.
func insecureCookieLoginURL() string {
	return "/auth/login?error=" + url.QueryEscape(insecureCookieMessage)
}

// requestIsSecure reports whether the request reached Spotter over an encrypted
// connection: TLS terminated here, or a reverse proxy that reported https in
// X-Forwarded-Proto. Spotter never terminates TLS itself (cmd/server/main.go
// calls ListenAndServe), so the forwarded header is the only signal a proxied
// install gives. A client on the network can claim https it does not have; that
// only brings back the pre-existing silent drop, never a weaker cookie.
// Governing: ADR-0034 (plain-HTTP cookie failures are surfaced, not silent)
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if comma := strings.IndexByte(proto, ','); comma >= 0 {
		proto = proto[:comma]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
