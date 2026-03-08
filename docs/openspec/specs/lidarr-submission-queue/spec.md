# SPEC-0017: Lidarr Submission Queue with Backpressure

**Status:** proposed
**Version:** 0.1.0
**Last Updated:** 2026-03-04
**Governing ADRs:** ADR-0029 (Lidarr submission rate limiting), ADR-0015 (pluggable enricher registry), ADR-0013 (goroutine ticker scheduling), ADR-0020 (error handling and resilience)

## Overview

When users with large Spotify libraries connect to Spotter, the metadata enrichment pipeline
submits every discovered artist and album to Lidarr in a single enrichment cycle — flooding
Lidarr's download queue and overwhelming upstream indexers. This spec defines a DB-persisted
submission queue that decouples Lidarr submissions from the enrichment pipeline. A background
submitter goroutine wakes on a configurable interval, checks Lidarr's actual queue depth, and
only submits items when the queue is below a configurable cap. Items not yet submitted are
tracked with a `queued` status visible in the UI.

See ADR-0029 for the architectural decision record.

---

## Requirements

### Requirement: Queue Entity Schema

The system SHALL define a `LidarrQueue` Ent entity in `ent/schema/lidarr_queue.go` with the
following fields:

- `entity_type` (enum: `artist`, `album`): the type of entity to submit to Lidarr
- `entity_id` (int): the Ent ID of the Artist or Album entity
- `musicbrainz_id` (string, required, max 255): the MusicBrainz ID used for Lidarr lookup
- `status` (enum: `queued`, `submitted`, `failed`): current submission state
- `attempts` (int, default 0): number of submission attempts made
- `last_error` (string, optional, nillable): error message from the most recent failed attempt
- `retry_at` (time, optional, nillable): earliest time a failed item MAY be retried
- `created_at` (time, immutable): when the item was enqueued
- `updated_at` (time): when the item was last modified

The entity MUST have an edge to `User` (many-to-one, required).

The entity MUST have a unique index on `(entity_type, entity_id, user)` to prevent duplicate
queue entries for the same entity.

#### Scenario: New album discovered during enrichment

- **WHEN** the Lidarr enricher discovers an album not present in Lidarr and the album has a valid MusicBrainz ID
- **THEN** a `LidarrQueue` row SHALL be inserted with `status=queued`, `attempts=0`, and `retry_at=nil`
- **AND** the album's `lidarr_status` field SHALL be set to `"queued"`

#### Scenario: Duplicate queue entry prevented

- **WHEN** the enricher attempts to enqueue an entity that already has a `LidarrQueue` row with `status=queued` or `status=failed`
- **THEN** the system MUST NOT create a duplicate row
- **AND** the existing row SHALL remain unchanged

#### Scenario: Entity already in Lidarr

- **WHEN** the Lidarr enricher finds an entity already present in Lidarr via `findArtist()` or `findAlbum()`
- **THEN** no `LidarrQueue` row SHALL be created
- **AND** the entity SHALL be updated with its `lidarr_id` and status as before (no behavioral change)

### Requirement: Enricher Decoupling

The Lidarr enricher's `EnrichArtist()` and `EnrichAlbum()` methods MUST NOT call `addArtist()`
or `addAlbum()` directly. When an entity is not found in Lidarr and has a valid MusicBrainz ID,
the enricher SHALL insert a `LidarrQueue` row with `status=queued` instead.

The enricher MUST continue to call `findArtist()` and `findAlbum()` synchronously to check
whether entities already exist in Lidarr. Only the submission of *new* entities to Lidarr is
deferred to the queue.

The enricher's `EnrichTrack()` method MUST set `lidarr_status` to `"queued"` for tracks whose
parent album has a pending `LidarrQueue` entry, rather than deriving status from Lidarr album
statistics (which are unavailable for unsubmitted albums).

#### Scenario: Artist not in Lidarr with valid MBID

- **WHEN** `EnrichArtist()` calls `findArtist()` and the artist is not found
- **AND** the artist has a non-empty `musicbrainz_id`
- **THEN** the enricher SHALL insert a `LidarrQueue` row with `entity_type=artist`
- **AND** the enricher SHALL NOT make a POST request to Lidarr's `/api/v1/artist` endpoint

#### Scenario: Album not in Lidarr with valid MBID

- **WHEN** `EnrichAlbum()` calls `findAlbum()` and the album is not found
- **AND** the album has a non-empty `musicbrainz_id`
- **THEN** the enricher SHALL insert a `LidarrQueue` row with `entity_type=album`
- **AND** the enricher SHALL NOT make a POST request to Lidarr's `/api/v1/album` endpoint

#### Scenario: Artist not in Lidarr without MBID

- **WHEN** `EnrichArtist()` is called for an artist without a `musicbrainz_id`
- **THEN** no `LidarrQueue` row SHALL be created
- **AND** the enricher SHALL return `nil, nil` (no data, no error) as it does today

#### Scenario: Track status for queued album

- **WHEN** `EnrichTrack()` runs for a track whose parent album has `lidarr_status = "queued"`
- **THEN** the track's `lidarr_status` SHALL be set to `"queued"`

