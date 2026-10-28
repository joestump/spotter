package handlers_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/enttest"
	"spotter/ent/tag"
	"spotter/ent/user"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/database"
	"spotter/internal/enrichers"
	"spotter/internal/events"
	"spotter/internal/handlers"
	"spotter/internal/services"
	"spotter/internal/tags"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tagWriteUserCounter int64

// fakeAIEnricher stands in for the OpenAI enricher. It returns its AI tags both
// in the deprecated AITags JSON and as TypedTags, which is what the real
// enricher does (internal/enrichers/openai).
type fakeAIEnricher struct{}

func (fakeAIEnricher) Type() enrichers.Type { return enrichers.TypeOpenAI }
func (fakeAIEnricher) Name() string         { return "fake-openai" }
func (fakeAIEnricher) IsAvailable() bool    { return true }

func (fakeAIEnricher) GetArtistImages(context.Context, *ent.Artist) ([]enrichers.ImageData, error) {
	return nil, nil
}

func (fakeAIEnricher) GetAlbumImages(context.Context, *ent.Album) ([]enrichers.ImageData, error) {
	return nil, nil
}

func (fakeAIEnricher) EnrichArtist(context.Context, *ent.Artist) (*enrichers.ArtistData, error) {
	return &enrichers.ArtistData{AISummary: "summary", AITags: []string{"ethereal"}, TypedTags: aiTypedTag()}, nil
}

func (fakeAIEnricher) EnrichAlbum(context.Context, *ent.Album) (*enrichers.AlbumData, error) {
	return &enrichers.AlbumData{AISummary: "summary", AITags: []string{"ethereal"}, TypedTags: aiTypedTag()}, nil
}

func (fakeAIEnricher) EnrichTrack(context.Context, *ent.Track) (*enrichers.TrackData, error) {
	return &enrichers.TrackData{AISummary: "summary", AITags: []string{"ethereal"}, TypedTags: aiTypedTag()}, nil
}

// aiTypedTag returns the padded form on purpose: the taxonomy write path owns
// normalization, so an enricher that pads a tag must not be able to store it
// verbatim.
func aiTypedTag() []tags.TypedTag {
	return []tags.TypedTag{{Name: "  ethereal  ", Type: "ai"}}
}

// setupTagWriteHandler builds a handler whose metadata service has an AI
// enricher registered, over a database that also carries the entity_tags table,
// so a regenerate call can be checked against the whole tag write path.
func setupTagWriteHandler(t *testing.T) (*ent.Client, *sql.DB, *handlers.Handler) {
	t.Helper()

	dsn := "file:" + filepath.Join(t.TempDir(), "tags.db") + "?_fk=1&_journal_mode=WAL"
	client := enttest.Open(t, "sqlite3", dsn)

	db, err := database.OpenRawDB("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.CreateEntityTagsTable(context.Background(), "sqlite3", db))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()
	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))

	svc := services.NewMetadataService(client, db, cfg, logger, bus)
	require.NoError(t, svc.Register(enrichers.TypeOpenAI, func(context.Context, *ent.User) (enrichers.Enricher, error) {
		return fakeAIEnricher{}, nil
	}))

	syncer := services.NewSyncer(client, cfg, logger, bus, nil)
	h := handlers.New(client, cfg, logger, encryptor, auth.NewJWTManager(testJWTSecret), syncer, svc, nil, nil, nil, nil, bus, nil)
	return client, db, h
}

func createTagWriteUser(t *testing.T, client *ent.Client) *ent.User {
	t.Helper()
	name := fmt.Sprintf("tagwrite_%d", atomic.AddInt64(&tagWriteUserCounter, 1))
	return client.User.Create().SetUsername(name).SetPaginationSize(25).SaveX(context.Background())
}

// callRegenerate invokes a Regenerate-AI handler as the given user.
func callRegenerate(t *testing.T, u *ent.User, path string, fn func(http.ResponseWriter, *http.Request), id int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	fn(w, req)
	return w
}

