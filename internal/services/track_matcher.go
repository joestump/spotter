// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-001 through REQ-PLSYNC-005 (track matching strategies)
package services

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/fuzzy"
	"spotter/internal/providers"
)

// MatchMethod describes how a track was matched.
type MatchMethod string

const (
	MatchMethodNone  MatchMethod = "none"
	MatchMethodISRC  MatchMethod = "isrc"
	MatchMethodExact MatchMethod = "exact"
	MatchMethodFuzzy MatchMethod = "fuzzy"
)

// MatchResult contains the result of matching a source track to the local library.
type MatchResult struct {
	SourceTrack      providers.Track
	NavidromeTrackID string      // Empty if no match found
	MatchConfidence  float64     // 0.0 to 1.0
	MatchMethod      MatchMethod // "exact", "fuzzy", "isrc", "none"
}

// TrackMatcher matches source tracks from external providers to local Navidrome tracks.
type TrackMatcher struct {
	Client             *ent.Client
	Logger             *slog.Logger
	MinMatchConfidence float64 // Minimum confidence for fuzzy matching (0.0-1.0)
}

// NewTrackMatcher creates a new TrackMatcher with the specified minimum match confidence.
func NewTrackMatcher(client *ent.Client, logger *slog.Logger, minConfidence float64) *TrackMatcher {
	return &TrackMatcher{
		Client:             client,
		Logger:             logger,
		MinMatchConfidence: minConfidence,
	}
}

// NormalizedCandidate is a library track whose match keys have been normalized
// once, up front. Fuzzy matching compares against the precomputed rune slices
// so per-source-track work never re-runs normalization over the library. The
// representation - and the scoring that reads it - lives in internal/fuzzy, so
// every consumer that fuzzy-matches (playlist sync, the vibes generator) walks
// the same stack.
type NormalizedCandidate = fuzzy.Candidate[*ent.Track]

// LibraryIndex is a precomputed matching index over a user's Navidrome library.
// Build it once per sync tick with LoadLibraryIndex and reuse it across
// playlists via MatchTracksWithIndex so matching cost no longer scales with
// playlist count x library size.
type LibraryIndex struct {
	UserID     int
	isrcMap    map[string]*ent.Track // key: lowercased ISRC
	exactMap   map[string]*ent.Track // key: normalized "artist|title"
	candidates []NormalizedCandidate
}

// Size returns the number of library tracks in the index.
func (idx *LibraryIndex) Size() int {
	return len(idx.candidates)
}

// LoadLibraryIndex loads the user's Navidrome-linked library once and builds
// the lookup maps and normalized fuzzy-match candidates.
// Governing: ADR-0014 (lookup maps for tiers 1-2, normalized candidates for tier 3)
func (m *TrackMatcher) LoadLibraryIndex(ctx context.Context, userID int) (*LibraryIndex, error) {
	// Get all tracks for this user's library (tracks that have a navidrome_id)
	// FIX: Filter by user through the artist edge: Track -> Artist -> User
	libraryTracks, err := m.Client.Track.Query().
		Where(
			track.NavidromeIDNotNil(),
			track.HasArtistWith(artist.HasUserWith(user.ID(userID))),
		).
		WithArtist().
		All(ctx)
	if err != nil {
		m.Logger.Error("failed to query library tracks",
			"user_id", userID,
			"error", err)
		return nil, err
	}

	idx := &LibraryIndex{
		UserID:     userID,
		isrcMap:    make(map[string]*ent.Track),
		exactMap:   make(map[string]*ent.Track),
		candidates: make([]NormalizedCandidate, 0, len(libraryTracks)),
	}

	for _, t := range libraryTracks {
		// ISRC map (if available)
		if t.Isrc != nil && *t.Isrc != "" {
			isrcKey := strings.ToLower(*t.Isrc)
			idx.isrcMap[isrcKey] = t
		}

		artistName := ""
		if t.Edges.Artist != nil {
			artistName = t.Edges.Artist.Name
		}
		normalizedTitle := fuzzy.Normalize(t.Name)
		normalizedArtist := fuzzy.Normalize(artistName)

		// Exact match map
		key := normalizedArtist + "|" + normalizedTitle
		idx.exactMap[key] = t

		// Fuzzy match candidate with precomputed normalized runes.
		// The query above already filters NavidromeIDNotNil, so every track
		// here is a valid candidate.
		idx.candidates = append(idx.candidates, fuzzy.NewCandidate(t, t.Name, artistName))
	}

	m.Logger.Debug("built library index",
		"user_id", userID,
		"library_track_count", len(libraryTracks),
		"isrc_map_size", len(idx.isrcMap),
		"exact_map_size", len(idx.exactMap))

	return idx, nil
}

