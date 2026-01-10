package lidarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"spotter/ent"
	"spotter/ent/syncevent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"strconv"
	"strings"
	"time"
)

type Enricher struct {
	logger *slog.Logger
	config *config.Config
	client *http.Client
	db     *ent.Client
	user   *ent.User
}

func New(logger *slog.Logger, cfg *config.Config, db *ent.Client) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		return &Enricher{
			logger: logger,
			config: cfg,
			client: &http.Client{
				Timeout: 30 * time.Second,
			},
			db:   db,
			user: user,
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeLidarr
}

func (e *Enricher) Name() string {
	return "Lidarr"
}

func (e *Enricher) IsAvailable() bool {
	return e.config.Lidarr.BaseURL != "" && e.config.Lidarr.APIKey != ""
}

// Lidarr API structs

type lidarrArtist struct {
	ID              int           `json:"id"`
	ArtistName      string        `json:"artistName"`
	ForeignArtistID string        `json:"foreignArtistId"` // MusicBrainz ID
	Monitored       bool          `json:"monitored"`
	Overview        string        `json:"overview"`
	Genres          []string      `json:"genres"`
	Images          []lidarrImage `json:"images"`
	Links           []lidarrLink  `json:"links"`
}

type lidarrImage struct {
	URL       string `json:"url"`
	CoverType string `json:"coverType"`
}

type lidarrLink struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type lidarrAlbum struct {
	ID             int           `json:"id"`
	Title          string        `json:"title"`
	ForeignAlbumID string        `json:"foreignAlbumId"` // MusicBrainz ID
	ArtistID       int           `json:"artistId"`
	Monitored      bool          `json:"monitored"`
	ReleaseDate    string        `json:"releaseDate"`
	Genres         []string      `json:"genres"`
	Images         []lidarrImage `json:"images"`
	AlbumType      string        `json:"albumType"`
}

type lidarrTrack struct {
	ID          int         `json:"id"`
	Title       string      `json:"title"`
	ArtistID    int         `json:"artistId"`
	AlbumID     int         `json:"albumId"`
	TrackNumber interface{} `json:"trackNumber"` // Handle int or string
	Duration    int         `json:"duration"`
	HasFile     bool        `json:"hasFile"`
}

// EnrichArtist
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	lArtist, err := e.findArtist(ctx, artist)
	if err != nil {
		return nil, err
	}

	if lArtist == nil || lArtist.ID == 0 {
		mbid := artist.MusicbrainzID
		if mbid == "" && lArtist != nil {
			mbid = lArtist.ForeignArtistID
		}

		if mbid != "" {
			added, err := e.addArtist(ctx, mbid)
			if err != nil {
				e.logger.Error("failed to add artist to lidarr", "error", err, "artist", artist.Name)
				return nil, nil
			}
			lArtist = added
			e.logEvent(ctx, "lidarr_artist_matched", fmt.Sprintf("Added artist to Lidarr: %s", lArtist.ArtistName), map[string]interface{}{
				"artist_name": lArtist.ArtistName,
				"lidarr_id":   lArtist.ID,
			})
		} else {
			return nil, nil
		}
	} else if artist.LidarrID == "" {
		// Found existing artist in Lidarr, first time match locally
		e.logEvent(ctx, "lidarr_artist_matched", fmt.Sprintf("Matched artist in Lidarr: %s", lArtist.ArtistName), map[string]interface{}{
			"artist_name": lArtist.ArtistName,
			"lidarr_id":   lArtist.ID,
		})
	}

	data := &enrichers.ArtistData{
		MusicBrainzID: lArtist.ForeignArtistID,
		Bio:           lArtist.Overview,
		Genres:        lArtist.Genres,
	}

	// Only set LidarrID if we have a valid ID (> 0)
	if lArtist.ID > 0 {
		data.LidarrID = fmt.Sprintf("%d", lArtist.ID)
	}

	return data, nil
}

func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	lArtist, err := e.findArtist(ctx, artist)
	if err != nil || lArtist == nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range lArtist.Images {
		images = append(images, enrichers.ImageData{
			URL:    img.URL,
			Type:   img.CoverType,
			Source: "lidarr",
		})
	}
	return images, nil
}

