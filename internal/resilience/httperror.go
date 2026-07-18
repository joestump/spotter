// Governing: ADR-0020 (exponential backoff and circuit breaker), SPEC error-handling REQ-ERR-001 through REQ-ERR-004
//
// Package resilience holds typed errors shared by provider/enricher HTTP
// clients and the services-layer error classifier. It lives below both
// internal/providers and internal/services so that clients can construct
// typed errors without creating an import cycle (internal/services imports
// internal/providers).
package resilience

// HTTPStatusError wraps an error with an HTTP status code so that
// services.ClassifyError can classify it on the typed path instead of
// falling back to string matching.
// Governing: SPEC error-handling REQ-ERR-004
type HTTPStatusError struct {
	StatusCode int
	Err        error
}

func (e *HTTPStatusError) Error() string {
	return e.Err.Error()
}

func (e *HTTPStatusError) Unwrap() error {
	return e.Err
}

// NewHTTPStatusError creates a new HTTPStatusError. All provider and
// enricher HTTP clients MUST wrap non-2xx responses with this constructor.
func NewHTTPStatusError(statusCode int, err error) *HTTPStatusError {
	return &HTTPStatusError{StatusCode: statusCode, Err: err}
}
