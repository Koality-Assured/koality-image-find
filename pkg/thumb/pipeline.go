package thumb

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/Koality-Assured/koality-image-find/pkg/cache"
	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
)

// Pipeline manages tiered thumbnail extraction with caching.
type Pipeline struct {
	cache     *cache.ThumbnailCache
	shellPool *ShellWorkerPool
}

// NewPipeline creates a new thumbnail extraction pipeline.
func NewPipeline(c *cache.ThumbnailCache) *Pipeline {
	return &Pipeline{
		cache:     c,
		shellPool: GetShellPool(),
	}
}

// GetThumbnail retrieves an existing cached thumbnail or generates a new one through the tier pipeline.
func (p *Pipeline) GetThumbnail(filePath string, mediaType uint8, size int64, modTime int64, targetSize int) ([]byte, string, error) {
	if targetSize <= 0 {
		targetSize = 256
	}

	key := cache.GenerateKey(filePath, size, modTime)

	// Step 1: Check memory & disk cache
	if p.cache != nil {
		if data, mime, hit := p.cache.Get(key); hit {
			return data, mime, nil
		}
	}

	var thumbBytes []byte
	var err error

	ext := strings.ToLower(filepath.Ext(filePath))

	// Step 2: Tiered extraction
	if mediaType == scanner.MediaTypeImage {
		// Tier 1: Try EXIF IFD1 embedded thumbnail (instant <0.3ms for JPEGs/RAW)
		if ext == ".jpg" || ext == ".jpeg" || ext == ".cr2" || ext == ".nef" || ext == ".dng" {
			thumbBytes, err = ExtractExifThumbnail(filePath)
		}

		// Tier 2: Pure-Go Scaled Decoding
		if len(thumbBytes) == 0 {
			thumbBytes, err = DecodeAndScaleImage(filePath, targetSize, targetSize)
		}

		// Tier 3: Shell COM (RAW, PSD, HEIC, TIFF)
		if len(thumbBytes) == 0 && p.shellPool != nil {
			thumbBytes, err = p.shellPool.ExtractThumbnail(filePath, targetSize, 2*time.Second)
		}

	} else if mediaType == scanner.MediaTypeVideo {
		// Video Tier 1: Shell COM (Media Foundation hardware decoder for MP4, MOV, WMV, AVI)
		if p.shellPool != nil {
			thumbBytes, err = p.shellPool.ExtractThumbnail(filePath, targetSize, 3*time.Second)
		}

		// Video Tier 2: FFmpeg CLI pipe fallback for exotic containers (MKV, WebM, FLV)
		if len(thumbBytes) == 0 && HasFFmpeg() {
			thumbBytes, err = ExtractVideoFrameFFmpeg(filePath, targetSize, 3*time.Second)
		}
	}

	if len(thumbBytes) == 0 {
		if err != nil {
			return nil, "", err
		}
		return nil, "", errors.New("unable to generate thumbnail for " + filePath)
	}

	// Step 3: Store in cache
	if p.cache != nil {
		_ = p.cache.Put(key, thumbBytes, "image/jpeg")
	}

	return thumbBytes, "image/jpeg", nil
}
