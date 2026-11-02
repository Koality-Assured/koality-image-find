package indexer

import (
	"fmt"
	"testing"
	"time"

	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
)

func TestIndexerBasic(t *testing.T) {
	idx := New()

	files := []scanner.MediaFile{
		{Path: `C:\Photos\vacation_01.jpg`, Name: `vacation_01.jpg`, Ext: `.jpg`, MediaType: scanner.MediaTypeImage, Size: 2048000, ModTime: 1700000000},
		{Path: `C:\Photos\vacation_02.png`, Name: `vacation_02.png`, Ext: `.png`, MediaType: scanner.MediaTypeImage, Size: 4096000, ModTime: 1700000100},
		{Path: `C:\Videos\sunset.mp4`, Name: `sunset.mp4`, Ext: `.mp4`, MediaType: scanner.MediaTypeVideo, Size: 10485760, ModTime: 1700000200},
		{Path: `C:\Videos\clip.mov`, Name: `clip.mov`, Ext: `.mov`, MediaType: scanner.MediaTypeVideo, Size: 8388608, ModTime: 1700000050},
	}

	added := idx.Add(files)
	if added != 4 {
		t.Fatalf("Expected 4 added, got %d", added)
	}

	if idx.Count() != 4 {
		t.Fatalf("Expected count 4, got %d", idx.Count())
	}

	// De-duplication check: adding the same path again should not increment count
	addedSecond := idx.Add(files)
	if addedSecond != 0 {
		t.Fatalf("Expected 0 added for duplicates, got %d", addedSecond)
	}
	if idx.Count() != 4 {
		t.Fatalf("Expected count to remain 4, got %d", idx.Count())
	}

	// Test GetByID
	view, found := idx.GetByID(1)
	if !found || view.Name != "vacation_01.jpg" {
		t.Fatalf("GetByID(1) failed: %+v", view)
	}

	// Test Search by text
	page := idx.GetPage(Query{SearchText: "vacation"}, SortConfig{}, 0, 10)
	if page.TotalCount != 2 {
		t.Errorf("Expected 2 vacation items, got %d", page.TotalCount)
	}

	// Test Filter by Video MediaType
	pageVideos := idx.GetPage(Query{MediaType: scanner.MediaTypeVideo}, SortConfig{}, 0, 10)
	if pageVideos.TotalCount != 2 {
		t.Errorf("Expected 2 videos, got %d", pageVideos.TotalCount)
	}

	// Test Sort by Size Descending
	pageSorted := idx.GetPage(Query{}, SortConfig{Field: SortBySize, Order: SortDesc}, 0, 10)
	if pageSorted.TotalCount != 4 {
		t.Errorf("Expected 4 sorted items, got %d", pageSorted.TotalCount)
	}
	if len(pageSorted.Items) > 0 && pageSorted.Items[0].Name != "sunset.mp4" {
		t.Errorf("Expected largest file to be sunset.mp4 (10MB), got %s", pageSorted.Items[0].Name)
	}
}

func BenchmarkIndexerSearch100k(b *testing.B) {
	idx := New()
	numItems := 100000
	files := make([]scanner.MediaFile, numItems)

	for i := 0; i < numItems; i++ {
		mType := scanner.MediaTypeImage
		ext := ".jpg"
		if i%5 == 0 {
			mType = scanner.MediaTypeVideo
			ext = ".mp4"
		}
		files[i] = scanner.MediaFile{
			Path:      fmt.Sprintf(`C:\Drive\Folder_%d\IMG_%06d%s`, i/1000, i, ext),
			Name:      fmt.Sprintf(`IMG_%06d%s`, i, ext),
			Ext:       ext,
			MediaType: mType,
			Size:      int64(1024 * (i + 1)),
			ModTime:   time.Now().Unix() - int64(i),
		}
	}

	idx.Add(files)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res := idx.GetPage(Query{SearchText: "IMG_05"}, SortConfig{Field: SortByDate, Order: SortDesc}, 0, 100)
		if res.TotalCount == 0 {
			b.Fatal("Expected search matches")
		}
	}
}