// EnrichAlbum
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	lAlbum, err := e.findAlbum(ctx, album)
	if err != nil {
		return nil, err
	}

	if lAlbum == nil {
		// Try to add album if missing. Need artist MBID first.
		if album.Edges.Artist != nil && album.Edges.Artist.MusicbrainzID != "" && album.MusicbrainzID != "" {
			// Ensure artist exists
			arData, err := e.EnrichArtist(ctx, album.Edges.Artist)
			if err != nil {
				e.logger.Warn("could not ensure artist exists in lidarr", "error", err)
			}

			var artistID int
			if arData != nil && arData.LidarrID != "" {
				parsedID, parseErr := strconv.Atoi(arData.LidarrID)
				if parseErr == nil {
					artistID = parsedID
				}
			}

			if artistID == 0 {
				// Fallback check
				lArt, findErr := e.findArtist(ctx, album.Edges.Artist)
				if findErr == nil && lArt != nil {
					artistID = lArt.ID
				}
			}

			if artistID == 0 {
				e.logger.Warn("cannot add album to lidarr: artist not found", "album", album.Name)
				return nil, nil
			}

			// Add album
			added, err := e.addAlbum(ctx, album.MusicbrainzID, artistID)
			if err != nil {
				e.logger.Error("failed to add album to lidarr", "error", err, "album", album.Name)
				return nil, nil
			}
			lAlbum = added
			e.logEvent(ctx, "lidarr_album_submitted", fmt.Sprintf("Submitted album to Lidarr: %s", lAlbum.Title), map[string]interface{}{
				"album_name": lAlbum.Title,
				"lidarr_id":  lAlbum.ID,
			})
		} else {
			return nil, nil
		}
	} else if album.LidarrID == "" {
		// Found existing album in Lidarr, first time match locally
		e.logEvent(ctx, "lidarr_album_matched", fmt.Sprintf("Matched album in Lidarr: %s", lAlbum.Title), map[string]interface{}{
			"album_name": lAlbum.Title,
			"lidarr_id":  lAlbum.ID,
		})
	}

	data := &enrichers.AlbumData{
		MusicBrainzID: lAlbum.ForeignAlbumID,
		AlbumType:     lAlbum.AlbumType,
		ReleaseDate:   lAlbum.ReleaseDate,
		Genre:         strings.Join(lAlbum.Genres, ", "),
	}

	// Only set LidarrID if we have a valid ID (> 0)
	if lAlbum.ID > 0 {
		data.LidarrID = fmt.Sprintf("%d", lAlbum.ID)
	}

	// Parse year
	if len(lAlbum.ReleaseDate) >= 4 {
		if _, err := fmt.Sscanf(lAlbum.ReleaseDate, "%d", &data.Year); err != nil {
			e.logger.Debug("failed to parse year from release date", "date", lAlbum.ReleaseDate, "error", err)
		}
	}

	return data, nil
}

func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	lAlbum, err := e.findAlbum(ctx, album)
	if err != nil || lAlbum == nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range lAlbum.Images {
		images = append(images, enrichers.ImageData{
			URL:    img.URL,
			Type:   img.CoverType,
			Source: "lidarr",
		})
	}
	return images, nil
}

// EnrichTrack
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	lTrack, err := e.findTrack(ctx, track)
	if err != nil {
		return nil, err
	}

	if lTrack == nil {
		// Track not found. Submit album if possible.
		if track.Edges.Album != nil && track.Edges.Album.MusicbrainzID != "" {
			_, err := e.EnrichAlbum(ctx, track.Edges.Album)
			if err != nil {
				e.logger.Error("failed to submit album for missing track", "error", err, "track", track.Name)
			}
		}

		return &enrichers.TrackData{
			LidarrStatus: "pending",
		}, nil
	}

	status := "monitored"
	if lTrack.HasFile {
		status = "available"
	}

	if track.LidarrID == nil {
		e.logEvent(ctx, "lidarr_track_matched", fmt.Sprintf("Matched track in Lidarr: %s", lTrack.Title), map[string]interface{}{
			"track_name": lTrack.Title,
			"lidarr_id":  lTrack.ID,
		})
	}

	data := &enrichers.TrackData{
		LidarrStatus: status,
		DurationMs:   lTrack.Duration,
	}

	// Only set LidarrID if we have a valid ID (> 0)
	if lTrack.ID > 0 {
		data.LidarrID = fmt.Sprintf("%d", lTrack.ID)
	}

	return data, nil
}

// Helper methods

