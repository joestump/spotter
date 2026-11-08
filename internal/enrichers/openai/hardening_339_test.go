// Regression tests for issue #339: the album prompt must know whether the album
// has cover art, so the template's cover-art branches actually fire.

package openai

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func albumLLMResponse(content string) llm.ChatResponse {
	resp := llm.ChatResponse{ID: "test", Object: "chat.completion", Model: "gpt-4o"}
	resp.Choices = []llm.ChatChoice{{Index: 0, FinishReason: "stop"}}
	resp.Choices[0].Message.Role = "assistant"
	resp.Choices[0].Message.Content = content
	return resp
}

func writeTestCoverArt(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cover.png")
	file, err := os.Create(path)
	require.NoError(t, err)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())
	return path
}

func albumPromptEnricher(t *testing.T, capture *string) enrichers.AlbumEnricher {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			*capture = string(body)
		}
		response := albumLLMResponse(`{"summary": "An album", "tags": ["art rock"]}`)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "../../../data/prompts"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	albumEnricher, ok := enricher.(enrichers.AlbumEnricher)
	require.True(t, ok)
	return albumEnricher
}

func TestEnrichAlbum_PromptAsksForCoverArtWhenImagesExist(t *testing.T) {
	var captured string
	albumEnricher := albumPromptEnricher(t, &captured)

	album := &ent.Album{
		Name: "Covered Album",
		Year: 1977,
		Edges: ent.AlbumEdges{
			Images: []*ent.AlbumImage{{LocalPath: writeTestCoverArt(t)}},
		},
	}

	_, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	assert.Contains(t, captured, "Dominant Colors")
	assert.Contains(t, captured, "cover_art_commentary")
}

func TestEnrichAlbum_PromptOmitsCoverArtWithoutImages(t *testing.T) {
	var captured string
	albumEnricher := albumPromptEnricher(t, &captured)

	album := &ent.Album{Name: "Uncovered Album", Year: 1977}

	_, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	assert.NotContains(t, captured, "Dominant Colors")
}
