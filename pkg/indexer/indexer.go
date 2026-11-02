package indexer

import (
	"bytes"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
)

// ItemRecord is a compact 36-byte columnar record representing an indexed file.
type ItemRecord struct {
	ID         uint32
	PathOffset uint32
	PathLen    uint16
	NameOffset uint32
	NameLen    uint16
	ExtOffset  uint32
	ExtLen     uint8
	MediaType  uint8
	Flags      uint8 // bit 0: IsCloud
	Reserved   uint8
	Size       int64
	ModTime    int64
}

const (
	FlagIsCloud uint8 = 1 << 0

	SortByName uint8 = 1
	SortByDate uint8 = 2
	SortBySize uint8 = 3
	SortByType uint8 = 4

	SortAsc  uint8 = 1
	SortDesc uint8 = 2
)

// Query defines search and filter criteria.
type Query struct {
	SearchText string
	MediaType  uint8
	Extension  string
	MinSize    int64
	MaxSize    int64
	MinDate    int64
	MaxDate    int64
}

// SortConfig defines order and sorting field.
type SortConfig struct {
	Field uint8
	Order uint8
}

// ItemView is the expanded human/UI-ready representation of an item.
type ItemView struct {
	ID        uint32 `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Ext       string `json:"ext"`
	MediaType uint8  `json:"mediaType"`
	Size      int64  `json:"size"`
	ModTime   int64  `json:"modTime"`
	IsCloud   bool   `json:"isCloud"`
}

// PageResult represents a virtual cursor window of items.
type PageResult struct {
	TotalCount int        `json:"totalCount"`
	Offset     int        `json:"offset"`
	Limit      int        `json:"limit"`
	Items      []ItemView `json:"items"`
}

// Indexer manages the in-memory columnar database and string pool.
type Indexer struct {
	mu         sync.RWMutex
	records    []ItemRecord
	stringPool []byte
	idLookup   map[uint32]uint32 // ID -> record index
	pathLookup map[string]uint32 // Path -> ID
	nextID     uint32
}

// New creates an empty high-performance indexer.
func New() *Indexer {
	return &Indexer{
		records:    make([]ItemRecord, 0, 10000),
		stringPool: make([]byte, 0, 256*1024),
		idLookup:   make(map[uint32]uint32),
		pathLookup: make(map[string]uint32),
		nextID:     1,
	}
}

// Add appends a batch of MediaFiles to the columnar index.
func (idx *Indexer) Add(files []scanner.MediaFile) int {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	added := 0
	for _, f := range files {
		cleanPath := filepath.Clean(f.Path)
		if _, exists := idx.pathLookup[cleanPath]; exists {
			continue
		}

		id := idx.nextID
		idx.nextID++

		pathBytes := []byte(cleanPath)
		nameBytes := []byte(f.Name)
		extBytes := []byte(f.Ext)

		pathOffset := uint32(len(idx.stringPool))
		idx.stringPool = append(idx.stringPool, pathBytes...)

		nameOffset := uint32(len(idx.stringPool))
		idx.stringPool = append(idx.stringPool, nameBytes...)

		extOffset := uint32(len(idx.stringPool))
		idx.stringPool = append(idx.stringPool, extBytes...)

		var flags uint8
		if f.IsCloud {
			flags |= FlagIsCloud
		}

		recIdx := uint32(len(idx.records))
		rec := ItemRecord{
			ID:         id,
			PathOffset: pathOffset,
			PathLen:    uint16(len(pathBytes)),
			NameOffset: nameOffset,
			NameLen:    uint16(len(nameBytes)),
			ExtOffset:  extOffset,
			ExtLen:     uint8(len(extBytes)),
			MediaType:  f.MediaType,
			Flags:      flags,
			Size:       f.Size,
			ModTime:    f.ModTime,
		}

		idx.records = append(idx.records, rec)
		idx.idLookup[id] = recIdx
		idx.pathLookup[cleanPath] = id
		added++
	}

	return added
}

// Count returns the total number of indexed files.
func (idx *Indexer) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.records)
}

// Clear resets the index.
func (idx *Indexer) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.records = idx.records[:0]
	idx.stringPool = idx.stringPool[:0]
	idx.idLookup = make(map[uint32]uint32)
	idx.pathLookup = make(map[string]uint32)
	idx.nextID = 1
}

// ResolvePath returns the string path for an ItemRecord.
func (idx *Indexer) ResolvePath(r *ItemRecord) string {
	return string(idx.stringPool[r.PathOffset : r.PathOffset+uint32(r.PathLen)])
}

// ResolveName returns the file name for an ItemRecord.
func (idx *Indexer) ResolveName(r *ItemRecord) string {
	return string(idx.stringPool[r.NameOffset : r.NameOffset+uint32(r.NameLen)])
}

// ResolveExt returns the file extension for an ItemRecord.
func (idx *Indexer) ResolveExt(r *ItemRecord) string {
	return string(idx.stringPool[r.ExtOffset : r.ExtOffset+uint32(r.ExtLen)])
}

// GetByID retrieves a single item view by ID.
func (idx *Indexer) GetByID(id uint32) (ItemView, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	recIdx, exists := idx.idLookup[id]
	if !exists || int(recIdx) >= len(idx.records) {
		return ItemView{}, false
	}
	r := &idx.records[recIdx]
	return ItemView{
		ID:        r.ID,
		Path:      idx.ResolvePath(r),
		Name:      idx.ResolveName(r),
		Ext:       idx.ResolveExt(r),
		MediaType: r.MediaType,
		Size:      r.Size,
		ModTime:   r.ModTime,
		IsCloud:   (r.Flags & FlagIsCloud) != 0,
	}, true
}

// GetByPath retrieves a single item view by file path.
func (idx *Indexer) GetByPath(path string) (ItemView, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	cleanPath := filepath.Clean(path)
	id, exists := idx.pathLookup[cleanPath]
	if !exists {
		return ItemView{}, false
	}
	recIdx := idx.idLookup[id]
	r := &idx.records[recIdx]
	return ItemView{
		ID:        r.ID,
		Path:      idx.ResolvePath(r),
		Name:      idx.ResolveName(r),
		Ext:       idx.ResolveExt(r),
		MediaType: r.MediaType,
		Size:      r.Size,
		ModTime:   r.ModTime,
		IsCloud:   (r.Flags & FlagIsCloud) != 0,
	}, true
}

// SearchAndSort executes parallel filtering and multi-column sorting.
func (idx *Indexer) SearchAndSort(q Query, s SortConfig) []uint32 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	n := len(idx.records)
	if n == 0 {
		return nil
	}

	searchLower := strings.ToLower(q.SearchText)
	searchBytes := []byte(searchLower)
	extFilter := strings.ToLower(q.Extension)
	if extFilter != "" && !strings.HasPrefix(extFilter, ".") {
		extFilter = "." + extFilter
	}

	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (n + numWorkers - 1) / numWorkers

	results := make([][]uint32, numWorkers)
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
			var matched []uint32

			for i := sIdx; i < eIdx; i++ {
				r := &idx.records[i]

				// Media type filter
				if q.MediaType != 0 && r.MediaType != q.MediaType {
					continue
				}

				// Size filter
				if q.MinSize > 0 && r.Size < q.MinSize {
					continue
				}
				if q.MaxSize > 0 && r.Size > q.MaxSize {
					continue
				}

				// Date filter
				if q.MinDate > 0 && r.ModTime < q.MinDate {
					continue
				}
				if q.MaxDate > 0 && r.ModTime > q.MaxDate {
					continue
				}

				// Extension filter
				if extFilter != "" {
					ext := string(idx.stringPool[r.ExtOffset : r.ExtOffset+uint32(r.ExtLen)])
					if !strings.EqualFold(ext, extFilter) {
						continue
					}
				}

				// Search substring filter
				if len(searchBytes) > 0 {
					nameBytes := idx.stringPool[r.NameOffset : r.NameOffset+uint32(r.NameLen)]
					if !bytes.Contains(bytes.ToLower(nameBytes), searchBytes) {
						// Also check path
						pathBytes := idx.stringPool[r.PathOffset : r.PathOffset+uint32(r.PathLen)]
						if !bytes.Contains(bytes.ToLower(pathBytes), searchBytes) {
							continue
						}
					}
				}

				matched = append(matched, uint32(i))
			}

			results[workerID] = matched
		}(w, start, end)
	}

	wg.Wait()

	// Merge worker results
	totalMatched := 0
	for _, m := range results {
		totalMatched += len(m)
	}

	matchedIndices := make([]uint32, 0, totalMatched)
	for _, m := range results {
		matchedIndices = append(matchedIndices, m...)
	}

	// Sort matched records
	if s.Field != 0 {
		isDesc := s.Order == SortDesc
		sort.Slice(matchedIndices, func(i, j int) bool {
			a := &idx.records[matchedIndices[i]]
			b := &idx.records[matchedIndices[j]]

			var less bool
			switch s.Field {
			case SortByName:
				nameA := idx.ResolveName(a)
				nameB := idx.ResolveName(b)
				less = strings.ToLower(nameA) < strings.ToLower(nameB)
			case SortByDate:
				less = a.ModTime < b.ModTime
			case SortBySize:
				less = a.Size < b.Size
			case SortByType:
				extA := idx.ResolveExt(a)
				extB := idx.ResolveExt(b)
				less = extA < extB
			default:
				less = a.ID < b.ID
			}

			if isDesc {
				return !less
			}
			return less
		})
	}

	return matchedIndices
}

// GetPage returns a virtual cursor page of items for the viewport.
func (idx *Indexer) GetPage(q Query, s SortConfig, offset int, limit int) PageResult {
	matchedIndices := idx.SearchAndSort(q, s)
	total := len(matchedIndices)

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}

	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	items := make([]ItemView, 0, end-start)
	for i := start; i < end; i++ {
		r := &idx.records[matchedIndices[i]]
		items = append(items, ItemView{
			ID:        r.ID,
			Path:      idx.ResolvePath(r),
			Name:      idx.ResolveName(r),
			Ext:       idx.ResolveExt(r),
			MediaType: r.MediaType,
			Size:      r.Size,
			ModTime:   r.ModTime,
			IsCloud:   (r.Flags & FlagIsCloud) != 0,
		})
	}

	return PageResult{
		TotalCount: total,
		Offset:     offset,
		Limit:      limit,
		Items:      items,
	}
}
