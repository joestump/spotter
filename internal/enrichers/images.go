// Governing: ADR-0027 (image storage: local filesystem),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-031 (images resized using pure-Go nfnt/resize),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-032 (WebP, PNG, JPEG, GIF formats supported via stdlib + x/image/webp),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-033 (existing local images skipped unless forced refresh)
package enrichers

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/webp"

	"spotter/internal/resilience"
)

const (
	// MaxImageSize defines the maximum width or height for downloaded images.
	MaxImageSize = 1024

	// imageDownloadTimeout bounds how long a single image download may take.
	// Governing: ADR-0020 (error handling and resilience), issue #333.
	imageDownloadTimeout = 30 * time.Second
)

// imageDownloadClient replaces http.Get (whose DefaultClient has no timeout)
// so one stalled image host cannot wedge an entire enrichment run.
var imageDownloadClient = &http.Client{Timeout: imageDownloadTimeout}

// DownloadAndSaveImage fetches an image from a URL, resizes it if necessary,
// converts it to PNG format, and saves it to the specified local path.
// It creates the destination directory if it doesn't exist.
// The download honors ctx cancellation and is bounded by imageDownloadTimeout.
// It returns the final path of the saved image or an error if any step fails.
func DownloadAndSaveImage(ctx context.Context, url, localPath string, logger *slog.Logger) (string, error) {
	if url == "" {
		return "", fmt.Errorf("image URL cannot be empty")
	}
	if localPath == "" {
		return "", fmt.Errorf("local path cannot be empty")
	}

	// Check if the file already exists. If so, do not re-download.
	if _, err := os.Stat(localPath); err == nil {
		logger.Debug("Image already exists locally, skipping download", "path", localPath)
		return localPath, nil
	}

	logger.Debug("Downloading image", "url", url)

	// Fetch the image from the provided URL with a bounded, cancellable request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create image download request for %s: %w", url, err)
	}
	resp, err := imageDownloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to start image download from %s: %w", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return "", resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("failed to download image, status: %s", resp.Status))
	}

	// Decode the image data. The blank import of image decoders allows
	// image.Decode to handle various formats (jpeg, gif, png, webp).
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to decode image from %s: %w", url, err)
	}

	// Resize the image if its dimensions exceed the maximum allowed size.
	if img.Bounds().Dx() > MaxImageSize || img.Bounds().Dy() > MaxImageSize {
		img = resize.Resize(MaxImageSize, 0, img, resize.Lanczos3)
		logger.Debug("Resized image", "url", url, "new_width", img.Bounds().Dx())
	}

	// Ensure the destination directory exists.
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create image directory %s: %w", dir, err)
	}

	// Create the destination file.
	file, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to create image file %s: %w", localPath, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warn("failed to close file", "error", err)
		}
	}()

	// Encode the (potentially resized) image as a PNG and save it to the file.
	if err := png.Encode(file, img); err != nil {
		return "", fmt.Errorf("failed to encode image to png: %w", err)
	}

	logger.Debug("Successfully saved image", "path", localPath)

	return localPath, nil
}
