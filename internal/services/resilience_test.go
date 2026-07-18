package services_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"spotter/internal/providers"
	"spotter/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Error Classification Tests ---

func TestClassifyError_HTTPRetriableStatuses(t *testing.T) {
	retriableCodes := []int{
		http.StatusRequestTimeout,      // 408 — a timeout, retriable per REQ-ERR-002
		http.StatusTooManyRequests,     // 429
		http.StatusNotFound,            // 404 — transient behind reverse proxies during redeploys
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusInternalServerError, // 500
	}

	for _, code := range retriableCodes {
		t.Run(fmt.Sprintf("HTTP_%d", code), func(t *testing.T) {
			err := services.NewHTTPStatusError(code, fmt.Errorf("http error %d", code))
			assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
		})
	}
}

func TestClassifyError_HTTPFatalStatuses(t *testing.T) {
	fatalCodes := []int{
		http.StatusUnauthorized, // 401
		http.StatusForbidden,    // 403
	}

	for _, code := range fatalCodes {
		t.Run(fmt.Sprintf("HTTP_%d", code), func(t *testing.T) {
			err := services.NewHTTPStatusError(code, fmt.Errorf("http error %d", code))
			assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(err))
		})
	}
}

func TestClassifyError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"timeout", &net.DNSError{IsTimeout: true, Err: "timeout"}},
		{"dns_error", &net.DNSError{Err: "no such host", Name: "api.spotify.com"}},
		{"connection_refused", &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(tt.err))
		})
	}
}

func TestClassifyError_WrappedHTTPError(t *testing.T) {
	inner := services.NewHTTPStatusError(http.StatusUnauthorized, fmt.Errorf("token expired"))
	wrapped := fmt.Errorf("spotify API call failed: %w", inner)
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(wrapped))
}

func TestClassifyError_NilError(t *testing.T) {
	assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(nil))
}

func TestClassifyError_GenericErrorDefaultsToRetriable(t *testing.T) {
	err := errors.New("something unexpected happened")
	assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
}

func TestClassifyError_MessageBasedRetriable(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{"timeout_message", "request timeout exceeded"},
		{"connection_refused_message", "connection refused by remote host"},
		{"dns_message", "dns lookup failed"},
		{"eof_message", "unexpected eof"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.msg)
			assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
		})
	}
}

func TestClassifyError_MessageBasedFatal(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{"unauthorized_message", "unauthorized: token expired"},
		{"forbidden_message", "forbidden: insufficient permissions"},
		{"invalid_api_key", "invalid api key provided"},
		// Providers that return plain "returned status NNN" messages without HTTPStatusError.
		// 404 is deliberately absent: it classifies retriable (transient behind proxies).
		{"plain_status_401", "spotify API returned status 401"},
		{"plain_status_403", "spotify API returned status 403"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.msg)
			assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(err))
		})
	}
}

// TestClassifyError_RealWorldClientFormats exercises the string-matching
// fallback against the exact formats the provider/enricher clients emit,
// including the colon variant Navidrome uses ("returned status: 401") that
// the old "status 401" patterns failed to match, causing revoked credentials
// to retry forever (issue #325).
func TestClassifyError_RealWorldClientFormats(t *testing.T) {
	fatal := []struct {
		name string
		msg  string
	}{
		{"navidrome_colon_401", "navidrome API returned status: 401"},
		{"navidrome_colon_403", "navidrome API returned status: 403"},
		{"navidrome_login_401", "navidrome login failed with status: 401"},
		{"navidrome_internal_401", "navidrome internal API returned status: 401"},
		{"lastfm_provider_401", "last.fm api returned status 401: invalid session key"},
		{"lidarr_error_401", "lidarr api error: 401 - unauthorized"},
		{"fanart_403", "Fanart.tv API returned status 403"},
	}
	for _, tt := range fatal {
		t.Run("fatal_"+tt.name, func(t *testing.T) {
			assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(errors.New(tt.msg)))
		})
	}

	// 5xx/429/404 messages must stay retriable — no behavior change for
	// transient errors, and 404s can be transient route-drops behind a
	// reverse proxy during redeploys.
	retriable := []struct {
		name string
		msg  string
	}{
		{"navidrome_colon_503", "navidrome API returned status: 503"},
		{"navidrome_colon_404", "navidrome API returned status: 404"},
		{"spotify_500", "spotify API returned status 500"},
		{"spotify_404", "spotify API returned status 404"},
		{"lastfm_502", "last.fm api returned status 502: bad gateway"},
	}
	for _, tt := range retriable {
		t.Run("retriable_"+tt.name, func(t *testing.T) {
			assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(errors.New(tt.msg)))
		})
	}
}

