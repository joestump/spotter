---
status: proposed
date: 2026-10-15
decision-makers: joestump (pending review; proposed by joestump-agent on #327)
---

# ADR-0031: Defer Artist/Album/Track Entity Creation in History Sync to a Dedicated Story

## Context and Problem Statement

SPEC listen-playlist-sync REQ-SYNC-021 states that when persisting a listened
track during history sync the system "MUST create an `Artist`, `Album`, and
`Track` entity if they do not already exist (by name/external ID)" and "MUST
link the `Listen` to the `Track`, `Artist`, `Album`, and `User`".

The implementation has never done the entity half. `persistListens` writes the
denormalized `track_name`/`artist_name`/`album_name` strings on the `Listen`
row (which is what the listen UI renders today), stores the provider track ID
for de-duplication, and leaves the optional `artist`/`album`/`track` edges
unset. Catalog linking through the three-tier matching algorithm
([ADR-0014](./ADR-0014-three-tier-track-matching-algorithm.md)) runs
asynchronously for playlists, not for history-sync batches. Story #327 asks the
question explicitly: implement the clause during history sync, or amend the
spec.

## Decision Drivers

* Running three-tier matching synchronously inside every history-sync batch
  would turn first sync (up to a lookback window of history, potentially tens
  of thousands of plays) into a matching-bound job; per-track failures would
  then abort entity creation too, entangling REQ-SYNC-022's "one track failure
  must not fail the sync" guarantee
* A history-sync play is evidence someone listened, not evidence the recording
  exists in the user's catalog; speculative `Track`/`Album`/`Artist` creation
  from play metadata alone would pollute library entities with unverified,
  provider-formatted names
* The async matcher already owns catalog linking elsewhere, and its correctness
  and performance were recently retuned (#330); folding a second creation path
  into the sync write path duplicates that ownership
* The spec-reconciliation epic #304 exists precisely to align normative text
  with implemented, intended behavior

## Decision

Do not implement entity creation or entity linking in the history-sync write
path now, and amend REQ-SYNC-021 so the clause matches the intended design:

* The `Listen` upsert MUST match by provider + provider track ID + `played_at`
  (unchanged), and MUST link the `Listen` to the `User` (unchanged).
* The denormalized track/artist/album strings remain the persisted record of
  what was played (unchanged behavior).
* Creation of and linking to `Artist`, `Album` and `Track` entities during
  history sync moves from MUST to a separate capability: the system SHOULD
  link listens to catalog entities asynchronously via the track matcher, in a
  dedicated story to be scoped under the spec-reconciliation epic #304.

This ADR is proposed, not accepted: adopting it means merging the matching
amendment to `docs/openspec/specs/listen-playlist-sync/spec.md` (or its
successor under #304) so the normative text no longer demands synchronous
entity creation.

## Consequences

* REQ-SYNC-021's open question is closed for story #327 without widening the
  sync write path; #327 ships the de-duplication and lookback fixes only.
* Listen rows keep no dangling half-linked catalog edges, and first sync keeps
  its current performance envelope.
* If the async linking story later lands under #304, it can promote the SHOULD
  to a MUST without touching the sync path's contract.
* Until this ADR is accepted, the spec and the code disagree on this clause;
  #327's PR says so rather than silently diverging.

## Alternatives Considered

* **Implement entity creation during history sync as written.** Rejected: it
  makes first sync matching-bound, duplicates the matcher's ownership, and
  creates speculative catalog entities from play metadata.
* **Delete the clause outright.** Rejected: catalog-linked listens are a real
  goal; the disagreement is about *when* and *by whom*, not *whether*.
