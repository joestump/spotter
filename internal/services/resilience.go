// Governing: ADR-0020 (exponential backoff and circuit breaker), ADR-0007 (event bus for notifications), SPEC error-handling
package services

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"spotter/internal/providers"
	"spotter/internal/resilience"
)

// ErrorClass represents the classification of an error as retriable or fatal.
// Governing: SPEC error-handling REQ-ERR-001
type ErrorClass int

const (
	// ErrorClassRetriable indicates a transient error that should be retried after a delay.
	ErrorClassRetriable ErrorClass = iota
	// ErrorClassFatal indicates a permanent error requiring user intervention.
	ErrorClassFatal
)

func (c ErrorClass) String() string {
	switch c {
	case ErrorClassRetriable:
		return "retriable"
	case ErrorClassFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// HTTPStatusError wraps an error with an HTTP status code for classification.
// It is defined in internal/resilience so that provider and enricher HTTP
// clients can construct it without importing internal/services (which would
// create an import cycle via internal/providers). This alias keeps the
// services-side API unchanged.
// Governing: SPEC error-handling REQ-ERR-004
type HTTPStatusError = resilience.HTTPStatusError

// NewHTTPStatusError creates a new HTTPStatusError.
func NewHTTPStatusError(statusCode int, err error) *HTTPStatusError {
	return resilience.NewHTTPStatusError(statusCode, err)
}

// ClassifyError classifies an error as retriable or fatal.
// It prefers typed classification: HTTPStatusError (HTTP status-based),
// net.Error (network-level), and JSON/XML decode errors (unparseable
// response bodies). Message-based string matching is kept only as a
// fallback for errors that reach the classifier unwrapped.
// Governing: SPEC error-handling REQ-ERR-001 through REQ-ERR-004
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorClassRetriable
	}

	// Check for HTTP status code errors
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return classifyHTTPStatus(httpErr.StatusCode)
	}

	// Check for network-level errors (timeout, connection refused, DNS)
	// Governing: SPEC error-handling REQ-ERR-002 (network timeout, connection refused, DNS failure)
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ErrorClassRetriable
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrorClassRetriable
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return ErrorClassRetriable
	}

	// Unparseable response bodies (malformed JSON/XML) indicate an API
	// contract change and are fatal. Truncated-body I/O errors (io.EOF,
	// io.ErrUnexpectedEOF) intentionally remain retriable — they usually
	// indicate transient network truncation, not a contract change.
	// Governing: SPEC error-handling REQ-ERR-003 (unparseable response body)
	var jsonSyntaxErr *json.SyntaxError
	var jsonTypeErr *json.UnmarshalTypeError
	var xmlSyntaxErr *xml.SyntaxError
	if errors.As(err, &jsonSyntaxErr) || errors.As(err, &jsonTypeErr) || errors.As(err, &xmlSyntaxErr) {
		return ErrorClassFatal
	}

	// Heuristic: check error message for common patterns. Fatal patterns are
	// checked first so a fatal status embedded in a message wins even when
	// the appended response body happens to contain a retriable-looking word
	// (e.g. `last.fm api returned status 401: ...timeout...`).
	msg := strings.ToLower(err.Error())
	if isFatalErrorMessage(msg) {
		return ErrorClassFatal
	}
	if isRetriableErrorMessage(msg) {
		return ErrorClassRetriable
	}

	// Default to retriable for unknown errors — prefer retry over giving up
	return ErrorClassRetriable
}

// classifyHTTPStatus maps HTTP status codes to error classes.
// Governing: SPEC error-handling REQ-ERR-002 (retriable statuses), REQ-ERR-003 (fatal statuses)
func classifyHTTPStatus(statusCode int) ErrorClass {
	switch statusCode {
	case http.StatusRequestTimeout, // 408 — a timeout, retriable per REQ-ERR-002
		http.StatusTooManyRequests,     // 429
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusInternalServerError: // 500
		return ErrorClassRetriable
	case http.StatusNotFound: // 404
		// Deliberate: 404 is retriable. Reverse proxies (e.g. Traefik) return
		// transient 404s while a backend's route is dropped during a container
		// redeploy; classifying that fatal would permanently stop sync with a
		// misleading "reconnect credentials" notification. A genuinely wrong
		// base URL keeps retrying at the 30-minute backoff cap instead —
		// invalid configuration is REQ-ERR-003's concern at config-validation
		// time, not per-request.
		return ErrorClassRetriable
	case http.StatusUnauthorized, // 401
		http.StatusForbidden: // 403
		return ErrorClassFatal
	default:
		if statusCode >= 500 {
			return ErrorClassRetriable
		}
		// Remaining 4xx (400, 405, 422, ...) are client errors that will not
		// succeed on retry — the request itself is wrong.
		return ErrorClassFatal
	}
}

func isRetriableErrorMessage(msg string) bool {
	retriablePatterns := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"dns",
		"no such host",
		"temporary failure",
		"too many requests",
		"service unavailable",
		"bad gateway",
		"eof",
		"broken pipe",
		"i/o timeout",
	}
	for _, pattern := range retriablePatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// fatalStatusPattern matches error messages that embed a fatal HTTP status
// code, covering the formats the clients actually emit (fallback only — all
// clients now wrap non-2xx responses in resilience.NewHTTPStatusError):
//
//	"navidrome API returned status: 401"     (colon before the code)
//	"navidrome login failed with status: 403"
//	"spotify API returned status 401"        (no colon)
//	"last.fm api returned status 401: ..."   (code followed by body)
//	"lidarr api error: 403 - ..."
//
// 404 is deliberately excluded to stay consistent with the typed path in
// classifyHTTPStatus: transient 404s from reverse proxies during redeploys
// must remain retriable.
// Governing: ADR-0020, SPEC error-handling REQ-ERR-003
var fatalStatusPattern = regexp.MustCompile(`(?:status|error):? *(?:401|403)\b`)

