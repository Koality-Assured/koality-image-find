package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScannerClassification(t *testing.T) {
	testCases := []struct {
		ext          string
		wantType     uint8
		wantNormExt  string
	}{
		{".jpg", MediaTypeImage, ".jpg"},
		{".JPEG", MediaTypeImage, ".jpeg"},
		{".PNG", MediaTypeImage, ".png"},
		{".gif", MediaTypeImage, ".gif"},
		{".webp", MediaTypeImage, ".webp"},
		{".cr2", MediaTypeImage, ".cr2"},
		{".mp4", MediaTypeVideo, ".mp4"},
		{".MKV", MediaTypeVideo, ".mkv"},
		{".mov", MediaTypeVideo, ".mov"},
		{".txt", MediaTypeUnknown, ".txt"},
		{".exe", MediaTypeUnknown, ".exe"},
	}

	for _, tc := range testCases {
		mType, nExt := ClassifyExtension(tc.ext)
		if mType != tc.wantType || nExt != tc.wantNormExt {
			t.Errorf("ClassifyExtension(%q) = (%v, %q); want (%v, %q)",
				tc.ext, mType, nExt, tc.wantType, tc.wantNormExt)
		}
	}
}

func TestScannerDirectoryTraversal(t *testing.T) {
	// Create temporary directory structure with dummy images and videos
	tempDir, err := os.MkdirTemp("", "koality_scan_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subDir1 := filepath.Join(tempDir, "sub1")
	subDir2 := filepath.Join(tempDir, "sub2")
	os.MkdirAll(subDir1, 0755)
	os.MkdirAll(subDir2, 0755)

	// Create test files
	filesToCreate := []string{
		filepath.Join(tempDir, "photo1.jpg"),
		filepath.Join(tempDir, "photo2.png"),
		filepath.Join(tempDir, "doc.pdf"), // Ignored
		filepath.Join(subDir1, "video1.mp4"),
		filepath.Join(subDir1, "clip.mov"),
		filepath.Join(subDir2, "camera.dng"),
		filepath.Join(subDir2, "script.py"), // Ignored
	}

	for _, f := range filesToCreate {
		if err := os.WriteFile(f, []byte("test-content"), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", f, err)
		}
	}

	var discovered []MediaFile
	config := ScannerConfig{
		Roots:        []string{tempDir},
		MaxWorkers:   4,
		IncludeCloud: false,
		BatchSize:    10,
	}

	s := New(config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats, err := s.Scan(ctx, func(batch []MediaFile) {
		discovered = append(discovered, batch...)
	})

	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	if stats.ImagesFound != 3 {
		t.Errorf("Expected 3 images found, got %d", stats.ImagesFound)
	}
	if stats.VideosFound != 2 {
		t.Errorf("Expected 2 videos found, got %d", stats.VideosFound)
	}
	if len(discovered) != 5 {
		t.Errorf("Expected 5 discovered files, got %d", len(discovered))
	}
}
