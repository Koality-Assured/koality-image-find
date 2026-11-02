package cache

import (
	"os"
	"testing"
)

func TestThumbnailCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "koality_cache_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	c, err := Open(tempDir, 3)
	if err != nil {
		t.Fatalf("Failed to open cache: %v", err)
	}
	defer c.Close()

	key1 := GenerateKey(`C:\Photos\test1.jpg`, 1024, 1700000000)
	key2 := GenerateKey(`C:\Photos\test2.jpg`, 2048, 1700000100)
	key3 := GenerateKey(`C:\Photos\test3.jpg`, 3072, 1700000200)
	key4 := GenerateKey(`C:\Photos\test4.jpg`, 4096, 1700000300)

	data1 := []byte("dummy-thumb-1")
	data2 := []byte("dummy-thumb-2")
	data3 := []byte("dummy-thumb-3")
	data4 := []byte("dummy-thumb-4")

	if err := c.Put(key1, data1, "image/jpeg"); err != nil {
		t.Fatalf("Put key1 failed: %v", err)
	}
	if err := c.Put(key2, data2, "image/jpeg"); err != nil {
		t.Fatalf("Put key2 failed: %v", err)
	}
	if err := c.Put(key3, data3, "image/jpeg"); err != nil {
		t.Fatalf("Put key3 failed: %v", err)
	}

	// Verify hit
	got, mime, hit := c.Get(key1)
	if !hit || string(got) != string(data1) || mime != "image/jpeg" {
		t.Fatalf("Get key1 mismatch: hit=%v, got=%s, mime=%s", hit, string(got), mime)
	}

	// Insert 4th item, LRU should evict oldest from memory but disk store still has it
	if err := c.Put(key4, data4, "image/jpeg"); err != nil {
		t.Fatalf("Put key4 failed: %v", err)
	}

	// key1 should still be readable from bbolt
	gotBack, _, hitBack := c.Get(key1)
	if !hitBack || string(gotBack) != string(data1) {
		t.Fatalf("Get key1 from disk failed: hit=%v, got=%s", hitBack, string(gotBack))
	}
}