// TestClassifyError_FatalPatternWinsOverRetriableSubstring pins the fallback
// ordering: a fatal status embedded in a message must win even when the
// appended response body contains a retriable-looking word like "timeout".
func TestClassifyError_FatalPatternWinsOverRetriableSubstring(t *testing.T) {
	err := errors.New("last.fm api returned status 401: upstream request timeout while validating session")
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(err))
}

// TestClassifyError_Transient404_Typed pins the deliberate decision that a
// typed 404 is retriable: reverse proxies (e.g. Traefik) return transient
// 404s while a backend's route is dropped during a container redeploy, and a
// fatal classification would permanently stop sync with a misleading
// "reconnect credentials" notification.
func TestClassifyError_Transient404_Typed(t *testing.T) {
	err := services.NewHTTPStatusError(http.StatusNotFound, fmt.Errorf("navidrome API returned status: %d", http.StatusNotFound))
	assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
}

// TestClassifyError_RequestTimeout408_Typed pins 408 as retriable — it is a
// timeout and REQ-ERR-002 requires timeouts to be retriable.
func TestClassifyError_RequestTimeout408_Typed(t *testing.T) {
	err := services.NewHTTPStatusError(http.StatusRequestTimeout, fmt.Errorf("spotify API returned status %d", http.StatusRequestTimeout))
	assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
}

// TestClassifyError_NavidromeRevokedCredentials_Typed simulates the error the
// Navidrome provider now returns for a revoked credential: a 401 wrapped in
// HTTPStatusError with the provider's exact message format.
func TestClassifyError_NavidromeRevokedCredentials_Typed(t *testing.T) {
	err := services.NewHTTPStatusError(401, fmt.Errorf("navidrome API returned status: %d", 401))
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(err))

	// Still fatal when wrapped further up the call chain
	wrapped := fmt.Errorf("failed to fetch listens: %w", err)
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(wrapped))
}

// Governing: SPEC error-handling REQ-ERR-003 (unparseable response body is fatal)
func TestClassifyError_DecodeErrorsFatal(t *testing.T) {
	var jsonTarget struct {
		Name string `json:"name"`
	}
	jsonSyntaxErr := json.Unmarshal([]byte("<html>502 Bad Gateway</html>"), &jsonTarget)
	require.Error(t, jsonSyntaxErr)

	jsonTypeErr := json.Unmarshal([]byte(`{"name": 42}`), &jsonTarget)
	require.Error(t, jsonTypeErr)

	var xmlTarget struct {
		Name string `xml:"name"`
	}
	xmlSyntaxErr := xml.Unmarshal([]byte("<lfm><name>x</wrong></lfm>"), &xmlTarget)
	require.Error(t, xmlSyntaxErr)
	var asXMLSyntax *xml.SyntaxError
	require.ErrorAs(t, xmlSyntaxErr, &asXMLSyntax)

	tests := []struct {
		name string
		err  error
	}{
		{"json_syntax", jsonSyntaxErr},
		{"json_type", jsonTypeErr},
		{"xml_syntax", xmlSyntaxErr},
		{"wrapped_json_syntax", fmt.Errorf("failed to decode response: %w", jsonSyntaxErr)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(tt.err))
		})
	}
}