### Requirement: Background Submitter Goroutine

The system SHALL run a `LidarrSubmitter` background goroutine launched from `cmd/server/main.go`
following the same ticker pattern as existing background loops (ADR-0013). The submitter SHALL
only start if Lidarr is configured (`SPOTTER_LIDARR_BASE_URL` and `SPOTTER_LIDARR_API_KEY` are
non-empty).

The submitter SHALL wake on a configurable interval (`SPOTTER_LIDARR_SUBMIT_INTERVAL`, default
`"30s"`). On each wake cycle, the submitter SHALL:

1. Query the `LidarrQueue` for items with `status=queued` OR (`status=failed` AND `retry_at <= now`)
2. If no eligible items exist, sleep until the next interval (MUST NOT call Lidarr API)
3. If eligible items exist, check Lidarr's queue depth via `GET /api/v1/queue`
4. If queue depth >= `SPOTTER_LIDARR_QUEUE_MAX` (default `20`), log a backpressure metric event and sleep until the next interval
5. If queue depth < cap, submit the oldest eligible item (artists before albums, ordered by `created_at`)
6. On success, update the queue row to `status=submitted` and update the entity's `lidarr_id` and `lidarr_status`
7. On failure, increment `attempts`, set `last_error`, compute `retry_at` using exponential backoff, and set `status=failed`. The submitter SHALL skip the failed item and continue processing remaining eligible items in the current wake cycle.
8. After a successful submission, loop back to step 3 (re-check queue depth) and continue submitting until the cap is reached or the local queue is empty
9. If all eligible items in a wake cycle fail, the submitter SHALL sleep until the next interval

The submitter MUST respect `ctx.Done()` for graceful shutdown (ADR-0018).

#### Scenario: Wake with empty local queue

- **WHEN** the submitter wakes and no `LidarrQueue` rows have `status=queued` or eligible `status=failed`
- **THEN** the submitter MUST NOT make any Lidarr API calls
- **AND** the submitter SHALL sleep until the next interval

#### Scenario: Lidarr queue at capacity

- **WHEN** the submitter wakes with eligible items
- **AND** Lidarr's `GET /api/v1/queue` returns `totalRecords >= SPOTTER_LIDARR_QUEUE_MAX`
- **THEN** the submitter MUST NOT submit any items
- **AND** the submitter SHALL emit a structured log event `metric.lidarr.backpressure` with `queue_depth` and `queue_max` attributes
- **AND** the submitter SHALL sleep until the next interval

#### Scenario: Lidarr queue below capacity

- **WHEN** the submitter wakes with eligible items
- **AND** Lidarr's queue depth < `SPOTTER_LIDARR_QUEUE_MAX`
- **THEN** the submitter SHALL dequeue and submit the oldest eligible item
- **AND** after successful submission, the submitter SHALL re-check Lidarr's queue depth and continue submitting until the cap is reached or the local queue is drained

#### Scenario: Submission succeeds

- **WHEN** the submitter POSTs an artist or album to Lidarr and receives a successful response
- **THEN** the `LidarrQueue` row SHALL be updated to `status=submitted`
- **AND** the corresponding Artist or Album entity SHALL be updated with `lidarr_id` from the response
- **AND** the entity's `lidarr_status` SHALL be updated (e.g., `"monitored"`)
- **AND** a `SyncEvent` SHALL be logged with event type `lidarr_artist_submitted` or `lidarr_album_submitted`

#### Scenario: Submission fails

- **WHEN** the submitter POSTs to Lidarr and receives an error (network, HTTP 4xx/5xx, timeout)
- **THEN** the `LidarrQueue` row SHALL be updated to `status=failed`
- **AND** `attempts` SHALL be incremented
- **AND** `last_error` SHALL be set to a descriptive error message
- **AND** `retry_at` SHALL be set to `now + backoff_duration`
- **AND** the submitter SHALL skip the failed item and continue processing remaining eligible items in the current wake cycle

#### Scenario: Artist submitted before dependent album

- **WHEN** the local queue contains both an artist and albums by that artist
- **THEN** the submitter SHALL submit the artist first (artists before albums, ordered by `created_at`)
- **AND** album submissions for that artist SHALL include the Lidarr artist ID from the artist submission

### Requirement: Backoff Strategy for Failed Submissions

Failed submissions MUST use exponential backoff consistent with ADR-0020. The backoff
duration SHALL be calculated as:

```
delay = min(base * 2^(attempts-1) + jitter, max_delay)
```

Where:
- `base` = 1 minute
- `max_delay` = 1 hour
- `jitter` = random duration in `[0, base)`

A failed item SHALL be retried when `retry_at <= now` during the submitter's next wake cycle.

The system SHOULD cap maximum attempts at 10. After 10 failed attempts, the item MUST remain
in `status=failed` and MUST NOT be automatically retried. The user MAY manually retry via a
future UI action.

#### Scenario: First failure

- **WHEN** a submission fails for the first time (`attempts` goes from 0 to 1)
- **THEN** `retry_at` SHALL be approximately `now + 1 minute` (plus jitter)

