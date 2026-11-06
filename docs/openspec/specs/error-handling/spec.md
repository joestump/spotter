# Error Handling and Resilience for External Service Calls

**Status:** accepted
**Version:** 0.1.0
**Last Updated:** 2026-02-21
**Governing ADRs:** ADR-0020 (exponential backoff with jitter), ADR-0007 (in-memory event bus), ADR-0016 (pluggable provider factory), ADR-0013 (goroutine ticker background scheduling)

## Overview

This spec defines how Spotter classifies, retries, and surfaces errors from external service calls (Navidrome, Spotify, Last.fm, MusicBrainz, Fanart.tv, OpenAI) during background sync operations. Errors are classified as retriable or fatal. Retriable errors trigger exponential backoff with jitter. Fatal errors publish a notification to the event bus for immediate user visibility in the browser.

## Scope

This spec covers:
- Error classification (retriable vs fatal) per provider
- Backoff state management (per-provider, per-user, in-memory)
- Backoff strategy (base delay, multiplier, cap, jitter)
- User notification for fatal errors via the event bus
- Recovery behavior (backoff reset on success)

Out of scope: Provider-specific API client implementation, retry logic within a single HTTP request (e.g., HTTP client `Transport`-level retries), persistent error state across restarts.

---

## Requirements

### Error Classification

**REQ-ERR-001** — Errors from external service calls MUST be classified into exactly one of two categories:
- **Retriable**: The error is transient and the operation SHOULD be retried after a delay.
- **Fatal**: The error indicates a permanent failure that requires user intervention.

**REQ-ERR-002** — The following error conditions MUST be classified as **retriable**:
- Network timeout or connection refused
- HTTP 429 (Too Many Requests)
- HTTP 503 (Service Unavailable)
- HTTP 502 (Bad Gateway)
- HTTP 500 (Internal Server Error) from external APIs
- OAuth token refresh succeeded (retry the original operation once with the new token)
- DNS resolution failure

**REQ-ERR-003** — The following error conditions MUST be classified as **fatal**:
- HTTP 401 (Unauthorized) — credentials revoked or expired with no refresh path
- HTTP 403 (Forbidden) — insufficient permissions or account deactivated
- Invalid or missing configuration (e.g., empty API key, malformed base URL)
- Unparseable response body (indicates API contract change)
- OAuth token refresh failed with 401/403 (refresh token itself is revoked)

**REQ-ERR-004** — Error classification SHOULD be implemented as a shared utility function that accepts an `error` and returns the classification. Provider-specific error mapping (e.g., Subsonic error codes, Last.fm XML error codes) MAY be handled by wrapper functions.

### Backoff Strategy

**REQ-BACK-001** — When a retriable error occurs, the system MUST calculate the next retry delay using exponential backoff:
```text
delay = min(baseDelay * 2^consecutiveFailures, maxDelay) * jitterFactor
```
Where:
- `baseDelay` = 30 seconds
- `maxDelay` = 30 minutes
- `jitterFactor` = random value in the range [0.75, 1.25]

**REQ-BACK-002** — The backoff delay MUST NOT exceed 30 minutes regardless of the number of consecutive failures.

**REQ-BACK-003** — Jitter MUST be applied to prevent synchronized retries when multiple providers fail simultaneously. The jitter range MUST be +/-25% of the calculated delay.

**REQ-BACK-004** — The sync loop MUST check the backoff state before calling a provider. If the current time is before `nextRetryAt`, the provider MUST be skipped for that sync tick.

### Per-Provider State

**REQ-STATE-001** — Backoff state MUST be maintained per-provider per-user. A `BackoffState` struct (or equivalent) MUST track at minimum:
- `consecutiveFailures int` — number of consecutive retriable errors
- `nextRetryAt time.Time` — earliest time the provider should be retried
- `lastError error` — the most recent error (for logging/diagnostics)
- `isFatal bool` — whether the last error was classified as fatal

**REQ-STATE-002** — Backoff state MUST be stored in memory (no database persistence). State is keyed by `(userID, providerType)`.

