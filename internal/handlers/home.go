package handlers

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/views/home"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
		return
	}

	stats, err := h.getHomeStats(r.Context(), u)
	if err != nil {
		h.Logger.Error("failed to get home stats", "error", err)
		// Continue with empty stats rather than failing
		stats = &home.HomeStats{
			NavidromeURL: h.Config.Navidrome.BaseURL,
			LoggedInUser: u.Username,
		}
	}

	h.Render(w, r, home.Index(h.Config, stats))
}

func (h *Handler) getHomeStats(ctx context.Context, u *ent.User) (*home.HomeStats, error) {
	// Refresh user to get all auth edges
	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		WithNavidromeAuth().
		WithLastfmAuth().
		Only(ctx)
	if err != nil {
		return nil, err
	}

	stats := &home.HomeStats{
		NavidromeURL: h.Config.Navidrome.BaseURL,
		LoggedInUser: u.Username,
		Providers:    make([]home.ProviderStats, 0, 3),
	}

	// Counts are computed by COUNT/GROUP BY in the database. Materializing the
	// user's entire history to count it made the render cost scale with listen
	// rows, and a scrobble is one row per play.
	// Governing: ADR-0004 (Ent ORM), ADR-0019 (structured metrics)
	totalListens, err := h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		Count(ctx)
	if err != nil {
		h.Logger.Error("failed to count listens", "error", err)
		totalListens = 0
	}

	stats.TotalListens = totalListens

	artistSet := make(map[string]bool)
	albumSet := make(map[string]bool)
	trackSet := make(map[string]bool)
	artistCounts := make(map[string]int)

	// One grouped query answers both the unique-artist count and the top-artist
	// counts that used to come out of the materialized history.
	var artistRows []struct {
		ArtistName string `json:"artist_name"`
		Count      int    `json:"count"`
	}
	err = h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		GroupBy(listen.FieldArtistName).
		Aggregate(ent.Count()).
		Scan(ctx, &artistRows)
	if err != nil {
		h.Logger.Error("failed to group listens by artist", "error", err)
	}
	for _, r := range artistRows {
		artistSet[r.ArtistName] = true
		artistCounts[r.ArtistName] = r.Count
	}

	var albumRows []struct {
		AlbumName string `json:"album_name"`
	}
	err = h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		GroupBy(listen.FieldAlbumName).
		Scan(ctx, &albumRows)
	if err != nil {
		h.Logger.Error("failed to group listens by album", "error", err)
	}
	for _, r := range albumRows {
		albumSet[r.AlbumName] = true
	}

	var trackRows []struct {
		TrackName  string `json:"track_name"`
		ArtistName string `json:"artist_name"`
	}
	err = h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		GroupBy(listen.FieldTrackName, listen.FieldArtistName).
		Scan(ctx, &trackRows)
	if err != nil {
		h.Logger.Error("failed to group listens by track", "error", err)
	}
	for _, r := range trackRows {
		trackSet[r.TrackName+"||"+r.ArtistName] = true
	}

	// Supplement global stats with playlist track data so users who have
	// playlists but few listening history records see meaningful numbers. Only
	// the name columns are read, grouped in the database.
	var ptRows []struct {
		ArtistName string `json:"artist_name"`
		AlbumName  string `json:"album_name"`
		TrackName  string `json:"track_name"`
	}
	ptErr := h.Client.PlaylistTrack.Query().
		Where(
			playlisttrack.HasPlaylistWith(
				playlist.HasUserWith(user.ID(u.ID)),
			),
		).
		GroupBy(
			playlisttrack.FieldArtistName,
			playlisttrack.FieldAlbumName,
			playlisttrack.FieldTrackName,
		).
		Scan(ctx, &ptRows)
	if ptErr != nil {
		h.Logger.Warn("failed to group playlist tracks for home stats", "error", ptErr)
	}
	for _, pt := range ptRows {
		if pt.ArtistName != "" {
			artistSet[pt.ArtistName] = true
			// Only count toward top artists if not already in listens
			// (listen-based counts remain the sort key)
		}
		if pt.AlbumName != "" {
			albumSet[pt.AlbumName] = true
		}
		if pt.TrackName != "" && pt.ArtistName != "" {
			trackSet[pt.TrackName+"||"+pt.ArtistName] = true
		}
	}

	stats.UniqueArtists = len(artistSet)
	stats.UniqueAlbums = len(albumSet)
	stats.UniqueTracks = len(trackSet)

	// Get enriched catalog counts
	stats.EnrichedArtistCount, err = h.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		Count(ctx)
	if err != nil {
		stats.EnrichedArtistCount = 0
	}

	stats.EnrichedAlbumCount, err = h.Client.Album.Query().
		Where(album.HasUserWith(user.ID(u.ID))).
		Count(ctx)
	if err != nil {
		stats.EnrichedAlbumCount = 0
	}

	stats.EnrichedTrackCount, err = h.Client.Track.Query().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(u.ID)))).
		Count(ctx)
	if err != nil {
		stats.EnrichedTrackCount = 0
	}

	// Calculate top artists
	type artistStat struct {
		Name  string
		Count int
	}
	var topArtists []artistStat
	for name, count := range artistCounts {
		topArtists = append(topArtists, artistStat{Name: name, Count: count})
	}
	sort.Slice(topArtists, func(i, j int) bool {
		return topArtists[i].Count > topArtists[j].Count
	})

	if len(topArtists) > 20 {
		topArtists = topArtists[:20]
	}

	// Fetch details for top artists
	topArtistNames := make([]string, len(topArtists))
	for i, a := range topArtists {
		topArtistNames[i] = a.Name
	}

	enrichedArtists, err := h.Client.Artist.Query().
		Where(
			artist.HasUserWith(user.ID(u.ID)),
			artist.NameIn(topArtistNames...),
		).
		WithImages().
		All(ctx)

	if err == nil {
		enrichedMap := make(map[string]*ent.Artist)
		for _, a := range enrichedArtists {
			enrichedMap[a.Name] = a
		}

		stats.TopArtists = make([]home.TopArtist, 0, len(topArtists))
		for _, a := range topArtists {
			if enriched, ok := enrichedMap[a.Name]; ok {
				stats.TopArtists = append(stats.TopArtists, home.TopArtist{
					Artist:  enriched,
					Listens: a.Count,
				})
			}
		}
	}

	// Navidrome provider stats
	navidromeStats := home.ProviderStats{
		Name:      "Navidrome",
		Connected: u.Edges.NavidromeAuth != nil,
	}
	if u.Edges.NavidromeAuth != nil {
		navidromeStats.Username = u.Username
		navidromeStats.LastSyncedAt = u.Edges.NavidromeAuth.LastSyncedAt
		navidromeStats.ServerURL = h.Config.Navidrome.BaseURL
		navidromeStats.ServerOnline = h.navidromeOnline(u.ID, u.Username, u.Edges.NavidromeAuth.Password)
		navidromeStats.TotalListens, err = h.Client.Listen.Query().
			Where(
				listen.HasUserWith(user.ID(u.ID)),
				listen.Source("navidrome"),
			).Count(ctx)
		if err != nil {
			navidromeStats.TotalListens = 0
		}
		navidromeStats.UniqueArtists = h.countUniqueArtists(ctx, u.ID, "navidrome")
		navidromeStats.UniqueAlbums = h.countUniqueAlbums(ctx, u.ID, "navidrome")
	}
	stats.Providers = append(stats.Providers, navidromeStats)

	// Spotify provider stats
	spotifyStats := home.ProviderStats{
		Name:      "Spotify",
		Connected: u.Edges.SpotifyAuth != nil,
	}
	if u.Edges.SpotifyAuth != nil {
		spotifyStats.Username = u.Edges.SpotifyAuth.DisplayName
		if spotifyStats.Username == "" {
			spotifyStats.Username = "Connected"
		}
		spotifyStats.LastSyncedAt = u.Edges.SpotifyAuth.LastSyncedAt
		spotifyStats.TotalListens, err = h.Client.Listen.Query().
			Where(
				listen.HasUserWith(user.ID(u.ID)),
				listen.Source("spotify"),
			).Count(ctx)
		if err != nil {
			spotifyStats.TotalListens = 0
		}
		spotifyStats.UniqueArtists = h.countUniqueArtists(ctx, u.ID, "spotify")
		spotifyStats.UniqueAlbums = h.countUniqueAlbums(ctx, u.ID, "spotify")
	}
	stats.Providers = append(stats.Providers, spotifyStats)

	// Last.fm provider stats
	lastfmStats := home.ProviderStats{
		Name:      "Last.fm",
		Connected: u.Edges.LastfmAuth != nil,
	}
	if u.Edges.LastfmAuth != nil {
		lastfmStats.Username = u.Edges.LastfmAuth.Username
		lastfmStats.LastSyncedAt = u.Edges.LastfmAuth.LastSyncedAt
		lastfmStats.TotalListens, err = h.Client.Listen.Query().
			Where(
				listen.HasUserWith(user.ID(u.ID)),
				listen.Source("lastfm"),
			).Count(ctx)
		if err != nil {
			lastfmStats.TotalListens = 0
		}
		lastfmStats.UniqueArtists = h.countUniqueArtists(ctx, u.ID, "lastfm")
		lastfmStats.UniqueAlbums = h.countUniqueAlbums(ctx, u.ID, "lastfm")
	}
	stats.Providers = append(stats.Providers, lastfmStats)

	return stats, nil
}

