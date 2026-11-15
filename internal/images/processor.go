package images

import (
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/tuxedocurly/wledger/internal/config"
	_ "golang.org/x/image/webp"
)

const (
	maxImageDimension = 8192
	maxImagePixels    = 36_000_000
)

func Init() error {
	// Ensure directory exists on startup
	return os.MkdirAll(config.DirUploadsImages, 0755)
}

// ProcessUpload handles the file upload, resize, and saving
// Returns the filename (e.g. "part_12345.jpg").
// The handler is responsible for prepending the web path.
func ProcessUpload(file multipart.File, header *multipart.FileHeader) (string, error) {
	if header == nil {
		return "", fmt.Errorf("missing upload header")
	}

	// Validate extension before reading/decoding image data
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}

	// Inspect header dimensions first to reject decompression bombs before full decode
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		return "", fmt.Errorf("failed to inspect image dimensions: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("invalid image dimensions: %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		return "", fmt.Errorf("image dimensions %dx%d exceed maximum %d", cfg.Width, cfg.Height, maxImageDimension)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return "", fmt.Errorf("image pixel count %d exceeds maximum %d", int64(cfg.Width)*int64(cfg.Height), maxImagePixels)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to rewind image stream: %w", err)
	}

	// Decode image
	img, err := imaging.Decode(file)
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	timestamp := time.Now().UnixNano()
	baseName := fmt.Sprintf("part_%d", timestamp)
	fileName := baseName + ".jpg" // Standardize to JPG

	// Main Image (Fit within 1024x1024)
	mainImg := imaging.Fit(img, 1024, 1024, imaging.Lanczos)

	// Thumbnail (Square crop 300x300)
	thumbImg := imaging.Thumbnail(img, 300, 300, imaging.Lanczos)

	// Save
	if err := saveJPG(mainImg, fileName); err != nil {
		return "", err
	}
	if err := saveJPG(thumbImg, baseName+"_thumb.jpg"); err != nil {
		return "", err
	}

	return fileName, nil
}

func saveJPG(img image.Image, name string) error {
	path := filepath.Join(config.DirUploadsImages, name)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 85})
}

// DeleteByWebPath takes the full web path (e.g., "/uploads/images/part_123.jpg")
// and removes the corresponding files from the file system.
func DeleteByWebPath(webPath string) {
	if webPath == "" {
		return
	}

	// Extract filename: "/uploads/images/part_123.jpg" -> "part_123.jpg"
	fileName := filepath.Base(webPath)

	// Safety check: avoids accidentally trying to delete directories
	if fileName == "." || fileName == "/" || fileName == "images" {
		return
	}

	// Delete Main Image
	os.Remove(filepath.Join(config.DirUploadsImages, fileName))

	// Delete Thumbnail
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	os.Remove(filepath.Join(config.DirUploadsImages, base+"_thumb.jpg"))
}
