---
status: proposed
date: 2026-11-01
decision-makers: joestump-agent (proposed; issue #333 review confirmed the defects)
---

# ADR-0033: External API client request-construction rules

## Context and Problem Statement

Review of the external API clients (issue #333, part of epic #318) found four
client-side defects that were not behavior gaps but construction gaps: values
interpolated straight into Lucene queries, query parameters interpolated with
`fmt.Sprintf`, a response shape assumed to always be an array, and image
downloads issued through `http.Get` on the default client. Each one is invisible
on the happy path and breaks on real-world data ("AC/DC", a one-scrobble
account, a stalled image host).

The existing records cover the surrounding policy — [ADR-0020](./ADR-0020-exponential-backoff-jitter.md)
for error classification and backoff, [ADR-0027](./ADR-0027-image-storage-filesystem.md)
for where images live, [ADR-0015](./ADR-0015-pluggable-enricher-registry-pattern.md)
for the enricher shape — but none states how an outbound request must be
built. This record proposes those rules so the same class of defect stays out.

## Decision Drivers

* MusicBrainz search is Lucene-flavoured: unquoted values 400 on reserved
  characters and bind only the first token of a multi-word name
* Query parameters carrying credentials or URLs must not be able to alter the
  query they travel in
* Last.fm serves XML-derived JSON, where a single-element list arrives as a
  bare object rather than a one-item array
* A background enrichment run must not depend on a third-party host's health;
  the default `http.Client` has no timeout at all

## Decision

1. **Quote and escape every user-derived value in a MusicBrainz query.**
   Values are wrapped in double quotes with embedded `"` and `\` backslash-
   escaped (`escapeLuceneValue`), so "AC/DC" is valid Lucene and multi-word
   names phrase-bind. Field names and `AND` stay outside the quotes.
2. **Build query strings with `net/url.Values`, never string interpolation.**
   Applies to any URL carrying an API key, a session key, or a redirect target.
3. **Tolerate both list shapes when decoding XML-derived JSON.** A custom
   `UnmarshalJSON` accepts an array, a bare object, and `null`, so a
   single-track page decodes as a one-item list.
4. **Every outbound download is context-bound and timeout-bounded.** Shared
   helpers take a `context.Context` and use a package client with an explicit
   timeout (30s for image downloads, matching the per-enricher clients);
   `http.Get` on the default client is not used.

## Consequences

* Searches that previously 400 or silently matched the wrong artist now return
  results; search strictness rises slightly because a quoted phrase must match
  as a phrase.
* The 30s bound makes a stalled image host a logged per-image failure instead
  of a wedged enrichment run; cancellation now propagates through ctx.
* Callers of `DownloadAndSaveImage` must hold a context, which every enricher
  call path already does.
* Rules 1 and 3 are pinned by tests (`TestEscapeLuceneValue`,
  `TestTolerantTrackList`) so a future client cannot reintroduce them.