func (e *Enricher) logEvent(ctx context.Context, eventType string, message string, meta map[string]interface{}) {
	if e.db == nil || e.user == nil {
		return
	}

	builder := e.db.SyncEvent.Create().
		SetUser(e.user).
		SetEventType(syncevent.EventType(eventType)).
		SetProvider("lidarr").
		SetMessage(message)

	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			builder.SetMetadata(string(b))
		}
	}

	if err := builder.Exec(ctx); err != nil {
		e.logger.Error("failed to create sync event", "error", err)
	}
}

func (e *Enricher) doRequest(ctx context.Context, method, endpoint string, body interface{}, result interface{}) error {
	u, err := url.Parse(e.config.Lidarr.BaseURL)
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
		jsonBody, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("X-Api-Key", e.config.Lidarr.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			e.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("lidarr api error: %d", resp.StatusCode)
		}
		return fmt.Errorf("lidarr api error: %d - %s", resp.StatusCode, string(b))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return err
		}
	}
	return nil
}

func (e *Enricher) findArtist(ctx context.Context, artist *ent.Artist) (*lidarrArtist, error) {
	// Try MBID search
	if artist.MusicbrainzID != "" {
		var artists []lidarrArtist
		err := e.doRequest(ctx, "GET", "artist", nil, &artists)
		if err != nil {
			return nil, err
		}

		for _, a := range artists {
			if a.ForeignArtistID == artist.MusicbrainzID {
				return &a, nil
			}
		}
	}

	// Try search by name or MBID
	term := artist.Name
	if artist.MusicbrainzID != "" {
		term = fmt.Sprintf("lidarr:%s", artist.MusicbrainzID)
	}

	u := fmt.Sprintf("artist/lookup?term=%s", url.QueryEscape(term))
	var results []lidarrArtist
	err := e.doRequest(ctx, "GET", u, nil, &results)
	if err != nil {
		return nil, err
	}

	for _, a := range results {
		if artist.MusicbrainzID != "" && a.ForeignArtistID == artist.MusicbrainzID {
			return &a, nil
		}
		if strings.EqualFold(a.ArtistName, artist.Name) {
			return &a, nil
		}
	}

	return nil, nil
}

func (e *Enricher) addArtist(ctx context.Context, mbid string) (*lidarrArtist, error) {
	u := fmt.Sprintf("artist/lookup?term=lidarr:%s", mbid)
	var results []lidarrArtist
	err := e.doRequest(ctx, "GET", u, nil, &results)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("artist not found in lidarr lookup")
	}

	artistToAdd := results[0]

	var rootFolders []struct {
		Path string `json:"path"`
	}
	if reqErr := e.doRequest(ctx, "GET", "rootfolder", nil, &rootFolders); reqErr != nil {
		return nil, reqErr
	}
	if len(rootFolders) == 0 {
		return nil, fmt.Errorf("no root folder configured in lidarr")
	}

	payload := map[string]interface{}{
		"artistName":        artistToAdd.ArtistName,
		"foreignArtistId":   artistToAdd.ForeignArtistID,
		"qualityProfileId":  1,
		"metadataProfileId": 1,
		"path":              fmt.Sprintf("%s/%s", rootFolders[0].Path, artistToAdd.ArtistName),
		"monitored":         true,
		"images":            artistToAdd.Images,
	}

	var qualityProfiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if reqErr := e.doRequest(ctx, "GET", "qualityprofile", nil, &qualityProfiles); reqErr == nil && len(qualityProfiles) > 0 {
		payload["qualityProfileId"] = qualityProfiles[0].ID
	}

	var metadataProfiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if reqErr := e.doRequest(ctx, "GET", "metadataprofile", nil, &metadataProfiles); reqErr == nil && len(metadataProfiles) > 0 {
		payload["metadataProfileId"] = metadataProfiles[0].ID
	}

	var newArtist lidarrArtist
	err = e.doRequest(ctx, "POST", "artist", payload, &newArtist)
	if err != nil {
		// Check if artist already exists - if so, fetch it instead
		if strings.Contains(err.Error(), "ArtistExistsValidator") || strings.Contains(err.Error(), "already been added") {
			e.logger.Info("artist already exists in lidarr, fetching existing", "mbid", mbid)
			return e.fetchArtistByMBID(ctx, mbid)
		}
		return nil, err
	}
	return &newArtist, nil
}

