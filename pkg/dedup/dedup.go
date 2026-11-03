package dedup

import (
	"sort"

	"github.com/Koality-Assured/koality-image-find/pkg/indexer"
	"github.com/Koality-Assured/koality-image-find/pkg/thumb"
)

// Cluster represents a group of visually duplicate or similar media items.
type Cluster struct {
	ID              int                `json:"id"`
	Primary         indexer.ItemView   `json:"primary"`
	Duplicates      []indexer.ItemView `json:"duplicates"`
	MaxDistance     int                `json:"maxDistance"`
	TotalSize       int64              `json:"totalSize"`
	ReclaimableSize int64              `json:"reclaimableSize"`
	Count           int                `json:"count"`
}

// ClusterResult encapsulates paginated clusters and summary statistics.
type ClusterResult struct {
	TotalClusters       int       `json:"totalClusters"`
	TotalDuplicates     int       `json:"totalDuplicates"`
	TotalReclaimableSize int64    `json:"totalReclaimableSize"`
	Offset              int       `json:"offset"`
	Limit               int       `json:"limit"`
	Clusters            []Cluster `json:"clusters"`
}

// ClusterItems takes items and their corresponding 64-bit dHashes,
// clusters connected components using MIH and DSU, designates primary keepers,
// and sorts clusters by reclaimable bytes descending.
// If valid is nil, all elements in hashes are considered valid.
func ClusterItems(items []indexer.ItemView, hashes []uint64, valid []bool, threshold int) []Cluster {
	n := len(items)
	if n < 2 || len(hashes) != n {
		return nil
	}

	if threshold < 0 {
		threshold = 0
	}

	// Step 1: Find matching pairs using Multi-Index Hashing
	mih := NewMIHIndex(hashes, valid)
	pairs := mih.FindPairs(threshold)
	if len(pairs) == 0 {
		return nil
	}

	// Step 2: Group into connected components with Disjoint Set Union
	dsu := NewDSU(n)
	for _, p := range pairs {
		dsu.Union(p[0], p[1])
	}

	// Step 3: Aggregate indices by DSU root
	groups := make(map[int][]int)
	for i := 0; i < n; i++ {
		if valid != nil && !valid[i] {
			continue // Skip uncomputed items
		}
		root := dsu.Find(i)
		groups[root] = append(groups[root], i)
	}

	// Deterministic root ordering to avoid Go map randomization
	rootKeys := make([]int, 0, len(groups))
	for r := range groups {
		rootKeys = append(rootKeys, r)
	}
	sort.Ints(rootKeys)

	// Step 4: Construct clusters for components with 2+ members
	var clusters []Cluster

	for _, rootKey := range rootKeys {
		memberIndices := groups[rootKey]
		if len(memberIndices) < 2 {
			continue
		}

		// Sort member items by quality heuristics:
		// 1. Larger file size (indicates less compression, higher resolution)
		// 2. If size is identical, prefer older modification time (original capture)
		// 3. Fallback to ID for 100% deterministic ordering
		sort.Slice(memberIndices, func(a, b int) bool {
			itemA := items[memberIndices[a]]
			itemB := items[memberIndices[b]]
			if itemA.Size != itemB.Size {
				return itemA.Size > itemB.Size
			}
			if itemA.ModTime != itemB.ModTime {
				return itemA.ModTime < itemB.ModTime
			}
			return itemA.ID < itemB.ID
		})

		primary := items[memberIndices[0]]
		duplicates := make([]indexer.ItemView, 0, len(memberIndices)-1)
		var totalSize int64 = primary.Size
		var maxDist int

		for i := 1; i < len(memberIndices); i++ {
			dup := items[memberIndices[i]]
			duplicates = append(duplicates, dup)
			totalSize += dup.Size
		}

		// Compute max distance within cluster
		for i := 0; i < len(memberIndices); i++ {
			hI := hashes[memberIndices[i]]
			for j := i + 1; j < len(memberIndices); j++ {
				hJ := hashes[memberIndices[j]]
				d := thumb.HammingDistance(hI, hJ)
				if d > maxDist {
					maxDist = d
				}
			}
		}

		reclaimable := totalSize - primary.Size

		clusters = append(clusters, Cluster{
			Primary:         primary,
			Duplicates:      duplicates,
			MaxDistance:     maxDist,
			TotalSize:       totalSize,
			ReclaimableSize: reclaimable,
			Count:           len(memberIndices),
		})
	}

	// Step 5: Sort clusters deterministically with default reclaimable order
	SortClusters(clusters, "reclaimable")
	return clusters
}

// PaginateClusters slices clusters into a paginated ClusterResult with overflow protection.
func PaginateClusters(allClusters []Cluster, offset, limit int) ClusterResult {
	totalClusters := len(allClusters)
	var totalDuplicates int
	var totalReclaimable int64

	for _, c := range allClusters {
		totalDuplicates += len(c.Duplicates)
		totalReclaimable += c.ReclaimableSize
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}

	start := offset
	if start > totalClusters {
		start = totalClusters
	}

	end := start + limit
	if end < start || end > totalClusters {
		end = totalClusters
	}

	count := end - start
	if count < 0 {
		count = 0
	}

	page := make([]Cluster, count)
	if count > 0 {
		copy(page, allClusters[start:end])
	}

	return ClusterResult{
		TotalClusters:        totalClusters,
		TotalDuplicates:      totalDuplicates,
		TotalReclaimableSize: totalReclaimable,
		Offset:               offset,
		Limit:                limit,
		Clusters:             page,
	}
}

// SortClusters sorts clusters according to the requested sort mode and re-indexes cluster IDs.
func SortClusters(clusters []Cluster, sortMode string) {
	sort.Slice(clusters, func(i, j int) bool {
		switch sortMode {
		case "count":
			if clusters[i].Count != clusters[j].Count {
				return clusters[i].Count > clusters[j].Count
			}
			return clusters[i].ReclaimableSize > clusters[j].ReclaimableSize
		case "date_asc":
			if clusters[i].Primary.ModTime != clusters[j].Primary.ModTime {
				return clusters[i].Primary.ModTime < clusters[j].Primary.ModTime
			}
		case "date_desc":
			if clusters[i].Primary.ModTime != clusters[j].Primary.ModTime {
				return clusters[i].Primary.ModTime > clusters[j].Primary.ModTime
			}
		default: // "reclaimable"
			if clusters[i].ReclaimableSize != clusters[j].ReclaimableSize {
				return clusters[i].ReclaimableSize > clusters[j].ReclaimableSize
			}
			if clusters[i].Count != clusters[j].Count {
				return clusters[i].Count > clusters[j].Count
			}
		}
		return clusters[i].Primary.ID < clusters[j].Primary.ID
	})

	for i := range clusters {
		clusters[i].ID = i + 1
	}
}