#### Scenario: Third failure

- **WHEN** a submission fails for the third time (`attempts` goes from 2 to 3)
- **THEN** `retry_at` SHALL be approximately `now + 4 minutes` (plus jitter)

#### Scenario: Max attempts exceeded

- **WHEN** a submission has failed 10 times
- **THEN** the item MUST remain `status=failed` with `attempts=10`
- **AND** the submitter MUST NOT retry it automatically

### Requirement: Configuration

The system SHALL support the following configuration keys via Viper (ADR-0009):

| Config Key | Env Var | Type | Default | Description |
|---|---|---|---|---|
| `lidarr.queue_max` | `SPOTTER_LIDARR_QUEUE_MAX` | int | `20` | Maximum Lidarr queue depth before backpressure pauses submissions |
| `lidarr.submit_interval` | `SPOTTER_LIDARR_SUBMIT_INTERVAL` | duration | `"30s"` | How often the submitter wakes to check and attempt to drain |

These keys SHALL be added to the `Lidarr` config struct alongside the existing `BaseURL` and
`APIKey` fields.

#### Scenario: Default configuration

- **WHEN** no `SPOTTER_LIDARR_QUEUE_MAX` or `SPOTTER_LIDARR_SUBMIT_INTERVAL` is set
- **THEN** the submitter SHALL use a queue cap of 20 and a wake interval of 30 seconds

#### Scenario: Custom configuration

- **WHEN** `SPOTTER_LIDARR_QUEUE_MAX=50` and `SPOTTER_LIDARR_SUBMIT_INTERVAL=1m` are set
- **THEN** the submitter SHALL pause when Lidarr has 50 or more queued items and wake every 60 seconds

### Requirement: UI Queued Status

The track status display (`internal/views/components/track_status.templ`) MUST render a
distinct visual state for `lidarr_status = "queued"`:

- Icon: `icon-[heroicons--queue-list]` (or equivalent queue/list icon)
- Badge color: `badge-warning` (matching the "pending" palette but with a distinct icon)
- Tooltip: `"Queued for Lidarr"`
- The icon MUST be visually distinct from the existing `"pending"` status (which means "submitted to Lidarr but not yet downloaded")

The `"queued"` status represents items Spotter intends to submit but has not yet sent to Lidarr.
The existing `"pending"` status represents items already in Lidarr awaiting download.

#### Scenario: Track with queued album displayed

- **WHEN** a track's `lidarr_status` is `"queued"`
- **THEN** the UI SHALL show the queue-list icon with `badge-warning` styling
- **AND** the tooltip SHALL read `"Queued for Lidarr"`
- **AND** no Lidarr deep-link SHALL be rendered (the entity is not yet in Lidarr)

#### Scenario: Track transitions from queued to monitored

- **WHEN** the background submitter successfully submits the parent album to Lidarr
- **AND** the next `EnrichTrack()` cycle runs
- **THEN** the track's `lidarr_status` SHALL change from `"queued"` to the appropriate Lidarr-derived status (e.g., `"monitored"`)

### Requirement: Queue Cleanup

Completed queue entries (`status=submitted`) older than 7 days SHOULD be deleted during the
submitter's wake cycle (or a dedicated cleanup pass). Failed entries that have exceeded the
maximum retry count (10 attempts) SHOULD be retained for operator inspection but MAY be
cleaned up after 30 days.

#### Scenario: Old submitted entries pruned

- **WHEN** the submitter wakes and finds `LidarrQueue` rows with `status=submitted` and `updated_at < now - 7 days`
- **THEN** those rows SHALL be deleted

#### Scenario: Permanently failed entries retained

- **WHEN** a `LidarrQueue` row has `status=failed` and `attempts >= 10`
- **THEN** the row SHALL be retained for at least 30 days for operator inspection

### Requirement: Observability

The submitter SHALL emit structured log events (ADR-0019) for the following operations:

- `metric.lidarr.submitted` — on successful submission, with `entity_type`, `entity_id`, `musicbrainz_id`, `duration_ms`
- `metric.lidarr.backpressure` — when submissions are paused due to queue cap, with `queue_depth`, `queue_max`, `local_pending` (number of queued items in local DB)
- `metric.lidarr.failed` — on submission failure, with `entity_type`, `entity_id`, `error`, `attempts`
- `metric.lidarr.queue_drained` — when a wake cycle completes, with `submitted_count`, `skipped_count`, `remaining_count`

#### Scenario: Backpressure logged

- **WHEN** the submitter detects Lidarr queue depth >= cap
- **THEN** an `slog.Info("metric.lidarr.backpressure", ...)` event SHALL be emitted with `queue_depth`, `queue_max`, and `local_pending` attributes

#### Scenario: Submission success logged

- **WHEN** an item is successfully submitted to Lidarr
- **THEN** an `slog.Info("metric.lidarr.submitted", ...)` event SHALL be emitted with `entity_type`, `entity_id`, `musicbrainz_id`, and `duration_ms` attributes
