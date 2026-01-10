package enrichers

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/webp"
)

const (
	// MaxImageSize defines the maximum width or height for downloaded images.
	MaxImageSize = 1024
)

// DownloadAndSaveImage fetches an image from a URL, resizes it if necessary,
// converts it to PNG format, and saves it to the specified local path.
// It creates the destination directory if it doesn't exist.
// It returns the final path of the saved image or an error if any step fails.
func DownloadAndSaveImage(url, localPath string, logger *slog.Logger) (string, error) {
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

	// Fetch the image from the provided URL
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to start image download from %s: %w", url, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Warn("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image, status: %s", resp.Status)
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
	if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
		return "", fmt.Errorf("failed to create image directory %s: %w", dir, mkdirErr)
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
