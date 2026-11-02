package scanner

import (
	"strings"
	"time"
)

const (
	MediaTypeUnknown uint8 = 0
	MediaTypeImage   uint8 = 1
	MediaTypeVideo   uint8 = 2
)

// MediaFile represents an indexed image or video file on disk.
type MediaFile struct {
	Path      string
	Name      string
	Ext       string
	MediaType uint8
	Size      int64
	ModTime   int64
	IsCloud   bool
}

// ScanStats holds live metrics during directory scanning.
type ScanStats struct {
	FoldersScanned    int64
	ImagesFound       int64
	VideosFound       int64
	ErrorsEncountered int64
	Duration          time.Duration
	IsComplete        bool
}

var (
	imageExtensions = map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
		".bmp":  true,
		".tiff": true,
		".tif":  true,
		".svg":  true,
		".ico":  true,
		".avif": true,
		".heic": true,
		".heif": true,
		".psd":  true,
		".raw":  true,
		".cr2":  true,
		".nef":  true,
		".arw":  true,
		".dng":  true,
		".orf":  true,
		".rw2":  true,
	}

	videoExtensions = map[string]bool{
		".mp4":  true,
		".mkv":  true,
		".mov":  true,
		".avi":  true,
		".webm": true,
		".wmv":  true,
		".flv":  true,
		".m4v":  true,
		".ts":   true,
		".mts":  true,
		".3gp":  true,
	}

	// personalMediaExtensions defines common media types people normally shoot,
	// record, or save themselves (photos, phone videos, camera RAW, webm/gif clips).
	// Excludes OS icons, vector SVG assets, legacy/system formats, and intermediate files.
	personalMediaExtensions = map[string]bool{
		// Common photo formats
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
		".heic": true,
		".heif": true,
		".dng":  true,
		".cr2":  true,
		".nef":  true,
		".arw":  true,
		// Common video formats
		".mp4":  true,
		".mov":  true,
		".webm": true,
		".mkv":  true,
		".m4v":  true,
	}
)

// IsPersonalMedia returns true if the extension belongs to common personal camera/phone media.
func IsPersonalMedia(ext string) bool {
	lower := strings.ToLower(ext)
	if !strings.HasPrefix(lower, ".") {
		lower = "." + lower
	}
	return personalMediaExtensions[lower]
}

// ClassifyExtension identifies whether an extension corresponds to an image, video, or unknown format.
func ClassifyExtension(ext string) (mediaType uint8, normalizedExt string) {
	lower := strings.ToLower(ext)
	if !strings.HasPrefix(lower, ".") {
		lower = "." + lower
	}
	if imageExtensions[lower] {
		return MediaTypeImage, lower
	}
	if videoExtensions[lower] {
		return MediaTypeVideo, lower
	}
	return MediaTypeUnknown, lower
}