func isFatalErrorMessage(msg string) bool {
	fatalPatterns := []string{
		"unauthorized",
		"forbidden",
		"invalid api key",
		"invalid credentials",
		"revoked",
		"deactivated",
	}
	for _, pattern := range fatalPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	// Catch "returned status 4xx" style messages from errors that were not
	// wrapped in HTTPStatusError. 401/403 are credential errors that won't
	// succeed on retry.
	return fatalStatusPattern.MatchString(msg)
}

// BackoffState tracks per-provider error state.
// Governing: SPEC error-handling REQ-STATE-001
type BackoffState struct {
	ConsecutiveFailures int
	NextRetryAt         time.Time
	LastError           error
	IsFatal             bool
	// NotifiedFatal tracks whether a fatal error notification has already been sent.
	// Governing: SPEC error-handling REQ-NOTIFY-002
	NotifiedFatal bool
}

// BackoffKey identifies a unique provider instance per user.
// Governing: SPEC error-handling REQ-STATE-002
type BackoffKey struct {
	UserID       int
	ProviderType providers.Type
}

// BackoffManager manages per-provider backoff state.
// Governing: SPEC error-handling REQ-STATE-002, REQ-STATE-003
type BackoffManager struct {
	mu     sync.RWMutex
	states map[BackoffKey]*BackoffState
	// nowFunc is used for time calculations, overridable in tests
	nowFunc func() time.Time
}

// NewBackoffManager creates a new BackoffManager.
func NewBackoffManager() *BackoffManager {
	return &BackoffManager{
		states:  make(map[BackoffKey]*BackoffState),
		nowFunc: time.Now,
	}
}

const (
	backoffBaseDelay = 30 * time.Second
	backoffMaxDelay  = 30 * time.Minute
)

// CalculateBackoff computes the retry delay after the Nth consecutive failure.
// delay = min(30s * 2^(consecutiveFailures-1), 30m) * jitter[0.75, 1.25]
//
// RecordFailure increments ConsecutiveFailures before calling this, so the
// first failure passes consecutiveFailures=1 and must wait ~30s (base delay),
// the second ~60s, the third ~120s — matching SPEC error-handling
// Scenarios 1-2 and ADR-0020 ("30s, 60s, 120s, ... up to 30 minutes").
// Governing: SPEC error-handling REQ-BACK-001, REQ-BACK-002, REQ-BACK-003
func CalculateBackoff(consecutiveFailures int) time.Duration {
	exponent := consecutiveFailures - 1
	if exponent < 0 {
		exponent = 0
	}
	delay := float64(backoffBaseDelay) * math.Pow(2, float64(exponent))
	if delay > float64(backoffMaxDelay) {
		delay = float64(backoffMaxDelay)
	}
	// Apply jitter: random value in [0.75, 1.25]
	jitter := 0.75 + rand.Float64()*0.5
	return time.Duration(delay * jitter)
}

// RecordFailure updates backoff state for a provider failure.
// Governing: SPEC error-handling REQ-STATE-001, REQ-STATE-004
func (m *BackoffManager) RecordFailure(key BackoffKey, err error, class ErrorClass) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.states[key]
	if !ok {
		state = &BackoffState{}
		m.states[key] = state
	}

	state.LastError = err

	if class == ErrorClassFatal {
		state.IsFatal = true
		// Fatal errors don't use backoff timing — they stay blocked until user action
		return
	}

	// Retriable error: increment failures and calculate next retry
	state.ConsecutiveFailures++
	delay := CalculateBackoff(state.ConsecutiveFailures)
	state.NextRetryAt = m.nowFunc().Add(delay)
}

// RecordSuccess resets backoff state after a successful call.
// Governing: SPEC error-handling REQ-RECOVER-001, REQ-RECOVER-002
func (m *BackoffManager) RecordSuccess(key BackoffKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.states[key]
	if !ok {
		return
	}

	state.ConsecutiveFailures = 0
	state.NextRetryAt = time.Time{}
	state.LastError = nil
	state.IsFatal = false
	state.NotifiedFatal = false
}

// ShouldSkip returns true if the provider should be skipped due to backoff or fatal state.
// Governing: SPEC error-handling REQ-BACK-004, REQ-STATE-004
func (m *BackoffManager) ShouldSkip(key BackoffKey) (skip bool, reason string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[key]
	if !ok {
		return false, ""
	}

	if state.IsFatal {
		return true, "fatal error requires user action"
	}

	if !state.NextRetryAt.IsZero() && m.nowFunc().Before(state.NextRetryAt) {
		return true, "backing off until " + state.NextRetryAt.Format(time.RFC3339)
	}

	return false, ""
}

// GetState returns a copy of the current backoff state for a key.
func (m *BackoffManager) GetState(key BackoffKey) (BackoffState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[key]
	if !ok {
		return BackoffState{}, false
	}
	return *state, true
}

// MarkNotified marks a fatal error as having been notified.
// Governing: SPEC error-handling REQ-NOTIFY-002
func (m *BackoffManager) MarkNotified(key BackoffKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state, ok := m.states[key]; ok {
		state.NotifiedFatal = true
	}
}

// ClearFatal resets the fatal flag for a provider (e.g., after user reconnects).
// Governing: SPEC error-handling REQ-STATE-004
func (m *BackoffManager) ClearFatal(key BackoffKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state, ok := m.states[key]; ok {
		state.IsFatal = false
		state.NotifiedFatal = false
		state.ConsecutiveFailures = 0
		state.NextRetryAt = time.Time{}
		state.LastError = nil
	}
}
