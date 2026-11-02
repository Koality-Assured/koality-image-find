package thumb

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Koality-Assured/koality-image-find/pkg/cache"
	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
)

func createTestPNG(t *testing.T, path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 100, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("Failed to encode PNG: %v", err)
	}
}

func createTestJPEG(t *testing.T, path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x % 255), B: uint8(y % 255), A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("Failed to encode JPEG: %v", err)
	}
}

func TestDecodeAndScale(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "thumb_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pngPath := filepath.Join(tempDir, "sample.png")
	createTestPNG(t, pngPath, 800, 600)

	thumbData, err := DecodeAndScaleImage(pngPath, 200, 200)
	if err != nil {
		t.Fatalf("DecodeAndScaleImage failed: %v", err)
	}

	if len(thumbData) == 0 {
		t.Fatalf("Expected non-empty thumbnail data")
	}

	// Verify it decodes back as valid JPEG with downscaled dimensions
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumbData))
	if err != nil {
		t.Fatalf("Failed to decode generated thumbnail config: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("Expected format jpeg, got %s", format)
	}
	if cfg.Width > 200 || cfg.Height > 200 {
		t.Errorf("Thumbnail dimensions %dx%d exceed target 200x200", cfg.Width, cfg.Height)
	}
}

func TestPipelineWithCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pipe_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	c, err := cache.Open(filepath.Join(tempDir, "cache"), 10)
	if err != nil {
		t.Fatalf("cache.Open failed: %v", err)
	}
	defer c.Close()

	jpgPath := filepath.Join(tempDir, "photo.jpg")
	createTestJPEG(t, jpgPath, 1024, 768)

	fi, err := os.Stat(jpgPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	p := NewPipeline(c)

	// First call: cache miss, generates and caches
	data1, mime1, err := p.GetThumbnail(jpgPath, scanner.MediaTypeImage, fi.Size(), fi.ModTime().Unix(), 256)
	if err != nil {
		t.Fatalf("GetThumbnail failed: %v", err)
	}
	if len(data1) == 0 || mime1 != "image/jpeg" {
		t.Errorf("Invalid thumbnail response: len=%d, mime=%s", len(data1), mime1)
	}

	// Second call: must hit cache
	data2, mime2, err := p.GetThumbnail(jpgPath, scanner.MediaTypeImage, fi.Size(), fi.ModTime().Unix(), 256)
	if err != nil {
		t.Fatalf("Second GetThumbnail failed: %v", err)
	}
	if !bytes.Equal(data1, data2) {
		t.Errorf("Cached data differs from original generation")
	}
	if mime2 != mime1 {
		t.Errorf("Mime differs")
	}
}