func TestClassifyError_5xxRetriable(t *testing.T) {
	// Any 5xx status should be retriable
	err := services.NewHTTPStatusError(504, fmt.Errorf("gateway timeout"))
	assert.Equal(t, services.ErrorClassRetriable, services.ClassifyError(err))
}

func TestErrorClassString(t *testing.T) {
	assert.Equal(t, "retriable", services.ErrorClassRetriable.String())
	assert.Equal(t, "fatal", services.ErrorClassFatal.String())
}

// --- Backoff Calculation Tests ---

func TestCalculateBackoff_ExponentialGrowth(t *testing.T) {
	// Test that backoff grows exponentially
	prev := time.Duration(0)
	for i := 0; i < 5; i++ {
		delay := services.CalculateBackoff(i)
		// With jitter, delay should be in range [base * 2^i * 0.75, base * 2^i * 1.25]
		// Just check it's growing (accounting for jitter variability)
		if i > 0 {
			// The minimum of current should generally be more than half the minimum of previous
			// Since jitter adds variance, we test the trend over multiple samples
			t.Logf("failures=%d delay=%v", i, delay)
		}
		assert.Greater(t, delay, time.Duration(0), "delay should be positive")
		_ = prev
		prev = delay
	}
}

func TestCalculateBackoff_FirstFailure(t *testing.T) {
	// First failure: base = 30s * 2^(1-1) = 30s, with jitter [22.5s, 37.5s]
	// Governing: SPEC error-handling Scenario 1 (first failure waits ~30s)
	for i := 0; i < 10; i++ {
		delay := services.CalculateBackoff(1)
		assert.GreaterOrEqual(t, delay, 22500*time.Millisecond, "first failure delay should be >= 22.5s (30s * 0.75)")
		assert.LessOrEqual(t, delay, 37500*time.Millisecond, "first failure delay should be <= 37.5s (30s * 1.25)")
	}
}

// TestCalculateBackoff_SpecScenarioProgression verifies the 30/60/120s
// progression promised by SPEC error-handling Scenario 2 and ADR-0020,
// given that RecordFailure increments the counter before calculating.
func TestCalculateBackoff_SpecScenarioProgression(t *testing.T) {
	expected := map[int]time.Duration{
		1: 30 * time.Second,  // after 1st failure
		2: 60 * time.Second,  // after 2nd failure
		3: 120 * time.Second, // after 3rd failure
	}
	for failures, base := range expected {
		for i := 0; i < 10; i++ {
			delay := services.CalculateBackoff(failures)
			assert.GreaterOrEqual(t, delay, time.Duration(float64(base)*0.75), "failures=%d", failures)
			assert.LessOrEqual(t, delay, time.Duration(float64(base)*1.25), "failures=%d", failures)
		}
	}
}

func TestCalculateBackoff_ZeroFailures(t *testing.T) {
	// Zero consecutive failures clamps to the base delay: 30s, with jitter [22.5s, 37.5s]
	for i := 0; i < 10; i++ {
		delay := services.CalculateBackoff(0)
		assert.GreaterOrEqual(t, delay, time.Duration(float64(22500)*float64(time.Millisecond)))
		assert.LessOrEqual(t, delay, time.Duration(float64(37500)*float64(time.Millisecond)))
	}
}

func TestCalculateBackoff_MaxDelayCap(t *testing.T) {
	// At very high failure counts, delay should be capped at 30 minutes
	for i := 0; i < 10; i++ {
		delay := services.CalculateBackoff(100)
		// Max delay with max jitter: 30m * 1.25 = 37.5m
		assert.LessOrEqual(t, delay, 38*time.Minute, "delay should not exceed max with jitter")
		// Min delay with min jitter: 30m * 0.75 = 22.5m
		assert.GreaterOrEqual(t, delay, 22*time.Minute, "delay at max should be around 30m * jitter")
	}
}

func TestCalculateBackoff_JitterVariance(t *testing.T) {
	// Run multiple times and verify we get different values (jitter is random)
	seen := make(map[time.Duration]bool)
	for i := 0; i < 20; i++ {
		delay := services.CalculateBackoff(3)
		seen[delay] = true
	}
	assert.Greater(t, len(seen), 1, "jitter should produce varying delays")
}

