---
status: proposed
date: 2026-10-28
decision-makers: pending maintainer decision (issue #349 asked a lane to implement or amend)
---

# ADR-0032: Entity Tags Projection Is Repaired By Re-Upsert, Not By Same-Transaction Writes

## Context and Problem Statement

[ADR-0025](./ADR-0025-unified-tag-taxonomy.md) and SPEC-0014 REQ "Denormalized Entity Tags Table" say that when a tag-entity association is created or removed through an Ent edge, the matching `entity_tags` row SHALL be created or deleted in the same transaction.

Issue #349 confirmed the implementation does not do that: `internal/tags/upsert.go` writes the Ent edge through the generated client and the `entity_tags` row through a separate raw `database/sql` connection, so the two statements commit independently. A crash or a failed statement between them leaves the two stores out of step.

Doing it literally is not available with the tooling the repo has today:

* Ent v0.14.5 generates no raw statement API on `*ent.Tx` (it exposes only `Commit`, `Rollback`, `Client()`), and the code generator runs without the `interceptf` or `sql/execquery` features (`ent/generate.go`), so there is no way to run the `INSERT` on the connection the transaction already holds.
* The only way around that is to build a throwaway `ent.Client` around a hand-opened `*sql.Tx` (`entsql.NewDriver` with a transaction adapter). That client would not carry the hooks registered by `internal/database.RegisterEncryptionHooks`, and constructing a client per write is a much larger correctness surface than the drift it prevents.
* The default database is SQLite opened with `cache=shared`, where two write connections touching the same table are told `SQLITE_LOCKED` instead of waiting. Holding an Ent transaction open while a second connection writes to `entity_tags` is therefore actively harmful on the default configuration.

## Decision Drivers

* Tag writes must converge, not necessarily commit atomically: `tags.UpsertTagsForEntity` is idempotent, so any later call for the same entity repairs a missing projection row.
* Nothing reads `entity_tags` yet. As of this writing the table has exactly one writer (`internal/tags/upsert.go`) and no reader in `internal/`, so drift cannot yet reach a user through tag browsing or filtering.
* Every tag write now goes through one helper (`services.MetadataService.PersistTypedTags`), so the convergence rule lives in one place instead of per call site.

## Considered Options

* **Option 1: One Ent transaction covering both writes.** Correct, but not expressible with the generated API (above). Blocked, not chosen.
* **Option 2: Bind a temporary Ent client to a hand-opened `*sql.Tx`.** Atomic, but bypasses the registered encryption hooks and rebuilds a client per call. Rejected.
* **Option 3: Commit the Ent edge first, then insert the projection row idempotently, and repair drift with any later upsert.** Chosen: it keeps the source of truth (the Ent junction tables) authoritative, keeps one connection per statement, and makes the failure loud instead of silent.
* **Option 4: Stop maintaining `entity_tags` until a reader exists.** Rejected; the table and its DDL already shipped in #307 and removing its maintenance would be a larger design reversal than #349 asks for.

## Decision Outcome

Chosen option 3. The contract SPEC-0014 REQ "Denormalized Entity Tags Table" is amended to state in this PR is:

1. The Ent edge write commits first and is authoritative.
2. The `entity_tags` row is written immediately afterwards with `INSERT ... ON CONFLICT (tag_id, entity_type, entity_id) DO NOTHING`, so the projection converges on every write.
3. A projection failure is logged with the entity and tag named, never swallowed: at error level on the user-triggered regenerate path, where the request exists precisely to publish those tags, and at warn level inside the background enrichment cycle, which re-runs the upsert on its next pass.
4. A missing row is repaired by the next `UpsertTagsForEntity` for that entity: the background enrichment cycle, a "regenerate AI" request, or the tag backfill all re-run it.

The removal half of the original REQ stays unimplemented, on purpose: no code path removes a tag-entity edge today (nothing calls `RemoveArtistIDs`, `RemoveAlbumIDs`, `RemoveTrackIDs` or their `Clear` equivalents on `Tag`), so there is no removal to mirror. Whoever adds tag removal, or deletes artists, albums or tracks outright, must delete the matching `entity_tags` rows in the same change and move this ADR to `accepted` with that rule recorded.

## Consequences

* Positive: no second write connection, no hook bypass, no new lock contention on the default SQLite configuration; every write path converges the projection through one helper.
* Positive: a projection failure is now visible in the log instead of being a `Warn` inside an enrichment loop.
* Negative: a process crash between the two statements can leave an entity missing from `entity_tags` until it is next enriched, regenerated or backfilled. Reads must tolerate that until this ADR is superseded.
* Negative: the guarantee is dialect-dependent. The `ON CONFLICT` form is PostgreSQL and SQLite syntax; MySQL compatibility is tracked separately in #346 and is not changed here.
* Open: the table has no reader yet. Whoever adds the first reader decides whether the drift window needs a reconciliation job, and should revisit this record at that point.
