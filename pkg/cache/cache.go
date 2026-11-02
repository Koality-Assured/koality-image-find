package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketThumbnails = []byte("thumbnails")
	bucketMetadata   = []byte("metadata")
	bucketHashes     = []byte("phashes")
)

// ThumbnailCache provides high-speed, memory-mapped thumbnail storage using bbolt
// and an in-memory LRU buffer for the active viewport.
type ThumbnailCache struct {
	db      *bolt.DB
	memMu   sync.RWMutex
	memLRU  map[string][]byte
	memKeys []string
	maxMem  int
}

// Open initializes or creates the bbolt database cache file.
func Open(cacheDir string, maxMemItems int) (*ThumbnailCache, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	dbPath := filepath.Join(cacheDir, "thumbnails.bolt")
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{
		Timeout: 2 * time.Second, // 2s lock timeout
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open bbolt cache (another instance may be running): %w", err)
	}

	// Create buckets
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketThumbnails); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketMetadata); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketHashes); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize buckets: %w", err)
	}

	if maxMemItems <= 0 {
		maxMemItems = 500 // ~30-50MB RAM for fast viewport scrolling
	}

	return &ThumbnailCache{
		db:      db,
		memLRU:  make(map[string][]byte, maxMemItems),
		memKeys: make([]string, 0, maxMemItems),
		maxMem:  maxMemItems,
	}, nil
}

// GenerateKey computes a deterministic cache key from path, size, and modtime.
func GenerateKey(path string, size int64, modTime int64) string {
	hasher := sha256.New()
	hasher.Write([]byte(filepath.Clean(path)))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(strconv.FormatInt(size, 10)))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(strconv.FormatInt(modTime, 10)))
	return hex.EncodeToString(hasher.Sum(nil))[:32] // 32 hex chars
}

// Get retrieves thumbnail bytes from memory LRU or bbolt disk store.
func (c *ThumbnailCache) Get(key string) ([]byte, string, bool) {
	// 1. Check in-memory LRU
	c.memMu.Lock()
	if data, hit := c.memLRU[key]; hit {
		// Promote key to most recently used (back of slice)
		for i, k := range c.memKeys {
			if k == key {
				c.memKeys = append(c.memKeys[:i], c.memKeys[i+1:]...)
				c.memKeys = append(c.memKeys, key)
				break
			}
		}
		c.memMu.Unlock()
		return data, "image/jpeg", true
	}
	c.memMu.Unlock()

	// 2. Check bbolt store
	var thumbnailData []byte
	var mimeType string = "image/jpeg"

	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketThumbnails)
		val := b.Get([]byte(key))
		if val != nil {
			thumbnailData = make([]byte, len(val))
			copy(thumbnailData, val)
		}
		bMeta := tx.Bucket(bucketMetadata)
		if metaVal := bMeta.Get([]byte(key)); metaVal != nil {
			mimeType = string(metaVal)
		}
		return nil
	})

	if err != nil || len(thumbnailData) == 0 {
		return nil, "", false
	}

	// 3. Promote to memory LRU
	c.putMem(key, thumbnailData)

	return thumbnailData, mimeType, true
}

// Put saves thumbnail bytes to bbolt and memory LRU.
func (c *ThumbnailCache) Put(key string, data []byte, mimeType string) error {
	if len(data) == 0 {
		return nil
	}

	err := c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketThumbnails)
		if err := b.Put([]byte(key), data); err != nil {
			return err
		}
		bMeta := tx.Bucket(bucketMetadata)
		if mimeType != "" {
			return bMeta.Put([]byte(key), []byte(mimeType))
		}
		return nil
	})

	if err != nil {
		return err
	}

	c.putMem(key, data)
	return nil
}

func (c *ThumbnailCache) putMem(key string, data []byte) {
	c.memMu.Lock()
	defer c.memMu.Unlock()

	if _, exists := c.memLRU[key]; exists {
		c.memLRU[key] = data
		return
	}

	if len(c.memKeys) >= c.maxMem {
		evictKey := c.memKeys[0]
		c.memKeys = c.memKeys[1:]
		delete(c.memLRU, evictKey)
	}

	c.memKeys = append(c.memKeys, key)
	c.memLRU[key] = data
}

// PutWithHash atomically stores thumbnail bytes, metadata, and perceptual hash in a single transaction.
func (c *ThumbnailCache) PutWithHash(key string, data []byte, mimeType string, hash uint64) error {
	if len(data) == 0 {
		return nil
	}

	err := c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketThumbnails)
		if err := b.Put([]byte(key), data); err != nil {
			return err
		}
		if mimeType != "" {
			bMeta := tx.Bucket(bucketMetadata)
			if err := bMeta.Put([]byte(key), []byte(mimeType)); err != nil {
				return err
			}
		}
		if hash != 0 {
			bHash := tx.Bucket(bucketHashes)
			var buf [8]byte
			for i := 0; i < 8; i++ {
				buf[i] = byte(hash >> (i * 8))
			}
			if err := bHash.Put([]byte(key), buf[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	c.putMem(key, data)
	return nil
}

// PutHash stores a 64-bit perceptual hash for a cache key.
func (c *ThumbnailCache) PutHash(key string, hash uint64) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHashes)
		var buf [8]byte
		for i := 0; i < 8; i++ {
			buf[i] = byte(hash >> (i * 8))
		}
		return b.Put([]byte(key), buf[:])
	})
}

// GetHash retrieves a stored 64-bit perceptual hash.
func (c *ThumbnailCache) GetHash(key string) (uint64, bool) {
	var hash uint64
	var found bool
	_ = c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHashes)
		val := b.Get([]byte(key))
		if len(val) >= 8 {
			for i := 0; i < 8; i++ {
				hash |= uint64(val[i]) << (i * 8)
			}
			found = true
		}
		return nil
	})
	return hash, found
}

// Close gracefully closes the bbolt database.
func (c *ThumbnailCache) Close() error {
	return c.db.Close()
}
