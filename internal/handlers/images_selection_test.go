package handlers

// Best-image selection tests for REQ-ENRICH-022 (issue #343).
//
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-022 — the best image
// is ranked by IsPrimary, then highest Likes, then largest dimensions
// (Width × Height). Records without a downloaded local_path are skipped
// (issue #127).

import (
	"testing"

	"spotter/ent"

	"github.com/stretchr/testify/assert"
)

func intPtr(v int) *int { return &v }

func TestBestArtistImagePath_PrimaryBeatsLikesAndSize(t *testing.T) {
	images := []*ent.ArtistImage{
		{ID: 1, LocalPath: "data/images/artists/1-fanart-aaaa.png", Likes: intPtr(500), Width: intPtr(4000), Height: intPtr(4000)},
		{ID: 2, LocalPath: "data/images/artists/1-thumbnail-bbbb.png", IsPrimary: true, Likes: intPtr(1), Width: intPtr(100), Height: intPtr(100)},
	}
	assert.Equal(t, "data/images/artists/1-thumbnail-bbbb.png", bestArtistImagePath(images),
		"IsPrimary must outrank Likes and dimensions")
}

func TestBestArtistImagePath_LikesBreakPrimaryTie(t *testing.T) {
	images := []*ent.ArtistImage{
		{ID: 1, LocalPath: "low-likes.png", IsPrimary: true, Likes: intPtr(3), Width: intPtr(2000), Height: intPtr(2000)},
		{ID: 2, LocalPath: "high-likes.png", IsPrimary: true, Likes: intPtr(42), Width: intPtr(500), Height: intPtr(500)},
	}
	assert.Equal(t, "high-likes.png", bestArtistImagePath(images),
		"among primaries, higher Likes must win before dimensions are considered")
}

func TestBestArtistImagePath_DimensionsBreakLikesTie(t *testing.T) {
	images := []*ent.ArtistImage{
		{ID: 1, LocalPath: "small.png", Likes: intPtr(10), Width: intPtr(500), Height: intPtr(500)},
		{ID: 2, LocalPath: "large.png", Likes: intPtr(10), Width: intPtr(1920), Height: intPtr(1080)},
	}
	assert.Equal(t, "large.png", bestArtistImagePath(images),
		"with equal Likes, larger Width × Height must win")
}

func TestBestArtistImagePath_SkipsRecordsWithoutLocalPath(t *testing.T) {
	// Governing: issue #127 — an undownloaded primary must not shadow a
	// downloaded fallback.
	images := []*ent.ArtistImage{
		{ID: 1, LocalPath: "", IsPrimary: true, Likes: intPtr(100)},
		{ID: 2, LocalPath: "downloaded.png"},
	}
	assert.Equal(t, "downloaded.png", bestArtistImagePath(images))
}

func TestBestArtistImagePath_NilLikesAndDimensionsRankLowest(t *testing.T) {
	images := []*ent.ArtistImage{
		{ID: 1, LocalPath: "no-metadata.png"},
		{ID: 2, LocalPath: "with-likes.png", Likes: intPtr(1)},
	}
	assert.Equal(t, "with-likes.png", bestArtistImagePath(images),
		"nil Likes must be treated as zero, not preferred")
}

func TestBestArtistImagePath_NoServableImages(t *testing.T) {
	assert.Empty(t, bestArtistImagePath(nil))
	assert.Empty(t, bestArtistImagePath([]*ent.ArtistImage{{ID: 1, LocalPath: "", IsPrimary: true}}))
}

func TestBestArtistImagePath_Deterministic(t *testing.T) {
	// Full ties fall back to lowest ID so serving is stable across requests.
	images := []*ent.ArtistImage{
		{ID: 7, LocalPath: "seven.png"},
		{ID: 3, LocalPath: "three.png"},
	}
	assert.Equal(t, "three.png", bestArtistImagePath(images))
}

func TestBestAlbumImagePath_PrimaryBeatsSize(t *testing.T) {
	images := []*ent.AlbumImage{
		{ID: 1, LocalPath: "big.png", Width: intPtr(3000), Height: intPtr(3000)},
		{ID: 2, LocalPath: "primary.png", IsPrimary: true, Width: intPtr(200), Height: intPtr(200)},
	}
	assert.Equal(t, "primary.png", bestAlbumImagePath(images),
		"IsPrimary must outrank dimensions")
}

func TestBestAlbumImagePath_DimensionsBreakTie(t *testing.T) {
	images := []*ent.AlbumImage{
		{ID: 1, LocalPath: "small.png", Width: intPtr(300), Height: intPtr(300)},
		{ID: 2, LocalPath: "large.png", Width: intPtr(1400), Height: intPtr(1400)},
	}
	assert.Equal(t, "large.png", bestAlbumImagePath(images))
}

func TestBestAlbumImagePath_SkipsRecordsWithoutLocalPath(t *testing.T) {
	images := []*ent.AlbumImage{
		{ID: 1, LocalPath: "", IsPrimary: true},
		{ID: 2, LocalPath: "fallback.png"},
	}
	assert.Equal(t, "fallback.png", bestAlbumImagePath(images))
}