// MatchTracks attempts to find Navidrome track IDs for source tracks.
// Uses multiple strategies: ISRC matching, exact name match, fuzzy matching.
// It loads the library index on every call; callers matching multiple
// playlists in one tick should use LoadLibraryIndex + MatchTracksWithIndex.
func (m *TrackMatcher) MatchTracks(ctx context.Context, userID int, tracks []providers.Track) ([]MatchResult, error) {
	idx, err := m.LoadLibraryIndex(ctx, userID)
	if err != nil {
		return nil, err
	}
	return m.MatchTracksWithIndex(idx, tracks), nil
}

// MatchTracksWithIndex matches source tracks against a precomputed LibraryIndex.
// It performs no I/O: the index must have been built via LoadLibraryIndex.
// Governing: ADR-0014 (three-tier ISRC -> exact -> fuzzy matching)
func (m *TrackMatcher) MatchTracksWithIndex(idx *LibraryIndex, tracks []providers.Track) []MatchResult {
	if idx == nil {
		panic("services.TrackMatcher.MatchTracksWithIndex: nil LibraryIndex; call LoadLibraryIndex first")
	}

	startTime := time.Now()
	results := make([]MatchResult, len(tracks))

	m.Logger.Info("starting track matching",
		"user_id", idx.UserID,
		"source_track_count", len(tracks),
		"library_track_count", idx.Size(),
		"min_match_confidence", m.MinMatchConfidence)

	if idx.Size() == 0 {
		m.Logger.Warn("no tracks with navidrome_id found for user",
			"user_id", idx.UserID,
			"hint", "ensure Navidrome sync has run to populate navidrome_id on tracks")

		// Return all results as unmatched
		for i, sourceTrack := range tracks {
			results[i] = MatchResult{
				SourceTrack:     sourceTrack,
				MatchConfidence: 0.0,
				MatchMethod:     MatchMethodNone,
			}
		}
		return results
	}

	// Track match statistics by method
	matchStats := map[MatchMethod]int{
		MatchMethodNone:  0,
		MatchMethodISRC:  0,
		MatchMethodExact: 0,
		MatchMethodFuzzy: 0,
	}

	// Governing: ADR-0019 (structured metrics), SPEC observability REQ "MATCH-001", REQ "MATCH-002", REQ "MATCH-003"
	// Match each source track
	for i, sourceTrack := range tracks {
		trackMatchStart := time.Now()
		result := MatchResult{
			SourceTrack:     sourceTrack,
			MatchConfidence: 0.0,
			MatchMethod:     MatchMethodNone,
		}

		// Strategy 1: ISRC matching (highest confidence)
		if sourceTrack.ISRC != "" {
			isrcKey := strings.ToLower(sourceTrack.ISRC)
			if matchedTrack, ok := idx.isrcMap[isrcKey]; ok {
				if matchedTrack.NavidromeID != nil {
					result.NavidromeTrackID = *matchedTrack.NavidromeID
					result.MatchConfidence = 1.0
					result.MatchMethod = MatchMethodISRC
					matchStats[MatchMethodISRC]++
					results[i] = result

					m.Logger.Debug("matched track by ISRC",
						"source_artist", sourceTrack.Artist,
						"source_title", sourceTrack.Name,
						"isrc", sourceTrack.ISRC,
						"navidrome_id", *matchedTrack.NavidromeID)
					// REQ "MATCH-002": emit metric for the successful strategy only
					m.Logger.Info("metric.track_match",
						"strategy", "isrc",
						"matched", true,
						"confidence", 1.0,
						"duration_ms", time.Since(trackMatchStart).Milliseconds())
					continue
				}
			}
		}

		// Strategy 2: Exact match (artist + title)
		normalizedTitle := fuzzy.Normalize(sourceTrack.Name)
		normalizedArtist := fuzzy.Normalize(sourceTrack.Artist)
		exactKey := normalizedArtist + "|" + normalizedTitle
		if matchedTrack, ok := idx.exactMap[exactKey]; ok {
			if matchedTrack.NavidromeID != nil {
				result.NavidromeTrackID = *matchedTrack.NavidromeID
				result.MatchConfidence = 1.0
				result.MatchMethod = MatchMethodExact
				matchStats[MatchMethodExact]++
				results[i] = result

				m.Logger.Debug("matched track by exact match",
					"source_artist", sourceTrack.Artist,
					"source_title", sourceTrack.Name,
					"navidrome_id", *matchedTrack.NavidromeID)
				// REQ "MATCH-002": emit metric for the successful strategy only
				m.Logger.Info("metric.track_match",
					"strategy", "exact",
					"matched", true,
					"confidence", 1.0,
					"duration_ms", time.Since(trackMatchStart).Milliseconds())
				continue
			}
		}

		// Strategy 3: Fuzzy matching
		bestIndex, confidence := fuzzy.Best([]rune(normalizedTitle), []rune(normalizedArtist), idx.candidates)
		if bestIndex >= 0 && confidence >= m.MinMatchConfidence {
			bestMatch := idx.candidates[bestIndex].Match
			if bestMatch.NavidromeID != nil {
				result.NavidromeTrackID = *bestMatch.NavidromeID
				result.MatchConfidence = confidence
				result.MatchMethod = MatchMethodFuzzy
				matchStats[MatchMethodFuzzy]++

				matchedArtist := ""
				if bestMatch.Edges.Artist != nil {
					matchedArtist = bestMatch.Edges.Artist.Name
				}

				m.Logger.Debug("matched track by fuzzy match",
					"source_artist", sourceTrack.Artist,
					"source_title", sourceTrack.Name,
					"matched_artist", matchedArtist,
					"matched_title", bestMatch.Name,
					"confidence", confidence,
					"navidrome_id", *bestMatch.NavidromeID)
				// REQ "MATCH-002": emit metric for the successful strategy only
				m.Logger.Info("metric.track_match",
					"strategy", "fuzzy",
					"matched", true,
					"confidence", confidence,
					"duration_ms", time.Since(trackMatchStart).Milliseconds())
			}
		} else {
			matchStats[MatchMethodNone]++

			m.Logger.Debug("no match found for track",
				"source_artist", sourceTrack.Artist,
				"source_title", sourceTrack.Name,
				"best_confidence", confidence,
				"min_required", m.MinMatchConfidence)
			// REQ "MATCH-003": all strategies failed, emit strategy="fuzzy", matched=false, confidence=0.0
			m.Logger.Info("metric.track_match",
				"strategy", "fuzzy",
				"matched", false,
				"confidence", 0.0,
				"duration_ms", time.Since(trackMatchStart).Milliseconds())
		}

		results[i] = result
	}

	// Log summary
	matched := 0
	for _, r := range results {
		if r.NavidromeTrackID != "" {
			matched++
		}
	}

	duration := time.Since(startTime)
	m.Logger.Info("track matching complete",
		"user_id", idx.UserID,
		"total_tracks", len(tracks),
		"matched_tracks", matched,
		"unmatched_tracks", len(tracks)-matched,
		"match_rate", float64(matched)/float64(len(tracks))*100,
		"matches_by_isrc", matchStats[MatchMethodISRC],
		"matches_by_exact", matchStats[MatchMethodExact],
		"matches_by_fuzzy", matchStats[MatchMethodFuzzy],
		"duration_ms", duration.Milliseconds())

	return results
}

