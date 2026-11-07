// Regression tests for issue #339: an LLM response with no choices must come back
// as an error, and a listen whose track has no artist must be skipped cleanly.

package vibes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/llm"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallOpenAI_EmptyChoicesReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{
			ID:      "empty",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gpt-4o",
			Choices: []llm.ChatChoice{},
		})
	}))
	defer server.Close()

	client := enttest.Open(t, "sqlite3", "file:ent_empty_choices?mode=memory&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-api-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"

	g := NewMixtapeGenerator(client, cfg, nil, events.NewBus())

	content, tokensUsed, err := g.callOpenAI(context.Background(), "give me a mixtape")
	require.Error(t, err)
	assert.Empty(t, content)
	assert.Zero(t, tokensUsed)
	assert.Contains(t, err.Error(), "no choices")
}

func TestGetListeningHistory_SkipsNilArtistTrack(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent_artistless_history?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	u, err := client.User.Create().SetUsername("artistless").Save(ctx)
	require.NoError(t, err)

	orphanTrack, err := client.Track.Create().SetName("Anonymous Tune").Save(ctx)
	require.NoError(t, err)

	_, err = client.Listen.Create().
		SetUser(u).
		SetTrack(orphanTrack).
		SetTrackName(orphanTrack.Name).
		SetArtistName("").
		SetAlbumName("").
		SetSource("navidrome").
		SetPlayedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	history, err := enhancer.getListeningHistory(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "Anonymous Tune", history[0].TrackName)
	assert.Empty(t, history[0].ArtistName)
}