func (h *Handler) countUniqueArtists(ctx context.Context, userID int, source string) int {
	var results []struct {
		ArtistName string `json:"artist_name"`
	}
	err := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.Source(source),
		).
		GroupBy(listen.FieldArtistName).
		Scan(ctx, &results)
	if err != nil {
		return 0
	}
	return len(results)
}

func (h *Handler) countUniqueAlbums(ctx context.Context, userID int, source string) int {
	var results []struct {
		AlbumName string `json:"album_name"`
	}
	err := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.Source(source),
		).
		GroupBy(listen.FieldAlbumName).
		Scan(ctx, &results)
	if err != nil {
		return 0
	}
	return len(results)
}

// checkNavidromeOnline pings the Navidrome server to check if it's reachable.
// It runs on the health cache's runner, not the request path, so ctx is the only
// thing that bounds how long a stale result is reported.
func (h *Handler) checkNavidromeOnline(ctx context.Context, username, password string) bool {
	if h.Config.Navidrome.BaseURL == "" {
		return false
	}

	// Use a short timeout for the ping
	client := &http.Client{Timeout: navidromePingTimeout}

	// Build ping URL with auth - generate random salt
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		h.Logger.Error("failed to generate random salt", "error", err)
		return false
	}
	salt := hex.EncodeToString(saltBytes)

	hash := md5.New()
	hash.Write([]byte(password + salt))
	token := hex.EncodeToString(hash.Sum(nil))

	url := h.Config.Navidrome.BaseURL + "/rest/ping.view?u=" + username + "&t=" + token + "&s=" + salt + "&v=1.16.1&c=spotter&f=json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.Logger.Debug("failed to build navidrome ping request", "error", err)
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		h.Logger.Debug("navidrome ping failed", "error", err)
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			h.Logger.Warn("failed to close response body", "error", err)
		}
	}()

	return resp.StatusCode == http.StatusOK
}