// assertAITagInTaxonomy is the acceptance criterion for #349: after a
// regenerate the tag is browsable through the taxonomy, without waiting for a
// background enrichment cycle.
func assertAITagInTaxonomy(t *testing.T, client *ent.Client, db *sql.DB, entityType string, entityID int) {
	t.Helper()
	ctx := context.Background()

	tagRow, err := client.Tag.Query().Where(tag.NormalizedNameEQ("ethereal")).Only(ctx)
	require.NoError(t, err, "regenerated AI tag never reached the Tag table")
	assert.Equal(t, "ethereal", tagRow.Name, "display name must be stored trimmed")
	assert.Equal(t, "ai", string(tagRow.TagType), "AI tags keep the ai tag type")

	var projections int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM entity_tags WHERE tag_id = ? AND entity_type = ? AND entity_id = ?`,
		tagRow.ID, entityType, entityID,
	).Scan(&projections))
	assert.Equal(t, 1, projections, "regenerated AI tag missing from entity_tags")
}

func TestArtistRegenerateAIPersistsTypedTags(t *testing.T) {
	client, db, h := setupTagWriteHandler(t)
	ctx := context.Background()
	u := createTagWriteUser(t, client)
	a := client.Artist.Create().SetName("Cocteau Twins").SetUser(u).SaveX(ctx)

	w := callRegenerate(t, u, "/library/artist/1/ai/regenerate", h.ArtistRegenerateAI, a.ID)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assertAITagInTaxonomy(t, client, db, "artist", a.ID)

	tagRow := client.Tag.Query().Where(tag.NormalizedNameEQ("ethereal")).OnlyX(ctx)
	artists, err := tagRow.QueryArtists().Where(artist.HasUserWith(user.ID(u.ID))).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, artists, "regenerated AI tag has no artist edge")

	fresh := client.Artist.GetX(ctx, a.ID)
	assert.Equal(t, []string{"ethereal"}, fresh.AiTags, "deprecated ai_tags JSON is still written until #309 removes it")
}

func TestAlbumRegenerateAIPersistsTypedTags(t *testing.T) {
	client, db, h := setupTagWriteHandler(t)
	ctx := context.Background()
	u := createTagWriteUser(t, client)
	a := client.Artist.Create().SetName("Cocteau Twins").SetUser(u).SaveX(ctx)
	al := client.Album.Create().SetName("Heaven or Las Vegas").SetArtist(a).SetUser(u).SaveX(ctx)

	w := callRegenerate(t, u, "/library/album/1/ai/regenerate", h.AlbumRegenerateAI, al.ID)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assertAITagInTaxonomy(t, client, db, "album", al.ID)

	tagRow := client.Tag.Query().Where(tag.NormalizedNameEQ("ethereal")).OnlyX(ctx)
	albums, err := tagRow.QueryAlbums().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, albums, "regenerated AI tag has no album edge")
}

func TestTrackRegenerateAIPersistsTypedTags(t *testing.T) {
	client, db, h := setupTagWriteHandler(t)
	ctx := context.Background()
	u := createTagWriteUser(t, client)
	a := client.Artist.Create().SetName("Cocteau Twins").SetUser(u).SaveX(ctx)
	al := client.Album.Create().SetName("Heaven or Las Vegas").SetArtist(a).SetUser(u).SaveX(ctx)
	tr := client.Track.Create().SetName("Cherry-coloured Dog").SetArtist(a).SetAlbum(al).SaveX(ctx)

	w := callRegenerate(t, u, "/library/track/1/ai/regenerate", h.TrackRegenerateAI, tr.ID)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assertAITagInTaxonomy(t, client, db, "track", tr.ID)

	tagRow := client.Tag.Query().Where(tag.NormalizedNameEQ("ethereal")).OnlyX(ctx)
	tracks, err := tagRow.QueryTracks().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tracks, "regenerated AI tag has no track edge")
}