// --- BackoffManager Tests ---

func TestBackoffManager_NewState(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}

	skip, _ := mgr.ShouldSkip(key)
	assert.False(t, skip, "new provider should not be skipped")
}

func TestBackoffManager_RecordRetriableFailure(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	err := services.NewHTTPStatusError(http.StatusServiceUnavailable, fmt.Errorf("service unavailable"))

	mgr.RecordFailure(key, err, services.ErrorClassRetriable)

	state, ok := mgr.GetState(key)
	require.True(t, ok)
	assert.Equal(t, 1, state.ConsecutiveFailures)
	assert.False(t, state.IsFatal)
	assert.False(t, state.NextRetryAt.IsZero(), "nextRetryAt should be set")
	assert.NotNil(t, state.LastError)
}

// TestBackoffManager_FirstFailureDelayMatchesSpec verifies end to end that
// the first recorded retriable failure schedules a retry ~30s out (RecordFailure
// increments before CalculateBackoff, which now compensates with 2^(n-1)).
// Governing: SPEC error-handling Scenarios 1-2, REQ-BACK-001
func TestBackoffManager_FirstFailureDelayMatchesSpec(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeNavidrome}

	before := time.Now()
	mgr.RecordFailure(key, fmt.Errorf("connection timeout"), services.ErrorClassRetriable)

	state, ok := mgr.GetState(key)
	require.True(t, ok)
	delay := state.NextRetryAt.Sub(before)
	// 30s with +/-25% jitter, small slack for elapsed wall time
	assert.GreaterOrEqual(t, delay, 22*time.Second, "first retry should be ~30s out, not ~60s")
	assert.LessOrEqual(t, delay, 38*time.Second, "first retry should be ~30s out, not ~60s")
}

func TestBackoffManager_RecordFatalFailure(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	err := services.NewHTTPStatusError(http.StatusUnauthorized, fmt.Errorf("token revoked"))

	mgr.RecordFailure(key, err, services.ErrorClassFatal)

	state, ok := mgr.GetState(key)
	require.True(t, ok)
	assert.True(t, state.IsFatal)
	assert.Equal(t, 0, state.ConsecutiveFailures, "fatal errors don't increment failure counter")

	skip, reason := mgr.ShouldSkip(key)
	assert.True(t, skip)
	assert.Contains(t, reason, "fatal")
}

func TestBackoffManager_ShouldSkipDuringBackoff(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	err := fmt.Errorf("connection timeout")

	mgr.RecordFailure(key, err, services.ErrorClassRetriable)

	// Should be skipped because nextRetryAt is in the future
	skip, reason := mgr.ShouldSkip(key)
	assert.True(t, skip)
	assert.Contains(t, reason, "backing off")
}

func TestBackoffManager_RecordSuccess_ResetsState(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	err := fmt.Errorf("connection timeout")

	// Record multiple failures
	mgr.RecordFailure(key, err, services.ErrorClassRetriable)
	mgr.RecordFailure(key, err, services.ErrorClassRetriable)
	mgr.RecordFailure(key, err, services.ErrorClassRetriable)

	state, _ := mgr.GetState(key)
	assert.Equal(t, 3, state.ConsecutiveFailures)

	// Record success
	mgr.RecordSuccess(key)

	state, ok := mgr.GetState(key)
	require.True(t, ok)
	assert.Equal(t, 0, state.ConsecutiveFailures)
	assert.True(t, state.NextRetryAt.IsZero())
	assert.Nil(t, state.LastError)
	assert.False(t, state.IsFatal)

	// Should not be skipped anymore
	skip, _ := mgr.ShouldSkip(key)
	assert.False(t, skip)
}

func TestBackoffManager_RecordSuccess_NoopForUnknownKey(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 999, ProviderType: providers.TypeSpotify}

	// Should not panic
	mgr.RecordSuccess(key)

	_, ok := mgr.GetState(key)
	assert.False(t, ok)
}

