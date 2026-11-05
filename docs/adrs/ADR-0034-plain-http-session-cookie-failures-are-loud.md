---
status: proposed
date: 2026-11-05
decision-makers: joestump-agent (proposed; issue #375 has no maintainer ruling)
---

# ADR-0034: Plain-HTTP Session Cookie Failures Are Surfaced, Not Silently Accepted

## Context and Problem Statement

`security.secure_cookies` defaults to `true`, so the `spotter_token` session cookie carries the `Secure` attribute (ADR-0022 T3, SPEC user-authentication REQ-SESSION-001). A browser that receives a `Secure` cookie over a plain-HTTP origin discards it and says nothing beyond a console warning. Spotter's login handler had already authenticated against Navidrome, created or updated the user, kicked off a sync and issued a redirect to `/`, so the user saw a login form that cleared itself and an app they could not enter ([issue #375](https://github.com/joestump/spotter/issues/375)). Docker installs reached over a LAN without TLS hit this, because Spotter never terminates TLS itself (`cmd/server/main.go` calls `ListenAndServe`) and any TLS in front of it belongs to a reverse proxy.

The symptom points at the network, not the config, so the reporter's first guess was a missing HTTPS requirement rather than one boolean they could set themselves.

## Decision Drivers

* The secure default must stay: making `Secure: false` the default would send session JWTs in cleartext for every install that never reads this ADR (ADR-0022 T3).
* A silent no-op is the worst possible outcome for a self-hosted operator; one actionable message naming the knob ends the debugging session.
* Spotter cannot observe the transport a proxy used, so any auto-detection has to trust a request header a client controls.
* OAuth `state` cookies already auto-detect with `Secure: r.TLS != nil` (SPEC user-authentication REQ-SPOTIFY-005, REQ-LASTFM-003), but they are 10-minute single-use values; the session cookie lives for 24 hours and opens the whole app, so the two do not carry the same risk.

## Considered Options

* **Option A**: keep everything as is and document that plain-HTTP installs must set `security.secure_cookies=false`.
* **Option B**: auto-detect — drop `Secure` whenever the request did not arrive over TLS, ignoring the config.
* **Option C**: keep `Secure` tied to `security.secure_cookies`, and when that flag is on but the request arrived over plain HTTP, set no session cookie at all and send the user back to the login page with one error naming `security.secure_cookies`.

## Decision Outcome

**Option C.** `PostLogin` checks `requestIsSecure(r)` before it sets the cookie: TLS terminated here, or an `X-Forwarded-Proto: https` from the proxy in front of Spotter. On a mismatch it logs a warning and redirects to `/auth/login?error=…` (or `HX-Redirect` for HTMX) with a message naming the knob and both ways out — HTTPS, or `security.secure_cookies=false`. The login page already renders an `error` query parameter, so no new surface was needed.

A secure request is unaffected: the cookie's `Secure` flag still follows config, never the transport.

## Consequences

**Positive**

* A plain-HTTP install either logs in or is told exactly which setting to change; the silent form-clearing case is gone.
* The default stays safe, and the documented opt-out keeps working without a code change.
* Proxied HTTPS installs are not nagged: `X-Forwarded-Proto` is checked, matching how the rate limiter already trusts `X-Forwarded-For` (`internal/middleware/ratelimit.go`).

**Negative**

* A client that spoofs `X-Forwarded-Proto: https` can make a plain-HTTP login offer a cookie the browser then drops. That returns the old silent behavior for that one request; it cannot weaken the cookie, and Spotter already trusts forwarded headers for IP attribution. A deployment wanting strict behaviour can keep its own proxy from passing the header through.
* Login on a mismatched plain-HTTP install is refused rather than half-applied, so an operator who expected to "get in and then fix config" has to edit config first.
* `Logout` still mirrors the login cookie's flags (REQ-SESSION-003); on an install that never stored a cookie there is nothing to clear, so it needs no change.

## References

* [issue #375](https://github.com/joestump/spotter/issues/375)
* ADR-0022 (threat model, T3: session cookie theft)
* ADR-0028 (CSRF protection: SameSite=Lax)
* SPEC user-authentication REQ-SESSION-001, REQ-SESSION-004
* ADR-0009 (Viper configuration: `SPOTTER_SECURITY_SECURE_COOKIES`)
