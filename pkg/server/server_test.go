package server

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Koality-Assured/koality-image-find/pkg/cache"
	"github.com/Koality-Assured/koality-image-find/pkg/indexer"
	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
	"github.com/Koality-Assured/koality-image-find/pkg/thumb"
)

func createTestPNGFile(t *testing.T, path string) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
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

func TestServerAuthAndEndpoints(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "srv_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pngPath := filepath.Join(tempDir, "photo.png")
	createTestPNGFile(t, pngPath)
	fi, _ := os.Stat(pngPath)

	idx := indexer.New()
	idx.Add([]scanner.MediaFile{
		{
			Path:      pngPath,
			Name:      "photo.png",
			Ext:       ".png",
			MediaType: scanner.MediaTypeImage,
			Size:      fi.Size(),
			ModTime:   fi.ModTime().Unix(),
		},
	})

	c, _ := cache.Open(filepath.Join(tempDir, "cache"), 10)
	defer c.Close()
	pipe := thumb.NewPipeline(c)

	srv := New(ServerConfig{
		Indexer:      idx,
		Pipeline:     pipe,
		SessionToken: "test-secret-token-12345",
	})

	baseURL, err := srv.Start()
	if err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer srv.Close()

	client := &http.Client{}

	// 1. Test unauthorized request without token (must return 401)
	reqUnauth, _ := http.NewRequest("GET", baseURL+"/api/items", nil)
	respUnauth, err := client.Do(reqUnauth)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d", respUnauth.StatusCode)
	}

	// 2. Test authorized request with X-Session-Token header
	reqAuth, _ := http.NewRequest("GET", baseURL+"/api/items", nil)
	reqAuth.Header.Set("X-Session-Token", "test-secret-token-12345")
	respAuth, err := client.Do(reqAuth)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer respAuth.Body.Close()
	if respAuth.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", respAuth.StatusCode)
	}

	var page indexer.PageResult
	if err := json.NewDecoder(respAuth.Body).Decode(&page); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if page.TotalCount != 1 || len(page.Items) != 1 {
		t.Errorf("Expected 1 item, got %+v", page)
	}

	// 3. Test thumbnail retrieval
	reqThumb, _ := http.NewRequest("GET", baseURL+"/api/thumbnail?id=1&token=test-secret-token-12345", nil)
	respThumb, err := client.Do(reqThumb)
	if err != nil {
		t.Fatalf("Thumbnail request failed: %v", err)
	}
	defer respThumb.Body.Close()
	if respThumb.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK for thumbnail, got %d", respThumb.StatusCode)
	}
	if respThumb.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("Expected image/jpeg Content-Type, got %s", respThumb.Header.Get("Content-Type"))
	}

	// 4. Test media byte range request
	reqMedia, _ := http.NewRequest("GET", baseURL+"/api/media?id=1&token=test-secret-token-12345", nil)
	reqMedia.Header.Set("Range", "bytes=0-10")
	respMedia, err := client.Do(reqMedia)
	if err != nil {
		t.Fatalf("Media request failed: %v", err)
	}
	defer respMedia.Body.Close()
	if respMedia.StatusCode != http.StatusPartialContent {
		t.Errorf("Expected 206 Partial Content for byte range, got %d", respMedia.StatusCode)
	}

	// 5. Add a duplicate image and test /api/duplicates
	dupPath := filepath.Join(tempDir, "photo_copy.png")
	createTestPNGFile(t, dupPath)
	fiDup, _ := os.Stat(dupPath)
	idx.Add([]scanner.MediaFile{
		{
			Path:      dupPath,
			Name:      "photo_copy.png",
			Ext:       ".png",
			MediaType: scanner.MediaTypeImage,
			Size:      fiDup.Size(),
			ModTime:   fiDup.ModTime().Unix(),
		},
	})

	reqDedup, _ := http.NewRequest("GET", baseURL+"/api/duplicates?token=test-secret-token-12345&compute=1&threshold=3", nil)
	respDedup, err := client.Do(reqDedup)
	if err != nil {
		t.Fatalf("Dedup request failed: %v", err)
	}
	defer respDedup.Body.Close()
	if respDedup.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK for /api/duplicates, got %d", respDedup.StatusCode)
	}

	var dedupResp DuplicatesResponse
	if err := json.NewDecoder(respDedup.Body).Decode(&dedupResp); err != nil {
		t.Fatalf("Dedup decode failed: %v", err)
	}

	if dedupResp.TotalClusters != 1 {
		t.Errorf("Expected 1 duplicate cluster, got %d", dedupResp.TotalClusters)
	}
	if len(dedupResp.Clusters) > 0 && len(dedupResp.Clusters[0].Duplicates) != 1 {
		t.Errorf("Expected 1 duplicate item in cluster, got %d", len(dedupResp.Clusters[0].Duplicates))
	}

	// 6. Test preset=personal endpoint filtering
	idx.Add([]scanner.MediaFile{
		{
			Path:      filepath.Join(tempDir, "icon.ico"),
			Name:      "icon.ico",
			Ext:       ".ico",
			MediaType: scanner.MediaTypeImage,
			Size:      1024,
			ModTime:   1000,
		},
	})
	reqPersonal, _ := http.NewRequest("GET", baseURL+"/api/items?preset=personal&token=test-secret-token-12345", nil)
	respPersonal, err := client.Do(reqPersonal)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer respPersonal.Body.Close()
	var personalPage indexer.PageResult
	_ = json.NewDecoder(respPersonal.Body).Decode(&personalPage)
	if personalPage.TotalCount != 2 { // Only photo.png and photo_copy.png, NOT icon.ico
		t.Errorf("Expected 2 personal items, got %d", personalPage.TotalCount)
	}
}
