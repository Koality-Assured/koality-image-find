package dedup

import (
	"testing"

	"github.com/Koality-Assured/koality-image-find/pkg/indexer"
)

func TestDSUBasic(t *testing.T) {
	dsu := NewDSU(6)
	if dsu.Count() != 6 {
		t.Fatalf("Expected initial count 6, got %d", dsu.Count())
	}

	dsu.Union(0, 1)
	dsu.Union(1, 2)
	if dsu.Count() != 4 {
		t.Errorf("Expected count 4 after 2 unions, got %d", dsu.Count())
	}

	if dsu.Find(0) != dsu.Find(2) {
		t.Errorf("Expected 0 and 2 to be in the same set")
	}

	if dsu.Find(0) == dsu.Find(3) {
		t.Errorf("Expected 0 and 3 to be in different sets")
	}

	// Redundant union
	if dsu.Union(0, 2) {
		t.Errorf("Expected false for already connected elements")
	}
}

func TestMIHExactAndNear(t *testing.T) {
	// Base hash: 0xAAAAAAAAAAAAAAAA (alternating bits)
	base := uint64(0xAAAAAAAAAAAAAAAA)

	h0 := base
	h1 := base                         // d = 0 (exact)
	h2 := base ^ (1 << 5)              // d = 1 (1 bit in chunk 0)
	h3 := base ^ (1 << 20)             // d = 1 (1 bit in chunk 1)
	h4 := base ^ (1<<5 | 1<<20 | 1<<35) // d = 3 (1 bit in each of chunks 0, 1, 2)
	h5 := base ^ (1<<5 | 1<<6 | 1<<20 | 1<<21 | 1<<35 | 1<<36) // d = 6 (2 bits in chunks 0, 1, 2)
	hFar := ^base                      // d = 64 (completely opposite)

	hashes := []uint64{h0, h1, h2, h3, h4, h5, hFar}
	mih := NewMIHIndex(hashes, nil)

	// Threshold 0 (exact only: h0 and h1)
	pairs0 := mih.FindPairs(0)
	if len(pairs0) != 1 || pairs0[0] != [2]int{0, 1} {
		t.Fatalf("Expected 1 exact pair (0, 1), got %+v", pairs0)
	}

	// Threshold 1 (pairs: {0,1}, {0,2}, {0,3}, {1,2}, {1,3})
	pairs1 := mih.FindPairs(1)
	if len(pairs1) < 4 {
		t.Errorf("Expected at least 4 pairs for threshold 1, got %d", len(pairs1))
	}

	// Threshold 3: h4 (d=3) should pair with h0 and h1
	pairs3 := mih.FindPairs(3)
	foundH4 := false
	for _, p := range pairs3 {
		if (p[0] == 0 && p[1] == 4) || (p[0] == 1 && p[1] == 4) {
			foundH4 = true
			break
		}
	}
	if !foundH4 {
		t.Errorf("Expected pair (0,4) or (1,4) in threshold 3")
	}

	// Threshold 6: h5 (d=6) should pair with h0
	pairs6 := mih.FindPairs(6)
	foundH5 := false
	for _, p := range pairs6 {
		if (p[0] == 0 && p[1] == 5) || (p[0] == 1 && p[1] == 5) {
			foundH5 = true
			break
		}
	}
	if !foundH5 {
		t.Errorf("Expected pair (0,5) or (1,5) in threshold 6")
	}

	// Far hash should never pair
	for _, p := range pairs6 {
		if p[0] == 6 || p[1] == 6 {
			t.Errorf("hFar (d=64) unexpectedly matched in pair %+v", p)
		}
	}
}

func TestClusterItems(t *testing.T) {
	baseHash := uint64(0x123456789ABCDEF0)

	items := []indexer.ItemView{
		{ID: 1, Name: "original_highres.jpg", Size: 8000000, ModTime: 1000},
		{ID: 2, Name: "copy_smaller.jpg", Size: 4000000, ModTime: 2000},
		{ID: 3, Name: "copy_lowest.jpg", Size: 2000000, ModTime: 3000},
		{ID: 4, Name: "distinct_photo.jpg", Size: 5000000, ModTime: 1500},
	}

	hashes := []uint64{
		baseHash,
		baseHash ^ (1 << 2), // d = 1
		baseHash ^ (1 << 4), // d = 1
		^baseHash,           // d = 64
	}

	clusters := ClusterItems(items, hashes, nil, 3)
	if len(clusters) != 1 {
		t.Fatalf("Expected 1 cluster, got %d", len(clusters))
	}

	c := clusters[0]
	if c.Count != 3 {
		t.Errorf("Expected cluster count 3, got %d", c.Count)
	}

	if c.Primary.Name != "original_highres.jpg" {
		t.Errorf("Expected primary to be original_highres.jpg (8MB), got %s", c.Primary.Name)
	}

	if len(c.Duplicates) != 2 {
		t.Fatalf("Expected 2 duplicates, got %d", len(c.Duplicates))
	}

	expectedReclaimable := int64(4000000 + 2000000)
	if c.ReclaimableSize != expectedReclaimable {
		t.Errorf("Expected reclaimable size %d, got %d", expectedReclaimable, c.ReclaimableSize)
	}

	// Test pagination
	res := PaginateClusters(clusters, 0, 10)
	if res.TotalClusters != 1 || res.TotalDuplicates != 2 || res.TotalReclaimableSize != expectedReclaimable {
		t.Errorf("PaginateClusters result mismatch: %+v", res)
	}
}
