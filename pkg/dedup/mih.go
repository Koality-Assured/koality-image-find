package dedup

import (
	"runtime"
	"sync"

	"github.com/Koality-Assured/koality-image-find/pkg/thumb"
)

// MIHIndex implements Multi-Index Hashing for 64-bit perceptual hashes.
// It partitions 64-bit hashes into 4 x 16-bit blocks (65,536 buckets per block)
// guaranteeing 100% recall for exact and near duplicates with sub-millisecond lookups.
type MIHIndex struct {
	hashes []uint64
	valid  []bool
	// Lazily allocated 4 tables of 65,536 buckets (~6.3MB)
	tables *[4][65536][]int
}

// NewMIHIndex creates a Multi-Index Hash from a slice of 64-bit hashes.
// If valid is nil, all elements in hashes are assumed valid.
func NewMIHIndex(hashes []uint64, valid []bool) *MIHIndex {
	return &MIHIndex{
		hashes: hashes,
		valid:  valid,
	}
}

func (idx *MIHIndex) build() {
	if idx.tables != nil {
		return
	}
	idx.tables = new([4][65536][]int)

	for i, h := range idx.hashes {
		if idx.valid != nil && !idx.valid[i] {
			continue // Skip uncomputed hashes
		}
		c0 := uint16(h)
		c1 := uint16(h >> 16)
		c2 := uint16(h >> 32)
		c3 := uint16(h >> 48)

		idx.tables[0][c0] = append(idx.tables[0][c0], i)
		idx.tables[1][c1] = append(idx.tables[1][c1], i)
		idx.tables[2][c2] = append(idx.tables[2][c2], i)
		idx.tables[3][c3] = append(idx.tables[3][c3], i)
	}
}

// FindPairs finds all unique pairs (i, j) where i < j and HammingDistance(hashes[i], hashes[j]) <= threshold.
func (idx *MIHIndex) FindPairs(threshold int) [][2]int {
	n := len(idx.hashes)
	if n < 2 {
		return nil
	}

	// Strategy:
	// For threshold <= 3: Exactly 1 chunk matches (Pigeonhole: floor(3/4) = 0).
	// For threshold <= 7: At least 1 chunk has <= 1 bit difference (Pigeonhole: floor(7/4) = 1).
	// For threshold > 7 and N <= 25,000: Parallel pairwise scan is faster than 2-bit neighbor bucket explosion.
	if threshold > 7 || n <= 500 {
		return idx.findPairsDirectParallel(threshold)
	}

	idx.build()
	return idx.findPairsMIH(threshold)
}

// findPairsMIH queries the 4 hash tables with 0-bit or 1-bit neighbor exploration.
func (idx *MIHIndex) findPairsMIH(threshold int) [][2]int {
	n := len(idx.hashes)
	check1Bit := threshold > 3

	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (n + numWorkers - 1) / numWorkers

	results := make([][][2]int, numWorkers)
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= n {
			break
		}

		wg.Add(1)
		go func(workerID, sIdx, eIdx int) {
			defer wg.Done()
			var localPairs [][2]int
			// Track visited candidates per query item to prevent duplicate candidate checks
			visited := make([]int, n)
			visitToken := 0

			for i := sIdx; i < eIdx; i++ {
				if idx.valid != nil && !idx.valid[i] {
					continue
				}
				h := idx.hashes[i]

				visitToken++
				chunks := [4]uint16{
					uint16(h),
					uint16(h >> 16),
					uint16(h >> 32),
					uint16(h >> 48),
				}

				for tableIdx := 0; tableIdx < 4; tableIdx++ {
					c := chunks[tableIdx]

					// 1. Exact match bucket
					bucket := idx.tables[tableIdx][c]
					for _, candidate := range bucket {
						if candidate > i { // Only compare forward to avoid duplicate pairs
							if visited[candidate] != visitToken {
								visited[candidate] = visitToken
								if thumb.HammingDistance(h, idx.hashes[candidate]) <= threshold {
									localPairs = append(localPairs, [2]int{i, candidate})
								}
							}
						}
					}

					// 2. 1-bit neighbor exploration (if threshold > 3)
					if check1Bit {
						for bit := 0; bit < 16; bit++ {
							neighbor := c ^ (1 << bit)
							nBucket := idx.tables[tableIdx][neighbor]
							for _, candidate := range nBucket {
								if candidate > i {
									if visited[candidate] != visitToken {
										visited[candidate] = visitToken
										if thumb.HammingDistance(h, idx.hashes[candidate]) <= threshold {
											localPairs = append(localPairs, [2]int{i, candidate})
										}
									}
								}
							}
						}
					}
				}
			}

			results[workerID] = localPairs
		}(w, start, end)
	}

	wg.Wait()

	total := 0
	for _, res := range results {
		total += len(res)
	}
	pairs := make([][2]int, 0, total)
	for _, res := range results {
		pairs = append(pairs, res...)
	}

	return pairs
}

// findPairsDirectParallel performs parallel pairwise comparison across all available CPU cores.
func (idx *MIHIndex) findPairsDirectParallel(threshold int) [][2]int {
	n := len(idx.hashes)
	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (n + numWorkers - 1) / numWorkers

	results := make([][][2]int, numWorkers)
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= n {
			break
		}

		wg.Add(1)
		go func(workerID, sIdx, eIdx int) {
			defer wg.Done()
			var localPairs [][2]int

			for i := sIdx; i < eIdx; i++ {
				if idx.valid != nil && !idx.valid[i] {
					continue
				}
				hI := idx.hashes[i]
				for j := i + 1; j < n; j++ {
					if idx.valid != nil && !idx.valid[j] {
						continue
					}
					hJ := idx.hashes[j]
					if thumb.HammingDistance(hI, hJ) <= threshold {
						localPairs = append(localPairs, [2]int{i, j})
					}
				}
			}

			results[workerID] = localPairs
		}(w, start, end)
	}

	wg.Wait()

	total := 0
	for _, res := range results {
		total += len(res)
	}
	pairs := make([][2]int, 0, total)
	for _, res := range results {
		pairs = append(pairs, res...)
	}

	return pairs
}
