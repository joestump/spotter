---
status: proposed
date: 2026-11-07
decision-makers: joestump-agent (proposed; issue #339 left "cascade or block-with-message" open)
---

# ADR-0035: A DJ That Still Owns Mixtapes Is Refused, Not Cascade-Deleted

## Context and Problem Statement

The `DJ.mixtapes` edge declares no `OnDelete` behaviour, so each mixtape's NOT NULL foreign key to its DJ is enforced with NO ACTION, and Spotter opens SQLite with `_fk=1`. `DeleteDJ` called `DeleteOneID` directly, so deleting a DJ that still owned any mixtape produced a constraint error and a bare 500, and the DJ simply could not be removed ([issue #339](https://github.com/joestump/spotter/issues/339)).

The DJ list had already taken a position: `showDeleteModal` in `internal/views/vibes/djs.templ` counts `dj.Edges.Mixtapes` and opens a "cannot delete" dialog with the count instead of a confirm. The server was the only place this failed silently, and it still failed on a stale page or a direct request.

## Decision Drivers

* A mixtape is something the user created; deleting a persona must not be able to destroy one as a side effect.
* The complaint was a 500 with no reason, not the refusal itself: any fix has to say what to do next.
* The UI already refuses, so the server should agree with it rather than offer a different contract.
* Cascade would be an auto-migration change to the schema, not just a handler change.

## Considered Options

* **Cascade at the schema** (`OnDelete: edge.Cascade` on `DJ.mixtapes`). Deleting a DJ silently deletes its mixtapes, including any Navidrome playlist they generated, which is left behind as an orphan. Rejected: silent data loss.
* **Delete the mixtapes in the handler first.** Same data loss, more code, and no confirmation the user asked for. Rejected for the same reason.
* **Refuse with the count and keep the existing UI flow.** Nothing is destroyed; the user deletes or reassigns the mixtapes they care about and then removes the DJ.

## Decision Outcome

**Option C.** `DeleteDJ` counts the DJ's mixtapes scoped to the requesting user before it deletes anything. With a count above zero it publishes an error notification, answers `409 Conflict` with "Delete or reassign their N mixtape(s) first", and leaves both rows intact. A DJ with no mixtapes deletes exactly as before.

## Consequences

**Positive**

* The DJ list and the server now refuse the same request for the same stated reason, and a stale page no longer produces a 500.
* No user data is destroyed by a persona deletion, so a mistaken click is recoverable.

**Negative**

* `409` is new to Spotter's handler surface. HTMX treats it as a failed swap, which is why the reason also travels as an SSE notification rather than only in the body.
* A "delete this DJ and its mixtapes" affordance stays unbuilt. It needs its own decision about the Navidrome playlists those mixtapes produced.

## References

* [issue #339](https://github.com/joestump/spotter/issues/339)
* ADR-0004 (Ent ORM and its generated constraints)
* ADR-0007 (in-memory event bus, notification payloads)
* `internal/handlers/vibes.go` (`DeleteDJ`), `internal/views/vibes/djs.templ` (`showDeleteModal`)