**REQ-STATE-003** — Backoff state MUST be protected by a `sync.RWMutex` or equivalent for safe concurrent access from multiple sync goroutines.

**REQ-STATE-004** — When a fatal error is recorded, the provider MUST NOT be retried automatically. The fatal flag MUST be cleared only when the user takes corrective action (e.g., reconnects the provider, updates credentials).

### User Notification

**REQ-NOTIFY-001** — When a fatal error is detected, the system MUST publish a `NotificationPayload` to the event bus with:
- `Title`: A human-readable summary (e.g., "Spotify Connection Failed")
- `Message`: A description of the error and suggested action (e.g., "Your Spotify credentials have expired. Please reconnect from Preferences.")
- `IconType`: `"error"`

**REQ-NOTIFY-002** — Fatal error notifications MUST be published at most once per error occurrence. If the same fatal error persists across sync ticks, the notification MUST NOT be repeated.

**REQ-NOTIFY-003** — Retriable errors SHOULD NOT generate user-visible notifications unless the backoff has reached the maximum delay (30 minutes), at which point a single `"warning"` notification MAY be published.

### Recovery

**REQ-RECOVER-001** — On a successful provider call, the backoff state for that provider MUST be fully reset:
- `consecutiveFailures` set to 0
- `nextRetryAt` set to zero value
- `lastError` set to nil
- `isFatal` set to false

**REQ-RECOVER-002** — Backoff reset MUST occur immediately after the successful call, before proceeding to the next operation for that provider.

---

## Scenarios

### Scenario 1: Transient network error with recovery

```gherkin
Given Spotify returns a network timeout during listen sync
When the syncer records the error
Then consecutiveFailures is set to 1
And nextRetryAt is set to ~30 seconds from now (with jitter)
And the syncer skips Spotify on the next tick if within the backoff window
When the backoff window elapses and the next sync tick occurs
And Spotify responds successfully
Then consecutiveFailures is reset to 0
And nextRetryAt is cleared
```

### Scenario 2: Repeated transient errors with escalating backoff

```gherkin
Given Spotify returns 503 on three consecutive sync attempts
Then the backoff delays are approximately:
  - After 1st failure: ~30s
  - After 2nd failure: ~60s
  - After 3rd failure: ~120s
And jitter is applied to each delay (+/-25%)
```

### Scenario 3: Fatal credential revocation

```gherkin
Given the user revokes Spotify access from their Spotify account settings
When the syncer attempts to use the stored refresh token
And Spotify returns 401 on the token refresh
Then the error is classified as fatal
And a NotificationPayload is published:
  Title: "Spotify Connection Failed"
  Message: "Your Spotify credentials have been revoked. Please reconnect from Preferences."
  IconType: "error"
And the Spotify provider is not retried until the user reconnects
```

### Scenario 4: OAuth token refresh with retry

```gherkin
Given the Spotify access token has expired
When the syncer calls the Spotify API and receives 401
And the token refresh succeeds (new access token obtained)
Then the original operation is retried once with the new token
And if the retry succeeds, backoff state is reset
```

### Scenario 5: Process restart clears backoff state

```gherkin
Given the Navidrome provider has a backoff of 15 minutes remaining
When the Spotter process is restarted
Then all in-memory backoff state is lost
And the Navidrome provider is retried immediately on the first sync tick
```

---

## Implementation Notes

- Backoff state: new struct in `internal/services/` or `internal/resilience/` — `BackoffState` with mutex-protected map
- Error classifier: utility function in same package — `ClassifyError(err error) ErrorClass`
- Integration point: `internal/services/sync.go` — check backoff before calling provider, update state after call
- Event bus notification: `internal/events/bus.go` — `PublishNotification(userID, title, message, iconType)`
- Provider factory: `internal/providers/providers.go` — no changes needed; error classification wraps provider return values
- Governing comment: `// Governing: ADR-0020 (exponential backoff with jitter), ADR-0007 (event bus for notifications), SPEC error-handling`
