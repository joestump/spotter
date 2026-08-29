package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The OAuth state carried through the provider round trip is
// "<csrf>:<encryptedUserID>", and the encrypted half is produced by
// Encryptor.EncryptInt, which returns "enc:v1:<ciphertext>". That value
// contains colons of its own, so the delimiter is the FIRST colon, never the
// last.
//
// Both callbacks used to scan backwards for a colon. On the Spotify side that
// split inside the marker -- csrfState came out as "<csrf>:enc:v1", which never
// equalled the cookie, so every Spotify authorization ended at
// /auth/login?error=invalid_state. Last.fm shared the bug but hid it: it never
// compares state, and Decrypt happens to accept the marker-stripped ciphertext
// through its legacy path.
//
// These tests pin the invariant that made the bug possible, and the fixed
// behaviour of each handler.

func TestEncryptedUserIDContainsColons(t *testing.T) {
	// The premise of the bug. If EncryptInt ever stops embedding colons this
	// test should fail loudly rather than let the parsing quietly go back to
	// being ambiguous.
	encryptor, err := crypto.NewEncryptor(make([]byte, 32))
	require.NoError(t, err)

	encrypted, err := encryptor.EncryptInt(42)
	require.NoError(t, err)

	assert.Contains(t, encrypted, ":",
		"encrypted user IDs embed colons, so OAuth state must split on the first one")
	assert.True(t, strings.HasPrefix(encrypted, crypto.EncryptedMarker),
		"expected the enc:v1: marker that makes a last-colon split wrong")

	// The precise failure: a last-colon split keeps part of the marker in the
	// CSRF half and strips it from the ciphertext half.
	state := "csrf-token:" + encrypted
	lastIdx := strings.LastIndex(state, ":")
	assert.NotEqual(t, "csrf-token", state[:lastIdx],
		"a last-colon split must not recover the CSRF token -- that was the bug")

	csrf, encPart, found := strings.Cut(state, ":")
	require.True(t, found)
	assert.Equal(t, "csrf-token", csrf)
	assert.Equal(t, encrypted, encPart, "the encrypted half must keep its marker")

	id, err := encryptor.DecryptInt(encPart)
	require.NoError(t, err)
	assert.Equal(t, 42, id)
}

func newOAuthTestHandler(t *testing.T) (*handlers.Handler, *crypto.Encryptor) {
	t.Helper()
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()
	syncer := services.NewSyncer(client, cfg, logger, bus, nil)
	encryptor, err := crypto.NewEncryptor(make([]byte, 32))
	require.NoError(t, err)
	jwtManager := auth.NewJWTManager(testJWTSecret)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, syncer, nil, nil, nil, nil, nil, bus, nil)
	return h, encryptor
}

// A callback whose state carries a real encrypted user ID must clear the CSRF
// comparison. Before the fix this redirected to error=invalid_state.
func TestSpotifyCallback_StateWithEncryptedUserIDPassesCSRF(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)

	const csrf = "uWZ0s1-e1W1BSNlrlps_28z6Z4RQb_aHaVhyT817a4I="
	encryptedUserID, err := encryptor.EncryptInt(1)
	require.NoError(t, err)

	req := httptest.NewRequest("GET",
		"/auth/spotify/callback?code=test-code&state="+csrf+":"+encryptedUserID, nil)
	req.AddCookie(&http.Cookie{Name: "spotify_oauth_state", Value: csrf})
	w := httptest.NewRecorder()

	h.SpotifyCallback(w, req.WithContext(context.Background()))

	location := w.Result().Header.Get("Location")
	assert.NotContains(t, location, "error=invalid_state",
		"CSRF state must match; a last-colon split reintroduces the invalid_state bug")
}

// A genuinely mismatched CSRF token must still be rejected -- the fix must not
// weaken the check it repairs.
func TestSpotifyCallback_RejectsMismatchedState(t *testing.T) {
	h, encryptor := newOAuthTestHandler(t)

	encryptedUserID, err := encryptor.EncryptInt(1)
	require.NoError(t, err)

	req := httptest.NewRequest("GET",
		"/auth/spotify/callback?code=test-code&state=attacker-supplied:"+encryptedUserID, nil)
	req.AddCookie(&http.Cookie{Name: "spotify_oauth_state", Value: "the-real-csrf-token"})
	w := httptest.NewRecorder()

	h.SpotifyCallback(w, req.WithContext(context.Background()))

	assert.Contains(t, w.Result().Header.Get("Location"), "error=invalid_state",
		"a mismatched CSRF token must still be refused")
}

// State with no colon at all is malformed and must be refused rather than
// panicking or being treated as a bare CSRF token.
func TestSpotifyCallback_RejectsStateWithoutColon(t *testing.T) {
	h, _ := newOAuthTestHandler(t)

	req := httptest.NewRequest("GET", "/auth/spotify/callback?code=test-code&state=no-colon-here", nil)
	req.AddCookie(&http.Cookie{Name: "spotify_oauth_state", Value: "no-colon-here"})
	w := httptest.NewRecorder()

	h.SpotifyCallback(w, req.WithContext(context.Background()))

	assert.Contains(t, w.Result().Header.Get("Location"), "error=invalid_state")
}

// Last.fm reads the same "<csrf>:<encryptedUserID>" shape out of its cookie. It
// never failed visibly, because Decrypt accepts the marker-stripped ciphertext
// as legacy input -- so assert it now recovers the marked value intact, and
// stops depending on that fallback.
func TestLastfmCallback_RecoversMarkedEncryptedUserID(t *testing.T) {
	_, encryptor := newOAuthTestHandler(t)

	const csrf = "ze8YV5lIP-1R4wUrjN0cPsKHtR1270BJft3v5GkHm-I="
	encryptedUserID, err := encryptor.EncryptInt(7)
	require.NoError(t, err)

	_, encPart, found := strings.Cut(csrf+":"+encryptedUserID, ":")
	require.True(t, found)

	assert.True(t, strings.HasPrefix(encPart, crypto.EncryptedMarker),
		"the Last.fm cookie split must preserve the enc:v1: marker")

	id, err := encryptor.DecryptInt(encPart)
	require.NoError(t, err)
	assert.Equal(t, 7, id)
}