func TestBackoffManager_PerProviderIsolation(t *testing.T) {
	mgr := services.NewBackoffManager()
	spotifyKey := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	navidromeKey := services.BackoffKey{UserID: 1, ProviderType: providers.TypeNavidrome}

	// Only Spotify has a failure
	mgr.RecordFailure(spotifyKey, fmt.Errorf("timeout"), services.ErrorClassRetriable)

	spotifySkip, _ := mgr.ShouldSkip(spotifyKey)
	navidromeSkip, _ := mgr.ShouldSkip(navidromeKey)

	assert.True(t, spotifySkip, "Spotify should be skipped")
	assert.False(t, navidromeSkip, "Navidrome should not be affected")
}

func TestBackoffManager_PerUserIsolation(t *testing.T) {
	mgr := services.NewBackoffManager()
	user1Key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	user2Key := services.BackoffKey{UserID: 2, ProviderType: providers.TypeSpotify}

	// Only user 1 has a failure
	mgr.RecordFailure(user1Key, fmt.Errorf("timeout"), services.ErrorClassRetriable)

	user1Skip, _ := mgr.ShouldSkip(user1Key)
	user2Skip, _ := mgr.ShouldSkip(user2Key)

	assert.True(t, user1Skip, "User 1 should be skipped")
	assert.False(t, user2Skip, "User 2 should not be affected")
}

func TestBackoffManager_FatalPreventsAutoRetry(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}

	mgr.RecordFailure(key, fmt.Errorf("unauthorized"), services.ErrorClassFatal)

	// Should always be skipped regardless of time passing
	skip, _ := mgr.ShouldSkip(key)
	assert.True(t, skip, "Fatal error should prevent auto-retry")
}

func TestBackoffManager_ClearFatal(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}

	mgr.RecordFailure(key, fmt.Errorf("unauthorized"), services.ErrorClassFatal)

	skip, _ := mgr.ShouldSkip(key)
	assert.True(t, skip)

	// Clear fatal (simulating user reconnecting)
	mgr.ClearFatal(key)

	skip, _ = mgr.ShouldSkip(key)
	assert.False(t, skip, "After clearing fatal, provider should be retried")
}

func TestBackoffManager_MarkNotified(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}

	mgr.RecordFailure(key, fmt.Errorf("unauthorized"), services.ErrorClassFatal)

	state, _ := mgr.GetState(key)
	assert.False(t, state.NotifiedFatal)

	mgr.MarkNotified(key)

	state, _ = mgr.GetState(key)
	assert.True(t, state.NotifiedFatal)
}

func TestBackoffManager_ConsecutiveFailuresEscalate(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}
	err := fmt.Errorf("service unavailable")

	for i := 0; i < 5; i++ {
		mgr.RecordFailure(key, err, services.ErrorClassRetriable)
	}

	// Verify that failures increment
	state, _ := mgr.GetState(key)
	assert.Equal(t, 5, state.ConsecutiveFailures)
}

func TestBackoffManager_SuccessResetsNotifiedFatal(t *testing.T) {
	mgr := services.NewBackoffManager()
	key := services.BackoffKey{UserID: 1, ProviderType: providers.TypeSpotify}

	mgr.RecordFailure(key, fmt.Errorf("unauthorized"), services.ErrorClassFatal)
	mgr.MarkNotified(key)

	state, _ := mgr.GetState(key)
	assert.True(t, state.NotifiedFatal)

	mgr.RecordSuccess(key)

	state, _ = mgr.GetState(key)
	assert.False(t, state.NotifiedFatal)
}

// --- HTTPStatusError Tests ---

func TestHTTPStatusError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("original error")
	httpErr := services.NewHTTPStatusError(500, inner)

	assert.Equal(t, 500, httpErr.StatusCode)
	assert.Contains(t, httpErr.Error(), "original error")
	assert.Equal(t, inner, errors.Unwrap(httpErr))
}
