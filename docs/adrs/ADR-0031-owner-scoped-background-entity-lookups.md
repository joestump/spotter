---
status: proposed
date: 2026-10-25
decision-makers: joestump (pending review)
---

# ADR-0031: Owner-Scoped Entity Lookups On Background Paths

## Context and Problem Statement

Spotter stores every catalog entity under a `User` edge, and the per-user scoping sweep for #338 put an explicit owner predicate on every user-facing read path that resolves an entity by name or by request ID. Two background paths were left out of that sweep because each needed a decision about where the user edge comes from ([#367](https://github.com/joestump/spotter/issues/367)):

* The Lidarr submitter (`internal/services/lidarr_submitter.go`) selected queue rows, then loaded `Album` / `Artist` by the row's bare `entity_id` and derived the notifying user from the loaded entity's own user edge.
* Catalog enrichment (`internal/services/metadata.go`) matched a track by name plus an artist ID that a caller had already loaded under the user predicate.

Neither path leaked data as written: queue rows are created from the requesting user's own entity, and enrichment callers pass an artist they loaded under a user predicate. The exposure is structural. The submitter's row carries no user predicate into its entity loads, so the identity of "who this submission is for" was read out of the entity being submitted rather than out of the row that asked for it; a future producer that writes a queue row from a different source would submit one user's catalog item and notify that user's owner without any code path looking wrong. In enrichment the invariant "the artist is already scoped" lived in the callers rather than in the query, so it held only as long as every caller remembered it.

[ADR-0022](./ADR-0022-threat-model-security-assumptions.md) still documents Spotter as single-user ("the operator is both the administrator and the sole user"), which predates the multi-user Navidrome model #338 assumes; this ADR states the assumption for background work explicitly.

## Decision Drivers

* Every read or write of a catalog entity should be able to state, in the query itself, whose copy it touches — an invariant that lives in callers is one new caller away from being wrong
* A background job has no request context to borrow a user from, so it needs a stored authority for whose work it is doing
* Failing an item silently is worse than failing it visibly: a queue row that quietly disappears is invisible in the metrics ADR-0019 already exposes
* Spotter already has the fail-closed plumbing for permanent errors in the submitter (`isPermanentLidarrError`, which caps `attempts` so a row that can never succeed stops consuming retries)
* No schema change or backfill should be needed to state ownership that the schema already states

## Considered Options

**Where the user edge for the Lidarr queue comes from**

* **Option A: use the queue row's existing `user` edge** — `ent/schema/lidarr_queue.go` has carried a required, unique `user` edge since #296, and `enqueueEntity` already sets it, so the row already records whose submission it is
* **Option B: add a new ownership column or edge and backfill existing rows** — what #367 assumed was needed; a migration plus a backfill that can only guess ownership from the entity the row points at, i.e. from the same data whose trustworthiness is in question
* **Option C: keep deriving the user from the entity being loaded** — no schema work, but the submitter keeps answering "who asked for this?" by asking the object

**What to do when a queue row names an entity its owner does not own**

* **Option A: fail the row permanently** — `status = failed`, `attempts` capped, `last_error` naming the mismatch, logged as a permanent failure
* **Option B: drop the row silently** — keeps the queue clean and the logs quiet, at the cost of a submission that vanishes from every metric
* **Option C: retry it** — treats an ownership mismatch as transient; a row that can never succeed walks the full backoff schedule

**Where the owner predicate belongs for enrichment matching**

* **Option A: in the query** — `track.HasArtistWith(artist.ID(...), artist.HasUserWith(user.ID(u.ID)))`, so the statement names the owner regardless of who issued it
* **Option B: in the caller** — the status quo ante: correct today because each caller scopes its artist load

## Decision Outcome

Chosen options: **A / A / A**.

1. **The queue row's own `user` edge is the authority for background submissions.** No migration, no new edge, no backfill: `tick` resolves the row's owner once and every entity load, writeback, and `SyncEvent` created for that row is scoped to or addressed as that user. A row without a resolvable owner is corrupt and is failed permanently rather than submitted on a guessed user's behalf.
2. **A foreign or stale entity ID fails the row closed and visibly.** The submitter checks ownership before it calls Lidarr, so nothing is submitted and no writeback or notification happens; the error string is added to `isPermanentLidarrError`, which caps `attempts` and leaves `status = failed` with `last_error` naming the mismatch, and the failure surfaces through the existing `metric.lidarr.failed` log with `permanent = true`.
3. **Owner predicates live in the query, not only in the caller.** Entity loads and writebacks on the queue path carry `HasUserWith(user.ID(owner.ID))`; enrichment track matching carries `artist.HasUserWith(user.ID(u.ID))` through the artist edge; `getOrCreateTrack` takes the `*ent.User` its artist and album were resolved under, matching `getOrCreateArtist` and `getOrCreateAlbum`.

Consequences:

* A queue row whose entity was deleted or transferred stops after one attempt instead of either submitting the wrong user's item or walking the backoff schedule, and the reason is readable on the row.
* The submitter no longer needs the entity's user edge at all, so the entity is loaded for its name and MBIDs rather than being trusted to name a user.
* An enrichment artist handed to `getOrCreateTrack` from any path can no longer match a track in another user's catalog; the query rejects it and the row is created under the artist the caller passed.
* ADR-0022's "sole user" deployment assumption is now known-stale for background paths and should be revised when this ADR is accepted.

## Related ADRs

| ADR | Relevance |
|-----|-----------|
| [ADR-0022](./ADR-0022-threat-model-security-assumptions.md) | Threat model this extends; its single-user assumption needs revision |
| [ADR-0029](./ADR-0029-lidarr-submission-rate-limiting.md) | Queue and submitter design this changes the authorization of |
| [ADR-0013](./ADR-0013-goroutine-ticker-background-scheduling.md) | Background goroutines run with full data access — the reason they must scope themselves |
| [ADR-0019](./ADR-0019-structured-metrics-observability.md) | `metric.lidarr.failed` is where a fail-closed ownership mismatch surfaces |

## More Information

* Queue row ownership: `ent/schema/lidarr_queue.go` (`user` edge, required), `internal/enrichers/lidarr/lidarr.go` (`enqueueEntity`)
* Scoped submitter paths: `internal/services/lidarr_submitter.go` (`queueOwner`, `entityOwnedBy`, `submitAlbum`, `updateEntityLidarrID`, `logSubmissionEvent`)
* Scoped enrichment matching: `internal/services/metadata.go` (`getOrCreateTrack`, listen track matching)
* Prior sweep: #368 for #338, which scoped the user-facing read paths
