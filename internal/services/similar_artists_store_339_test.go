// Regression tests for issue #339: storing an AI response whose artists match
// nothing must not wipe the relationships already on file, and a replace must
// happen in one write.

package services

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/similarartist"
	"spotter/internal/config"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// similarArtistCandidate aliases the anonymous parameter type of
// storeSimilarArtists so tests can build the slice readably.
type similarArtistCandidate = struct {
	Name       string  `json:"name"`
	ID         int     `json:"id"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type similarArtistsFixture struct {
	client       *ent.Client
	svc          *SimilarArtistsService
	user         *ent.User
	sourceArtist *ent.Artist
	stored       *ent.Artist
}

func setupSimilarArtistsStore(t *testing.T) similarArtistsFixture {
	t.Helper()

	dbName := strings.NewReplacer("/", "_", " ", "_", "=", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", "file:"+dbName+"?mode=memory&_fk=1")
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	u, err := client.User.Create().SetUsername(dbName).Save(ctx)
	require.NoError(t, err)

	sourceArtist, err := client.Artist.Create().SetName("Source Artist").SetUser(u).Save(ctx)
	require.NoError(t, err)

	stored, err := client.Artist.Create().SetName("Previously Similar").SetUser(u).Save(ctx)
	require.NoError(t, err)

	_, err = client.SimilarArtist.Create().
		SetSourceArtist(sourceArtist).
		SetSimilarArtist(stored).
		SetUser(u).
		SetProvider(ProviderOpenAI).
		SetConfidence(0.9).
		SetRank(1).
		Save(ctx)
	require.NoError(t, err)

	svc := NewSimilarArtistsService(client, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	return similarArtistsFixture{client: client, svc: svc, user: u, sourceArtist: sourceArtist, stored: stored}
}

func TestStoreSimilarArtists_KeepsExistingWhenNothingMatches(t *testing.T) {
	f := setupSimilarArtistsStore(t)

	err := f.svc.storeSimilarArtists(context.Background(), f.user.ID, f.sourceArtist.ID,
		[]similarArtistCandidate{{Name: "Not In Library", ID: 9999, Confidence: 0.8, Reason: "hallucinated"}})

	require.Error(t, err)

	rows, err := f.client.SimilarArtist.Query().WithSimilarArtist().All(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1, "prior relationships must survive a response that matches nothing")
	assert.Equal(t, f.stored.ID, rows[0].Edges.SimilarArtist.ID)
}

func TestStoreSimilarArtists_ReplacesMatchesInOneWrite(t *testing.T) {
	f := setupSimilarArtistsStore(t)

	ctx := context.Background()
	replacement, err := f.client.Artist.Create().SetName("Fresh Match").SetUser(f.user).Save(ctx)
	require.NoError(t, err)

	err = f.svc.storeSimilarArtists(ctx, f.user.ID, f.sourceArtist.ID, []similarArtistCandidate{
		{Name: "Fresh Match", ID: 0, Confidence: 0.7, Reason: "same era"},
		// The same artist mentioned twice must not fail the unique constraint.
		{Name: "Fresh Match", ID: replacement.ID, Confidence: 0.6, Reason: "repeat mention"},
	})
	require.NoError(t, err)

	rows, err := f.client.SimilarArtist.Query().
		WithSimilarArtist().
		Order(ent.Asc(similarartist.FieldRank)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, replacement.ID, rows[0].Edges.SimilarArtist.ID)
	assert.Equal(t, 1, rows[0].Rank)
}
