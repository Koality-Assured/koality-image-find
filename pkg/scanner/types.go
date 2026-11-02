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
)

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
