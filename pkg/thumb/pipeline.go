package thumb

import (
	"bytes"
	"errors"
	"image"
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

// Cache returns the underlying ThumbnailCache.
func (p *Pipeline) Cache() *cache.ThumbnailCache {
	return p.cache
}

// ExtractHash returns the 64-bit dHash for an item, checking cache first and computing if necessary.
func (p *Pipeline) ExtractHash(filePath string, mediaType uint8, size int64, modTime int64) (uint64, error) {
	key := cache.GenerateKey(filePath, size, modTime)
	if p.cache != nil {
		if hash, found := p.cache.GetHash(key); found && hash != 0 {
			return hash, nil
		}
	}

	_, _, hash, err := p.GetThumbnailWithHash(filePath, mediaType, size, modTime, 256)
	return hash, err
}

// GetThumbnail retrieves an existing cached thumbnail or generates a new one through the tier pipeline.
func (p *Pipeline) GetThumbnail(filePath string, mediaType uint8, size int64, modTime int64, targetSize int) ([]byte, string, error) {
	data, mime, _, err := p.GetThumbnailWithHash(filePath, mediaType, size, modTime, targetSize)
	return data, mime, err
}

// GetThumbnailWithHash retrieves thumbnail bytes and the 64-bit dHash perceptual hash.
func (p *Pipeline) GetThumbnailWithHash(filePath string, mediaType uint8, size int64, modTime int64, targetSize int) ([]byte, string, uint64, error) {
	if targetSize <= 0 {
		targetSize = 256
	}

	key := cache.GenerateKey(filePath, size, modTime)

	// Step 1: Check memory & disk cache
	if p.cache != nil {
		if data, mime, hit := p.cache.Get(key); hit {
			hash, _ := p.cache.GetHash(key)
			return data, mime, hash, nil
		}
	}

	var thumbBytes []byte
	var hash uint64
	var err error

	ext := strings.ToLower(filepath.Ext(filePath))

	// Step 2: Tiered extraction
	if mediaType == scanner.MediaTypeImage {
		// Tier 1: Try EXIF IFD1 embedded thumbnail
		if ext == ".jpg" || ext == ".jpeg" || ext == ".cr2" || ext == ".nef" || ext == ".dng" {
			thumbBytes, err = ExtractExifThumbnail(filePath)
		}

		// Tier 2: Pure-Go Scaled Decoding & Hashing
		if len(thumbBytes) == 0 {
			thumbBytes, hash, err = DecodeScaleAndHash(filePath, targetSize, targetSize)
		}

		// Tier 3: Shell COM (RAW, PSD, HEIC, TIFF)
		if len(thumbBytes) == 0 && p.shellPool != nil {
			thumbBytes, hash, err = p.shellPool.ExtractThumbnailWithHash(filePath, targetSize, 2*time.Second)
		}

	} else if mediaType == scanner.MediaTypeVideo {
		// Video Tier 1: Shell COM (Media Foundation hardware decoder for MP4, MOV, WMV, AVI)
		if p.shellPool != nil {
			thumbBytes, hash, err = p.shellPool.ExtractThumbnailWithHash(filePath, targetSize, 3*time.Second)
		}

		// Video Tier 2: FFmpeg CLI pipe fallback
		if len(thumbBytes) == 0 && HasFFmpeg() {
			thumbBytes, err = ExtractVideoFrameFFmpeg(filePath, targetSize, 3*time.Second)
		}
	}

	if len(thumbBytes) == 0 {
		if err != nil {
			return nil, "", 0, err
		}
		return nil, "", 0, errors.New("unable to generate thumbnail for " + filePath)
	}

	// Step 2.5: Ensure 64-bit dHash is computed if extracted directly from EXIF/FFmpeg
	if hash == 0 && len(thumbBytes) > 0 {
		if decoded, _, errDec := image.Decode(bytes.NewReader(thumbBytes)); errDec == nil {
			hash = DHash64Image(decoded)
		}
	}

	// Step 3: Store in cache atomically in a single bbolt transaction
	if p.cache != nil {
		_ = p.cache.PutWithHash(key, thumbBytes, "image/jpeg", hash)
	}

	return thumbBytes, "image/jpeg", hash, nil
}
