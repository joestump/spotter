---
status: proposed
date: 2026-11-07
decision-makers: joestump-agent (proposed; issue #344 leaves cached vs async open)
---

# ADR-0035: Provider Reachability Is Reported From A Cache, Refreshed Off The Render Path

## Context and Problem Statement

The home page shows a Navidrome online/offline badge. It obtained that badge by calling `checkNavidromeOnline` inline, on the request goroutine, with a `http.Client` whose timeout was 5 seconds. A Navidrome server that is slow or unreachable therefore held up the whole render for up to 5 seconds for every page view, which issue #344 lists under an acceptance criterion that says the render "is not blocked by Navidrome reachability". The story offered two shapes, "make the ping cached or async", and did not pick between them.

The two options are not equivalent against that criterion. A plain TTL cache still blocks: the first render after the entry goes stale pays the probe, so every `ttl` interval one page view is slow, and the very first render after startup pays it in full.

## Decision Drivers

* The acceptance criterion is about latency on the render path, not about the probe's timeout
* A badge is observability, not a gate: nothing downstream reads `ServerOnline` before doing work, so one interval of staleness costs a wrong dot, not a failed operation
* Background work must belong to the application lifecycle so shutdown cancels and drains it (ADR-0018, SPEC graceful-shutdown REQ-CTX-003) rather than leaving a goroutine racing process exit
* Spotter is multi-user (ADR-0022 note in ADR-0031), so a probe result is per user and must not be shared across accounts
* Repeated renders must not stack probes on one another while one is already in flight

## Considered Options

* **Option A: synchronous ping with a TTL cache** — simplest, but a stale entry makes one render in every interval as slow as the server is unresponsive, so the criterion holds only most of the time
* **Option B: async refresh behind a per-user cache** — the render reads the last known result and hands the probe to `Jobs`; no render ever waits, at the cost of one interval of a stale dot and an unknown first one
* **Option C: probe on a fixed scheduler with no render involvement** — warmest data, but it keeps probing users who never look at the home page, and it needs its own lifecycle wiring rather than borrowing the one request already has

## Decision Outcome

Chosen option B, because it is the only one that satisfies the acceptance criterion unconditionally, and because a stale badge is cheap while a stalled render is not. `internal/handlers/navidrome_health.go` holds a per-user entry with the last result, the time it was recorded, and an in-flight flag; `Handler.navidromeOnline` returns the last known result and, when the entry is older than the TTL, starts exactly one refresh through `Jobs.Start`.

## Consequences

* A Navidrome server that stops answering cannot add latency to a page view; the worst case is that the badge is up to one TTL interval behind
* The first render after startup reports offline while the first probe runs, and every render after it reports what the probe found. A probe that is cancelled by shutdown leaves the entry stale instead of caching an outage that never happened, and the next render tries again
* `Jobs` being unset (tests, optional wiring) falls back to an untracked goroutine, matching the existing pattern in `internal/handlers/artists.go`
* The TTL is a single constant, so a deployment that wants a fresher dot trades render freshness against probe load in one place

## References

* [Issue #344](https://github.com/joestump/spotter/issues/344) — enrichment and dashboard performance
* [ADR-0018](./ADR-0018-graceful-shutdown-context-cancellation.md) — background work is tracked and cancelled on shutdown
* [ADR-0019](./ADR-0019-structured-metrics-observability.md) — observability must stay cheap to collect
* [ADR-0031](./ADR-0031-owner-scoped-background-entity-lookups.md) — background work needs a stated owner
