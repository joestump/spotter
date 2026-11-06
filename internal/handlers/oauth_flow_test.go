package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spotter/internal/handlers"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Last.fm's protocol does not echo the state parameter back, so nothing compares
// it: the cookie carries only the encrypted user ID that identifies the session.
// Generating a value no one checks invites treating it as a check that exists.
//
// These tests pin the resolution that used to be two near-identical ~200-line
// handler bodies, one per provider.

func TestLastfmCallback_CookieCarriesJustTheEncryptedUserID(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)
	client := h.Client

	u, err := client.User.Create().SetUsername("lastfm-session").Save(context.Background())
	require.NoError(t, err)

	encryptedUserID, err := encryptor.EncryptInt(u.ID)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/auth/lastfm/callback", nil)
	req.AddCookie(&http.Cookie{Name: "lastfm_oauth_state", Value: encryptedUserID})
	w := httptest.NewRecorder()

	h.LastFMCallback(w, req)

	location := w.Result().Header.Get("Location")
	assert.NotContains(t, location, "error=session_expired",
		"the cookie alone identifies the session")
	assert.NotContains(t, location, "error=invalid_state")
	assert.Contains(t, location, "error=missing_token",
		"resolution should reach the grant check, which is where this request stops")
}

// A cookie issued before the unused state value was dropped prefixes the same
// ciphertext with it; those sessions must still land.
func TestLastfmCallback_StillAcceptsTheLegacyCookieShape(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)
	client := h.Client

	u, err := client.User.Create().SetUsername("lastfm-legacy").Save(context.Background())
	require.NoError(t, err)

	encryptedUserID, err := encryptor.EncryptInt(u.ID)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/auth/lastfm/callback", nil)
	req.AddCookie(&http.Cookie{Name: "lastfm_oauth_state", Value: "some-legacy-state:" + encryptedUserID})
	w := httptest.NewRecorder()

	h.LastFMCallback(w, req)

	location := w.Result().Header.Get("Location")
	assert.NotContains(t, location, "error=session_expired")
	assert.Contains(t, location, "error=missing_token")
}

func TestLastfmCallback_WithoutACookieThereIsNoSession(t *testing.T) {
	h, _ := newOAuthTestHandler(t)

	req := httptest.NewRequest("GET", "/auth/lastfm/callback?token=whatever", nil)
	w := httptest.NewRecorder()

	h.LastFMCallback(w, req)

	assert.Contains(t, w.Result().Header.Get("Location"), "error=session_expired")
}

// The login side must actually write that shape: the encrypted user ID, with no
// state value in front of it.
func TestLastfmLogin_CookieHoldsOnlyTheEncryptedUserID(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)
	client := h.Client
	h.Config.LastFM.APIKey = "test-api-key"

	u, err := client.User.Create().SetUsername("lastfm-login").Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/auth/lastfm/login", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.LastFMLogin(w, req)

	cookie := findCookie(w, "lastfm_oauth_state")
	require.NotNil(t, cookie, "the flow must set its state cookie")

	userID, err := encryptor.DecryptInt(cookie.Value)
	require.NoError(t, err, "the cookie must be nothing but the encrypted user ID")
	assert.Equal(t, u.ID, userID)

	redirect, err := url.Parse(redirectLocation(w))
	require.NoError(t, err)
	assert.Empty(t, redirect.Query().Get("state"),
		"Last.fm has no state to carry through its side")
}

// Spotify is the opposite case: it echoes state, so the cookie holds the CSRF
// token and the URL carries the pair.
func TestSpotifyLogin_CookieHoldsTheCSRFTokenAndTheURLCarriesThePair(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)
	client := h.Client
	h.Config.Spotify.ClientID = "test-client-id"
	h.Config.Spotify.ClientSecret = "test-client-secret"

	u, err := client.User.Create().SetUsername("spotify-login").Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/auth/spotify/login", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.SpotifyLogin(w, req)

	cookie := findCookie(w, "spotify_oauth_state")
	require.NotNil(t, cookie)
	assert.NotContains(t, cookie.Value, ":", "the cookie holds the CSRF token alone")

	redirect, err := url.Parse(redirectLocation(w))
	require.NoError(t, err)

	csrf, encryptedUserID, found := strings.Cut(redirect.Query().Get("state"), ":")
	require.True(t, found, "the redirect must carry the CSRF token and the user ID")
	assert.Equal(t, cookie.Value, csrf,
		"the state starts with the token the cookie holds")

	userID, err := encryptor.DecryptInt(encryptedUserID)
	require.NoError(t, err, "the half after the first colon is the encrypted user ID")
	assert.Equal(t, u.ID, userID)
}

func redirectLocation(w *httptest.ResponseRecorder) string {
	return w.Result().Header.Get("Location")
}

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
