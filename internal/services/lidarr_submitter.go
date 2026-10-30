// Governing: SPEC-0017 REQ "Background Submitter Goroutine", REQ "Backoff Strategy", REQ "Observability", ADR-0029, ADR-0013

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/lidarrqueue"
	"spotter/ent/syncevent"
	"spotter/ent/user"
	"spotter/internal/config"

	"entgo.io/ent/dialect/sql"
)

const (
	// maxAttempts is the maximum number of submission attempts before giving up.
	// Governing: SPEC-0017 REQ "Backoff Strategy"
	maxAttempts = 10

	// maxBackoff is the maximum backoff delay.
	// Governing: SPEC-0017 REQ "Backoff Strategy"
	maxBackoff = 1 * time.Hour

	// baseBackoff is the base delay for exponential backoff.
	// Governing: SPEC-0017 REQ "Backoff Strategy"
	baseBackoff = 1 * time.Minute

	// maxJitter is the maximum random jitter added to backoff.
	// Governing: SPEC-0017 REQ "Backoff Strategy"
	maxJitter = 1 * time.Minute
)

// LidarrSubmitter drains the LidarrQueue table at a controlled rate,
// submitting items to Lidarr's API and using queue depth as backpressure.
// Governing: SPEC-0017 REQ "Background Submitter Goroutine", ADR-0029, ADR-0013
type LidarrSubmitter struct {
	db         *ent.Client
	config     *config.Config
	logger     *slog.Logger
	httpClient *http.Client
}