// GetUnmatchedTracks returns tracks that couldn't be matched to the library.
func GetUnmatchedTracks(results []MatchResult) []MatchResult {
	var unmatched []MatchResult
	for _, r := range results {
		if r.NavidromeTrackID == "" {
			unmatched = append(unmatched, r)
		}
	}
	return unmatched
}

// GetMatchedTracks returns tracks that were successfully matched.
func GetMatchedTracks(results []MatchResult) []MatchResult {
	var matched []MatchResult
	for _, r := range results {
		if r.NavidromeTrackID != "" {
			matched = append(matched, r)
		}
	}
	return matched
}

// MatchStats contains statistics about a matching operation.
type MatchStats struct {
	Total         int
	Matched       int
	Unmatched     int
	ByMethod      map[MatchMethod]int
	AvgConfidence float64
}

// GetMatchStats calculates statistics for a set of match results.
func GetMatchStats(results []MatchResult) MatchStats {
	stats := MatchStats{
		Total:    len(results),
		ByMethod: make(map[MatchMethod]int),
	}

	totalConfidence := 0.0
	matchedCount := 0

	for _, r := range results {
		stats.ByMethod[r.MatchMethod]++
		if r.NavidromeTrackID != "" {
			stats.Matched++
			totalConfidence += r.MatchConfidence
			matchedCount++
		} else {
			stats.Unmatched++
		}
	}

	if matchedCount > 0 {
		stats.AvgConfidence = totalConfidence / float64(matchedCount)
	}

	return stats
}