func (e *Enricher) fetchArtistByMBID(ctx context.Context, mbid string) (*lidarrArtist, error) {
	// Fetch all artists and find the one with matching MBID
	var artists []lidarrArtist
	err := e.doRequest(ctx, "GET", "artist", nil, &artists)
	if err != nil {
		return nil, err
	}

	for _, a := range artists {
		if a.ForeignArtistID == mbid {
			return &a, nil
		}
	}

	return nil, fmt.Errorf("artist with mbid %s not found in lidarr despite existing", mbid)
}

func (e *Enricher) findAlbum(ctx context.Context, album *ent.Album) (*lidarrAlbum, error) {
	if album.Edges.Artist == nil || album.Edges.Artist.MusicbrainzID == "" {
		return nil, nil
	}

	artist, err := e.findArtist(ctx, album.Edges.Artist)
	if err != nil || artist == nil {
		return nil, err
	}

	if artist.ID == 0 {
		return nil, nil
	}

	u := fmt.Sprintf("album?artistId=%d", artist.ID)
	var albums []lidarrAlbum
	err = e.doRequest(ctx, "GET", u, nil, &albums)
	if err != nil {
		return nil, err
	}

	for _, a := range albums {
		if album.MusicbrainzID != "" && a.ForeignAlbumID == album.MusicbrainzID {
			return &a, nil
		}
		if strings.EqualFold(a.Title, album.Name) {
			return &a, nil
		}
	}

	return nil, nil
}

func (e *Enricher) addAlbum(ctx context.Context, mbid string, artistID int) (*lidarrAlbum, error) {
	u := fmt.Sprintf("album/lookup?term=lidarr:%s", mbid)
	var results []lidarrAlbum
	err := e.doRequest(ctx, "GET", u, nil, &results)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("album not found in lidarr lookup")
	}

	albumToAdd := results[0]

	payload := map[string]interface{}{
		"title":          albumToAdd.Title,
		"foreignAlbumId": albumToAdd.ForeignAlbumID,
		"artistId":       artistID,
		"monitored":      true,
		"albumType":      albumToAdd.AlbumType,
		"releaseDate":    albumToAdd.ReleaseDate,
		"images":         albumToAdd.Images,
	}

	var newAlbum lidarrAlbum
	err = e.doRequest(ctx, "POST", "album", payload, &newAlbum)
	if err != nil {
		// Check if album already exists - if so, fetch it instead
		if strings.Contains(err.Error(), "AlbumExistsValidator") || strings.Contains(err.Error(), "already been added") {
			e.logger.Info("album already exists in lidarr, fetching existing", "mbid", mbid)
			return e.fetchAlbumByMBID(ctx, mbid, artistID)
		}
		return nil, err
	}
	return &newAlbum, nil
}

func (e *Enricher) fetchAlbumByMBID(ctx context.Context, mbid string, artistID int) (*lidarrAlbum, error) {
	// Fetch all albums for this artist and find the one with matching MBID
	u := fmt.Sprintf("album?artistId=%d", artistID)
	var albums []lidarrAlbum
	err := e.doRequest(ctx, "GET", u, nil, &albums)
	if err != nil {
		return nil, err
	}

	for _, a := range albums {
		if a.ForeignAlbumID == mbid {
			return &a, nil
		}
	}

	return nil, fmt.Errorf("album with mbid %s not found in lidarr despite existing", mbid)
}

func (e *Enricher) findTrack(ctx context.Context, track *ent.Track) (*lidarrTrack, error) {
	if track.Edges.Album == nil {
		return nil, nil
	}

	album, err := e.findAlbum(ctx, track.Edges.Album)
	if err != nil || album == nil {
		return nil, err
	}

	u := fmt.Sprintf("track?artistId=%d&albumId=%d", album.ArtistID, album.ID)
	var tracks []lidarrTrack
	err = e.doRequest(ctx, "GET", u, nil, &tracks)
	if err != nil {
		return nil, err
	}

	for _, t := range tracks {
		if track.TrackNumber != nil {
			tnStr := fmt.Sprintf("%d", *track.TrackNumber)
			var lidarrTnStr string
			switch v := t.TrackNumber.(type) {
			case float64:
				lidarrTnStr = fmt.Sprintf("%.0f", v)
			case string:
				lidarrTnStr = v
			}

			if lidarrTnStr == tnStr {
				// Secondary check on name if available?
				// Just track number should be enough for an album
				return &t, nil
			}
		}
		// Fallback to title
		if strings.EqualFold(t.Title, track.Name) {
			return &t, nil
		}
	}

	return nil, nil
}