// NewLidarrSubmitter creates a new LidarrSubmitter.
func NewLidarrSubmitter(db *ent.Client, cfg *config.Config, logger *slog.Logger) *LidarrSubmitter {
	return &LidarrSubmitter{
		db:     db,
		config: cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// lidarrQueueResponse represents the Lidarr API queue response.
type lidarrQueueResponse struct {
	TotalRecords int `json:"totalRecords"`
}

// lidarrSubmitArtist represents the payload/response for adding an artist.
type lidarrSubmitArtist struct {
	ID              int    `json:"id"`
	ArtistName      string `json:"artistName"`
	ForeignArtistID string `json:"foreignArtistId"`
}

// lidarrSubmitAlbum represents the payload/response for adding an album.
type lidarrSubmitAlbum struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	ForeignAlbumID string `json:"foreignAlbumId"`
}

// lidarrRootFolder represents a Lidarr root folder.
type lidarrRootFolder struct {
	Path string `json:"path"`
}

// lidarrProfile represents a quality or metadata profile.
type lidarrProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Run starts the main ticker loop for the submitter.
// It wakes every SubmitInterval, queries for eligible items, and submits them.
// Governing: SPEC-0017 REQ "Background Submitter Goroutine", ADR-0018 (graceful shutdown)
func (s *LidarrSubmitter) Run(ctx context.Context) {
	interval, err := time.ParseDuration(s.config.Lidarr.SubmitInterval)
	if err != nil {
		s.logger.Error("invalid lidarr submit interval, using default 3m", "error", err, "value", s.config.Lidarr.SubmitInterval)
		interval = 3 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("lidarr submitter started", "interval", interval, "queue_max", s.config.Lidarr.QueueMax)

	// Governing: SPEC graceful-shutdown REQ-CTX-001 (select on ctx.Done vs ticker.C)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("lidarr submitter shutting down")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick performs one wake cycle of the submitter.
func (s *LidarrSubmitter) tick(ctx context.Context) {
	// Governing: SPEC-0017 REQ "Queue Cleanup", ADR-0029
	// Use consolidated CleanupLidarrQueue which handles both submitted and permanently-failed entries.
	if err := CleanupLidarrQueue(ctx, s.db); err != nil {
		s.logger.Error("failed to cleanup lidarr queue", "error", err)
	}

	submittedCount := 0
	skippedCount := 0

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Query for eligible items: status=queued OR (status=failed AND retry_at <= now AND attempts < maxAttempts)
		// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
		now := time.Now()
		items, err := s.db.LidarrQueue.Query().
			Where(
				lidarrqueue.Or(
					lidarrqueue.StatusEQ(lidarrqueue.StatusQueued),
					lidarrqueue.And(
						lidarrqueue.StatusEQ(lidarrqueue.StatusFailed),
						lidarrqueue.RetryAtLTE(now),
						lidarrqueue.AttemptsLT(maxAttempts),
					),
				),
			).
			// Artists before albums ("artist" > "album" lexicographically, so descending puts artists first)
			// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
			Order(
				lidarrqueue.ByEntityType(sql.OrderDesc()),
				lidarrqueue.ByCreatedAt(sql.OrderAsc()),
			).
			Limit(1).
			All(ctx)
		if err != nil {
			s.logger.Error("failed to query lidarr queue", "error", err)
			return
		}

		if len(items) == 0 {
			// No eligible items, done for this tick
			break
		}

		item := items[0]

		// The queue row's own user edge is the only authority for who a
		// submission is made for. Entity edges are addressed by bare ID below,
		// so they must never be trusted to name the requesting user.
		// Governing: ADR-0031 (proposed), issue #367
		owner, ownerErr := s.queueOwner(ctx, item)
		if ownerErr != nil {
			s.logger.Error("metric.lidarr.failed",
				"entity_type", item.EntityType,
				"entity_id", item.EntityID,
				"item_id", item.ID,
				"error", ownerErr.Error(),
				"attempts", maxAttempts,
				"permanent", true,
			)
			if err := s.db.LidarrQueue.UpdateOneID(item.ID).
				SetAttempts(maxAttempts).
				SetLastError(ownerErr.Error()).
				SetStatus(lidarrqueue.StatusFailed).
				Exec(ctx); err != nil {
				s.logger.Error("failed to update queue item after owner lookup failure", "error", err, "item_id", item.ID)
			}
			skippedCount++
			continue
		}

		// Check Lidarr queue depth before submitting
		// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
		depth, err := s.getQueueDepth(ctx)
		if err != nil {
			s.logger.Error("failed to get lidarr queue depth", "error", err)
			return
		}

		queueMax := s.config.Lidarr.QueueMax
		if queueMax <= 0 {
			queueMax = 50
		}

		// Count remaining eligible items for metrics
		remaining, _ := s.db.LidarrQueue.Query().
			Where(
				lidarrqueue.Or(
					lidarrqueue.StatusEQ(lidarrqueue.StatusQueued),
					lidarrqueue.And(
						lidarrqueue.StatusEQ(lidarrqueue.StatusFailed),
						lidarrqueue.RetryAtLTE(now),
						lidarrqueue.AttemptsLT(maxAttempts),
					),
				),
			).
			Count(ctx)

		if depth >= queueMax {
			// Governing: SPEC-0017 REQ "Observability" — metric.lidarr.backpressure
			s.logger.Info("metric.lidarr.backpressure",
				"queue_depth", depth,
				"queue_max", queueMax,
				"local_pending", remaining,
			)
			skippedCount += remaining
			break
		}

		// Submit the item
		start := time.Now()
		lidarrID, submitErr := s.submitItem(ctx, item, owner)
		duration := time.Since(start)

		if submitErr != nil {
			// Governing: SPEC-0017 REQ "Backoff Strategy", REQ "Observability" — metric.lidarr.failed
			newAttempts := item.Attempts + 1
			errMsg := submitErr.Error()

			// Detect permanently-failed submissions that will never succeed on retry.
			// These are set to maxAttempts so they won't be retried.
			permanent := isPermanentLidarrError(errMsg)
			if permanent {
				newAttempts = maxAttempts
			}

			s.logger.Info("metric.lidarr.failed",
				"entity_type", item.EntityType,
				"entity_id", item.EntityID,
				"error", errMsg,
				"attempts", newAttempts,
				"permanent", permanent,
			)

			update := s.db.LidarrQueue.UpdateOneID(item.ID).
				SetAttempts(newAttempts).
				SetLastError(errMsg).
				SetStatus(lidarrqueue.StatusFailed)

			if newAttempts < maxAttempts {
				retryAt := ComputeBackoff(newAttempts)
				update = update.SetRetryAt(retryAt)
			}
			// If maxAttempts reached (or permanent failure), leave status=failed with no retry_at (won't be retried)

			if err := update.Exec(ctx); err != nil {
				s.logger.Error("failed to update queue item after failure", "error", err, "item_id", item.ID)
			}
			skippedCount++
			// Continue to next item rather than stopping
			continue
		}

		// Success
		// Governing: SPEC-0017 REQ "Observability" — metric.lidarr.submitted

		s.logger.Info("metric.lidarr.submitted",
			"entity_type", item.EntityType,
			"entity_id", item.EntityID,
			"musicbrainz_id", item.MusicbrainzID,
			"duration_ms", duration.Milliseconds(),
		)

		// Update queue row: status=submitted
		if err := s.db.LidarrQueue.UpdateOneID(item.ID).
			SetStatus(lidarrqueue.StatusSubmitted).
			Exec(ctx); err != nil {
			s.logger.Error("failed to update queue item after success", "error", err, "item_id", item.ID)
		}

		// Update the entity's lidarr_id and lidarr_status
		// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
		s.updateEntityLidarrID(ctx, item, owner, lidarrID)

		// Log SyncEvent for successful submission
		// Governing: SPEC-0017 REQ "Observability"
		s.logSubmissionEvent(ctx, item, owner, lidarrID)

		submittedCount++
	}

	// Governing: SPEC-0017 REQ "Observability" — metric.lidarr.queue_drained
	if submittedCount > 0 || skippedCount > 0 {
		remaining, _ := s.db.LidarrQueue.Query().
			Where(
				lidarrqueue.Or(
					lidarrqueue.StatusEQ(lidarrqueue.StatusQueued),
					lidarrqueue.And(
						lidarrqueue.StatusEQ(lidarrqueue.StatusFailed),
						lidarrqueue.AttemptsLT(maxAttempts),
					),
				),
			).
			Count(ctx)

		s.logger.Info("metric.lidarr.queue_drained",
			"submitted_count", submittedCount,
			"skipped_count", skippedCount,
			"remaining_count", remaining,
		)
	}
}

// getQueueDepth queries Lidarr's API for the current download queue depth.
// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
func (s *LidarrSubmitter) getQueueDepth(ctx context.Context) (int, error) {
	var result lidarrQueueResponse
	if err := s.doRequest(ctx, "GET", "queue?page=1&pageSize=1", nil, &result); err != nil {
		return 0, err
	}
	return result.TotalRecords, nil
}

// queueOwner resolves the user that owns a queue row. The row's own user edge
// is required by the schema (ent/schema/lidarr_queue.go), so a row without one
// is corrupt and must not be submitted on anyone's behalf.
// Governing: ADR-0031 (proposed), issue #367
func (s *LidarrSubmitter) queueOwner(ctx context.Context, item *ent.LidarrQueue) (*ent.User, error) {
	owner, err := item.QueryUser().Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("queue row has no owner (item %d): %w", item.ID, err)
	}
	return owner, nil
}

// entityOwnedBy reports whether the entity a queue row names belongs to the row's
// owner. It is the fail-closed gate that keeps a foreign or stale entity ID from
// reaching Lidarr or a writeback.
// Governing: ADR-0031 (proposed), issue #367
func (s *LidarrSubmitter) entityOwnedBy(ctx context.Context, item *ent.LidarrQueue, owner *ent.User) (bool, error) {
	switch item.EntityType {
	case lidarrqueue.EntityTypeArtist:
		return s.db.Artist.Query().
			Where(artist.ID(item.EntityID), artist.HasUserWith(user.ID(owner.ID))).
			Exist(ctx)
	case lidarrqueue.EntityTypeAlbum:
		return s.db.Album.Query().
			Where(album.ID(item.EntityID), album.HasUserWith(user.ID(owner.ID))).
			Exist(ctx)
	default:
		return false, nil
	}
}

// submitItem submits a queue item to Lidarr on behalf of the queue row's owner.
// Returns the Lidarr ID on success.
// Governing: SPEC-0017 REQ "Background Submitter Goroutine", ADR-0031 (proposed)
func (s *LidarrSubmitter) submitItem(ctx context.Context, item *ent.LidarrQueue, owner *ent.User) (int, error) {
	owned, err := s.entityOwnedBy(ctx, item, owner)
	if err != nil {
		return 0, fmt.Errorf("failed to verify entity ownership: %w", err)
	}
	if !owned {
		return 0, fmt.Errorf("%s %d does not belong to the queue owner (user %d)", item.EntityType, item.EntityID, owner.ID)
	}

	switch item.EntityType {
	case lidarrqueue.EntityTypeArtist:
		return s.submitArtist(ctx, item, owner)
	case lidarrqueue.EntityTypeAlbum:
		return s.submitAlbum(ctx, item, owner)
	default:
		return 0, fmt.Errorf("unknown entity type: %s", item.EntityType)
	}
}

// submitArtist adds an artist to Lidarr by MusicBrainz ID. The submission is
// keyed on the queue row's own MusicBrainz ID, so no local entity is loaded.
func (s *LidarrSubmitter) submitArtist(ctx context.Context, item *ent.LidarrQueue, owner *ent.User) (int, error) {
	// Lookup artist in Lidarr
	u := fmt.Sprintf("artist/lookup?term=lidarr:%s", item.MusicbrainzID)
	var results []lidarrSubmitArtist
	if err := s.doRequest(ctx, "GET", u, nil, &results); err != nil {
		return 0, fmt.Errorf("artist lookup failed: %w", err)
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("artist not found in lidarr lookup for mbid %s", item.MusicbrainzID)
	}

	artistToAdd := results[0]

	// Validate that the artist has a name — some MusicBrainz entries return
	// empty names from Lidarr's lookup, which will fail Lidarr's validation.
	if artistToAdd.ArtistName == "" {
		return 0, fmt.Errorf("artist lookup returned empty name for mbid %s", item.MusicbrainzID)
	}

	// Get root folder
	var rootFolders []lidarrRootFolder
	if err := s.doRequest(ctx, "GET", "rootfolder", nil, &rootFolders); err != nil {
		return 0, fmt.Errorf("failed to get root folders: %w", err)
	}
	if len(rootFolders) == 0 {
		return 0, fmt.Errorf("no root folder configured in lidarr")
	}

	// Build payload
	// Set monitored=true but disable automatic search to avoid flooding
	// Lidarr's download queue. Albums are submitted individually via the
	// album queue entries, which provides controlled backpressure.
	payload := map[string]interface{}{
		"artistName":        artistToAdd.ArtistName,
		"foreignArtistId":   artistToAdd.ForeignArtistID,
		"qualityProfileId":  1,
		"metadataProfileId": 1,
		"path":              fmt.Sprintf("%s/%s", rootFolders[0].Path, artistToAdd.ArtistName),
		"monitored":         true,
		"addOptions": map[string]interface{}{
			"monitor":                "none",
			"searchForMissingAlbums": false,
		},
	}

	// Get quality profile
	var qualityProfiles []lidarrProfile
	if err := s.doRequest(ctx, "GET", "qualityprofile", nil, &qualityProfiles); err == nil && len(qualityProfiles) > 0 {
		payload["qualityProfileId"] = qualityProfiles[0].ID
	}

	// Get metadata profile
	var metadataProfiles []lidarrProfile
	if err := s.doRequest(ctx, "GET", "metadataprofile", nil, &metadataProfiles); err == nil && len(metadataProfiles) > 0 {
		payload["metadataProfileId"] = metadataProfiles[0].ID
	}

	var newArtist lidarrSubmitArtist
	if err := s.doRequest(ctx, "POST", "artist", payload, &newArtist); err != nil {
		// Check if artist already exists
		if strings.Contains(err.Error(), "ArtistExistsValidator") || strings.Contains(err.Error(), "already been added") {
			// Fetch existing artist
			existing, fetchErr := s.fetchArtistByMBID(ctx, item.MusicbrainzID)
			if fetchErr != nil {
				return 0, fetchErr
			}
			return existing.ID, nil
		}
		return 0, fmt.Errorf("failed to add artist: %w", err)
	}

	return newArtist.ID, nil
}

// fetchArtistByMBID fetches an existing artist from Lidarr by MusicBrainz ID.
func (s *LidarrSubmitter) fetchArtistByMBID(ctx context.Context, mbid string) (*lidarrSubmitArtist, error) {
	var artists []lidarrSubmitArtist
	if err := s.doRequest(ctx, "GET", "artist", nil, &artists); err != nil {
		return nil, err
	}
	for _, a := range artists {
		if a.ForeignArtistID == mbid {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("artist with mbid %s not found in lidarr despite existing", mbid)
}

// submitAlbum adds an album to Lidarr by MusicBrainz ID.
// The album and its artist are loaded under the queue owner's predicate, so the
// owner invariant lives in the query rather than in the caller.
// Governing: ADR-0031 (proposed), issue #367
func (s *LidarrSubmitter) submitAlbum(ctx context.Context, item *ent.LidarrQueue, owner *ent.User) (int, error) {
	// First, ensure the artist is in Lidarr. Load album scoped to the owner.
	albumEntity, err := s.db.Album.Query().
		Where(
			album.ID(item.EntityID),
			album.HasUserWith(user.ID(owner.ID)),
		).
		Only(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load album entity: %w", err)
	}

	artistEntity, err := s.db.Artist.Query().
		Where(
			artist.HasUserWith(user.ID(owner.ID)),
			artist.HasAlbumsWith(album.ID(albumEntity.ID)),
		).
		Only(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load artist for album: %w", err)
	}

	// Find artist in Lidarr (they should already be there)
	var artistID int
	if artistEntity.MusicbrainzID != "" {
		existing, fetchErr := s.fetchArtistByMBID(ctx, artistEntity.MusicbrainzID)
		if fetchErr == nil {
			artistID = existing.ID
		}
	}
	if artistID == 0 {
		return 0, fmt.Errorf("artist not found in lidarr for album submission")
	}

	// Lookup album
	u := fmt.Sprintf("album/lookup?term=lidarr:%s", item.MusicbrainzID)
	var results []lidarrSubmitAlbum
	if err := s.doRequest(ctx, "GET", u, nil, &results); err != nil {
		return 0, fmt.Errorf("album lookup failed: %w", err)
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("album not found in lidarr lookup for mbid %s", item.MusicbrainzID)
	}

	albumToAdd := results[0]

	payload := map[string]interface{}{
		"title":          albumToAdd.Title,
		"foreignAlbumId": albumToAdd.ForeignAlbumID,
		"artistId":       artistID,
		"monitored":      true,
		"addOptions": map[string]interface{}{
			"searchForNewAlbum": true,
		},
	}

	var newAlbum lidarrSubmitAlbum
	if err := s.doRequest(ctx, "POST", "album", payload, &newAlbum); err != nil {
		// Check if album already exists
		if strings.Contains(err.Error(), "AlbumExistsValidator") || strings.Contains(err.Error(), "already been added") {
			existing, fetchErr := s.fetchAlbumByMBID(ctx, item.MusicbrainzID, artistID)
			if fetchErr != nil {
				return 0, fetchErr
			}
			return existing.ID, nil
		}
		return 0, fmt.Errorf("failed to add album: %w", err)
	}

	return newAlbum.ID, nil
}

// fetchAlbumByMBID fetches an existing album from Lidarr by MusicBrainz ID.
func (s *LidarrSubmitter) fetchAlbumByMBID(ctx context.Context, mbid string, artistID int) (*lidarrSubmitAlbum, error) {
	u := fmt.Sprintf("album?artistId=%d", artistID)
	var albums []lidarrSubmitAlbum
	if err := s.doRequest(ctx, "GET", u, nil, &albums); err != nil {
		return nil, err
	}
	for _, a := range albums {
		if a.ForeignAlbumID == mbid {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("album with mbid %s not found in lidarr despite existing", mbid)
}

// updateEntityLidarrID updates the lidarr_id and lidarr_status fields on the
// corresponding artist or album entity. The update carries the queue owner's
// predicate, so a queue row can never write another user's catalog row.
// Governing: SPEC-0017 REQ "Background Submitter Goroutine", ADR-0031 (proposed)
func (s *LidarrSubmitter) updateEntityLidarrID(ctx context.Context, item *ent.LidarrQueue, owner *ent.User, lidarrID int) {
	lidarrIDStr := fmt.Sprintf("%d", lidarrID)
	switch item.EntityType {
	case lidarrqueue.EntityTypeArtist:
		n, err := s.db.Artist.Update().
			Where(artist.ID(item.EntityID), artist.HasUserWith(user.ID(owner.ID))).
			SetLidarrID(lidarrIDStr).
			SetLidarrStatus("monitored").
			Save(ctx)
		if err != nil {
			s.logger.Error("failed to update artist lidarr_id", "error", err, "entity_id", item.EntityID)
		} else if n == 0 {
			s.logger.Error("refused to update lidarr_id on an artist the queue owner does not own",
				"entity_id", item.EntityID, "owner_id", owner.ID)
		}
	case lidarrqueue.EntityTypeAlbum:
		n, err := s.db.Album.Update().
			Where(album.ID(item.EntityID), album.HasUserWith(user.ID(owner.ID))).
			SetLidarrID(lidarrIDStr).
			SetLidarrStatus("monitored").
			Save(ctx)
		if err != nil {
			s.logger.Error("failed to update album lidarr_id", "error", err, "entity_id", item.EntityID)
		} else if n == 0 {
			s.logger.Error("refused to update lidarr_id on an album the queue owner does not own",
				"entity_id", item.EntityID, "owner_id", owner.ID)
		}
	}
}

// logSubmissionEvent creates a SyncEvent record for a successful Lidarr submission.
// The notified user is the queue row's owner, never the loaded entity's user edge.
// Governing: SPEC-0017 REQ "Observability", ADR-0031 (proposed)
func (s *LidarrSubmitter) logSubmissionEvent(ctx context.Context, item *ent.LidarrQueue, owner *ent.User, lidarrID int) {
	// Determine the event type and the message; the entity name is cosmetic and
	// is loaded under the owner's predicate when available.
	var eventType syncevent.EventType
	var message string

	switch item.EntityType {
	case lidarrqueue.EntityTypeArtist:
		eventType = syncevent.EventTypeLidarrArtistSubmitted
		name := ""
		if a, err := s.db.Artist.Query().
			Where(artist.ID(item.EntityID), artist.HasUserWith(user.ID(owner.ID))).
			Only(ctx); err == nil {
			name = a.Name
		} else if !ent.IsNotFound(err) {
			s.logger.Error("failed to load artist for sync event", "error", err, "entity_id", item.EntityID)
		}
		message = fmt.Sprintf("Submitted artist to Lidarr: %s", name)
	case lidarrqueue.EntityTypeAlbum:
		eventType = syncevent.EventTypeLidarrAlbumSubmitted
		name := ""
		if a, err := s.db.Album.Query().
			Where(album.ID(item.EntityID), album.HasUserWith(user.ID(owner.ID))).
			Only(ctx); err == nil {
			name = a.Name
		} else if !ent.IsNotFound(err) {
			s.logger.Error("failed to load album for sync event", "error", err, "entity_id", item.EntityID)
		}
		message = fmt.Sprintf("Submitted album to Lidarr: %s", name)
	default:
		return
	}

	meta := map[string]interface{}{
		"entity_id":      item.EntityID,
		"musicbrainz_id": item.MusicbrainzID,
		"lidarr_id":      lidarrID,
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		s.logger.Error("failed to marshal sync event metadata", "error", err)
		return
	}

	if err := s.db.SyncEvent.Create().
		SetUserID(owner.ID).
		SetEventType(eventType).
		SetProvider("lidarr").
		SetMessage(message).
		SetMetadata(string(metaJSON)).
		Exec(ctx); err != nil {
		s.logger.Error("failed to create sync event", "error", err)
	}
}

// isPermanentLidarrError returns true for errors that will never succeed on retry.
func isPermanentLidarrError(errMsg string) bool {
	return strings.Contains(errMsg, "artist lookup returned empty name") ||
		strings.Contains(errMsg, "ArtistPathValidator") ||
		strings.Contains(errMsg, "already configured for an existing artist") ||
		strings.Contains(errMsg, "not found in lidarr lookup for mbid") ||
		strings.Contains(errMsg, "artist not found in lidarr for album submission") ||
		// Ownership failures never heal on retry: the row names an entity its
		// owner does not own. Governing: ADR-0031 (proposed)
		strings.Contains(errMsg, "does not belong to the queue owner") ||
		strings.Contains(errMsg, "queue row has no owner")
}

// ComputeBackoff calculates the retry time using exponential backoff with jitter.
// delay = min(1m * 2^(attempts-1) + jitter, 1h) where jitter is random in [0, 1m)
// Governing: SPEC-0017 REQ "Backoff Strategy"
func ComputeBackoff(attempts int) time.Time {
	delay := baseBackoff * time.Duration(math.Pow(2, float64(attempts-1)))
	if delay > maxBackoff {
		delay = maxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(maxJitter)))
	delay += jitter
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return time.Now().Add(delay)
}

// doRequest performs an HTTP request to the Lidarr API.
func (s *LidarrSubmitter) doRequest(ctx context.Context, method, endpoint string, body interface{}, result interface{}) error {
	u, err := url.Parse(s.config.Lidarr.BaseURL)
	if err != nil {
		return err
	}

	var query string
	if idx := strings.Index(endpoint, "?"); idx != -1 {
		query = endpoint[idx+1:]
		endpoint = endpoint[:idx]
	}

	pathJoin, err := url.JoinPath(u.Path, "api/v1", endpoint)
	if err != nil {
		return err
	}
	u.Path = pathJoin
	u.RawQuery = query

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("X-Api-Key", s.config.Lidarr.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lidarr api error: %d - %s", resp.StatusCode, string(b))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return err
		}
	}
	return nil
}
