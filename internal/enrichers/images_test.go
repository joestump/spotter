package enrichers

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDownloadAndSaveImage(t *testing.T) {
	// Create a dummy logger that discards output
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()

	// Test Case 1: Successful download and save
	t.Run("successful download and save", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			png.Encode(w, img)
		}))
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "test.png")

		resultPath, err := DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.NoError(t, err)
		assert.Equal(t, localPath, resultPath)
		assert.FileExists(t, localPath)
	})

	// Test Case 2: File already exists
	t.Run("file already exists", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This handler should not be called
			t.Fatal("server was called when it should have been skipped")
		}))
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "existing.png")

		// Create the file beforehand
		f, err := os.Create(localPath)
		assert.NoError(t, err)
		f.Close()

		resultPath, err := DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.NoError(t, err)
		assert.Equal(t, localPath, resultPath)
	})

	// Test Case 3: Invalid URL (server error)
	t.Run("invalid url", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "notfound.png")

		_, err := DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.Error(t, err)
	})

	// Test Case 4: Bad image data
	t.Run("bad image data", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "this is not an image")
		}))
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "bad.png")

		_, err := DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.Error(t, err)
	})

	// Test Case 5: Directory creation failure (read-only)
	t.Run("directory creation failure", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("skipping test as root user")
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			png.Encode(w, img)
		}))
		defer server.Close()

		// Create a read-only directory
		tempDir := t.TempDir()
		readOnlyDir := filepath.Join(tempDir, "readonly")
		err := os.Mkdir(readOnlyDir, 0555)
		assert.NoError(t, err)

		localPath := filepath.Join(readOnlyDir, "subdir", "image.png")

		_, err = DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.Error(t, err)
	})

	// Test Case 6: Image resizing
	t.Run("image resizing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			img := image.NewRGBA(image.Rect(0, 0, MaxImageSize+100, MaxImageSize+100))
			png.Encode(w, img)
		}))
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "resized.png")

		_, err := DownloadAndSaveImage(ctx, server.URL, localPath, logger)
		assert.NoError(t, err)

		f, err := os.Open(localPath)
		assert.NoError(t, err)
		defer f.Close()

		img, _, err := image.DecodeConfig(f)
		assert.NoError(t, err)
		assert.Equal(t, MaxImageSize, img.Width)
	})

	// Test Case 7: A cancelled context aborts the download
	t.Run("aborts on context cancellation", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			png.Encode(w, img)
		}))
		defer close(release)
		defer server.Close()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "cancelled.png")

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := DownloadAndSaveImage(canceledCtx, server.URL, localPath, logger)
		assert.ErrorIs(t, err, context.Canceled)
		assert.NoFileExists(t, localPath)
	})

	// Test Case 8: A stalled image host is cut off by the client timeout
	t.Run("aborts on client timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			png.Encode(w, img)
		}))
		defer server.Close()

		orig := imageDownloadClient
		imageDownloadClient = &http.Client{Timeout: 50 * time.Millisecond}
		defer func() { imageDownloadClient = orig }()

		tempDir := t.TempDir()
		localPath := filepath.Join(tempDir, "timeout.png")

		_, err := DownloadAndSaveImage(context.Background(), server.URL, localPath, logger)
		assert.Error(t, err)
		assert.NoFileExists(t, localPath)
	})
}
