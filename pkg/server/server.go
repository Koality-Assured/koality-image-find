package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Koality-Assured/koality-image-find/pkg/cache"
	"github.com/Koality-Assured/koality-image-find/pkg/dedup"
	"github.com/Koality-Assured/koality-image-find/pkg/indexer"
	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
	"github.com/Koality-Assured/koality-image-find/pkg/thumb"
	"github.com/Koality-Assured/koality-image-find/pkg/winapi"
)

// ServerConfig configures the local loopback HTTP server.
type ServerConfig struct {
	Indexer       *indexer.Indexer
	Scanner       *scanner.Scanner
	Pipeline      *thumb.Pipeline
	StaticFS      fs.FS
	SessionToken  string
	OnStartScan   func(roots []string)
	CurrentStats  func() scanner.ScanStats
}

// Server handles authenticated loopback HTTP requests.
type Server struct {
	config     ServerConfig
	listener   net.Listener
	httpServer *http.Server
	token      string
	mu         sync.RWMutex

	// Background deduplication hashing progress
	dedupMu        sync.Mutex
	dedupRunning   bool
	dedupTotal     int64
	dedupProcessed atomic.Int64
	dedupCancel    context.CancelFunc
	dedupWg        sync.WaitGroup
}

// GenerateSessionToken creates a cryptographically secure 256-bit token.
func GenerateSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// New creates an authenticated HTTP server instance.
func New(config ServerConfig) *Server {
	if config.SessionToken == "" {
		config.SessionToken = GenerateSessionToken()
	}
	s := &Server{
		config: config,
		token:  config.SessionToken,
	}
	return s
}

// Token returns the session token.
func (s *Server) Token() string {
	return s.token
}

// Start binds to an available port on 127.0.0.1 and begins serving requests.
func (s *Server) Start() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.listener = listener

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/status", s.authMiddleware(s.handleStatus))
	mux.HandleFunc("/api/scan", s.authMiddleware(s.handleScan))
	mux.HandleFunc("/api/items", s.authMiddleware(s.handleItems))
	mux.HandleFunc("/api/thumbnail", s.authMiddleware(s.handleThumbnail))
	mux.HandleFunc("/api/media", s.authMiddleware(s.handleMedia))
	mux.HandleFunc("/api/action/reveal", s.authMiddleware(s.handleActionReveal))
	mux.HandleFunc("/api/action/open", s.authMiddleware(s.handleActionOpen))
	mux.HandleFunc("/api/duplicates", s.authMiddleware(s.handleDuplicates))
	mux.HandleFunc("/api/dedup/compute", s.authMiddleware(s.handleDedupCompute))
	mux.HandleFunc("/api/dedup/status", s.authMiddleware(s.handleDedupStatus))

	// Embedded Static Frontend
	if s.config.StaticFS != nil {
		fileServer := http.FileServer(http.FS(s.config.StaticFS))
		mux.Handle("/", fileServer)
	}

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		_ = s.httpServer.Serve(listener)
	}()

	addr := listener.Addr().String()
	return "http://" + addr, nil
}

// Close stops the HTTP server and cancels active background tasks.
func (s *Server) Close() error {
	s.dedupMu.Lock()
	if s.dedupCancel != nil {
		s.dedupCancel()
	}
	s.dedupMu.Unlock()
	s.dedupWg.Wait()

	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

// authMiddleware enforces the 256-bit session token and checks loopback origins.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Verify session token from Header or Query Param
		reqToken := r.Header.Get("X-Session-Token")
		if reqToken == "" {
			reqToken = r.URL.Query().Get("token")
		}

		if reqToken == "" || reqToken != s.token {
			http.Error(w, `{"error":"unauthorized: invalid session token"}`, http.StatusUnauthorized)
			return
		}

		// Security: Validate Host header is 127.0.0.1 or localhost
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" {
			http.Error(w, `{"error":"forbidden: host not allowed"}`, http.StatusForbidden)
			return
		}

		// Origin header check (if present)
		origin := r.Header.Get("Origin")
		if origin != "" {
			u, err := url.Parse(origin)
			if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
				http.Error(w, `{"error":"forbidden: origin not allowed"}`, http.StatusForbidden)
				return
			}
		}

		next(w, r)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var stats scanner.ScanStats
	if s.config.CurrentStats != nil {
		stats = s.config.CurrentStats()
	}
	totalIndexed := 0
	if s.config.Indexer != nil {
		totalIndexed = s.config.Indexer.Count()
	}

	resp := map[string]interface{}{
		"totalIndexed":      totalIndexed,
		"foldersScanned":    stats.FoldersScanned,
		"imagesFound":       stats.ImagesFound,
		"videosFound":       stats.VideosFound,
		"errorsEncountered": stats.ErrorsEncountered,
		"scanDurationMs":    stats.Duration.Milliseconds(),
		"scanComplete":      stats.IsComplete,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Roots []string `json:"roots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if s.config.OnStartScan != nil {
		s.config.OnStartScan(req.Roots)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "scan initiated"})
}

func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	searchText := q.Get("q")
	mediaTypeStr := q.Get("type")
	ext := q.Get("ext")
	presetStr := q.Get("preset")
	personalStr := q.Get("personal")
	sortFieldStr := q.Get("sort")
	sortOrderStr := q.Get("order")
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))

	var mediaType uint8
	if mediaTypeStr == "image" {
		mediaType = scanner.MediaTypeImage
	} else if mediaTypeStr == "video" {
		mediaType = scanner.MediaTypeVideo
	}

	personalOnly := presetStr == "personal" || personalStr == "1" || personalStr == "true"

	var sortField uint8
	switch sortFieldStr {
	case "name":
		sortField = indexer.SortByName
	case "date":
		sortField = indexer.SortByDate
	case "size":
		sortField = indexer.SortBySize
	case "type":
		sortField = indexer.SortByType
	default:
		sortField = indexer.SortByDate
	}

	var sortOrder uint8
	if sortOrderStr == "asc" {
		sortOrder = indexer.SortAsc
	} else {
		sortOrder = indexer.SortDesc
	}

	query := indexer.Query{
		SearchText:   searchText,
		MediaType:    mediaType,
		Extension:    ext,
		PersonalOnly: personalOnly,
	}
	sortConfig := indexer.SortConfig{
		Field: sortField,
		Order: sortOrder,
	}

	page := s.config.Indexer.GetPage(query, sortConfig, offset, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id64 == 0 {
		http.Error(w, "invalid id parameter", http.StatusBadRequest)
		return
	}

	id := uint32(id64)
	item, found := s.config.Indexer.GetByID(id)
	if !found {
		http.Error(w, "item not found in index", http.StatusNotFound)
		return
	}

	// Security: Prevent full media streaming if cloud placeholder to prevent accidental downloads
	if item.IsCloud {
		http.Error(w, "cloud placeholder media cannot be streamed without hydration", http.StatusGone)
		return
	}

	sizeStr := r.URL.Query().Get("size")
	targetSize, _ := strconv.Atoi(sizeStr)
	if targetSize <= 0 {
		targetSize = 256
	}

	// Security: Prevent thumbnail generation if cloud placeholder
	if item.IsCloud {
		http.Error(w, "cloud placeholder thumbnail not available", http.StatusGone)
		return
	}

	thumbData, mimeType, err := s.config.Pipeline.GetThumbnail(item.Path, item.MediaType, item.Size, item.ModTime, targetSize)
	if err != nil || len(thumbData) == 0 {
		http.Error(w, "thumbnail generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(thumbData)))
	_, _ = w.Write(thumbData)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id64 == 0 {
		http.Error(w, "invalid id parameter", http.StatusBadRequest)
		return
	}

	id := uint32(id64)
	item, found := s.config.Indexer.GetByID(id)
	if !found {
		http.Error(w, "item not found in index", http.StatusNotFound)
		return
	}

	// Security: Prevent full media streaming if cloud placeholder to prevent accidental downloads
	if item.IsCloud {
		http.Error(w, "cloud placeholder media cannot be streamed without hydration", http.StatusGone)
		return
	}

	// Security: Whitelist path check against disk
	cleanPath := filepath.Clean(item.Path)
	file, err := os.Open(winapi.ToExtendedPath(cleanPath))
	if err != nil {
		http.Error(w, "file unreadable", http.StatusNotFound)
		return
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	// http.ServeContent natively handles HTTP Byte Ranges (crucial for video scrubbing!)
	http.ServeContent(w, r, filepath.Base(cleanPath), fi.ModTime(), file)
}

func (s *Server) handleActionReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID uint32 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	item, found := s.config.Indexer.GetByID(req.ID)
	if !found {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	err := winapi.OpenFolderAndSelectItem(item.Path)
	if err != nil {
		http.Error(w, "reveal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (s *Server) handleActionOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID uint32 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	item, found := s.config.Indexer.GetByID(req.ID)
	if !found {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	// Security: Whitelist allowed media types only before executing
	mediaType, _ := scanner.ClassifyExtension(item.Ext)
	if mediaType != scanner.MediaTypeImage && mediaType != scanner.MediaTypeVideo {
		http.Error(w, "prohibited file type", http.StatusForbidden)
		return
	}

	err := winapi.ShellExecute(0, "open", item.Path, "", "", 1)
	if err != nil {
		http.Error(w, "open failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DuplicatesResponse represents the cluster analysis and statistics payload.
type DuplicatesResponse struct {
	TotalItems           int             `json:"totalItems"`
	HashedItems          int             `json:"hashedItems"`
	Computing            bool            `json:"computing"`
	TotalClusters        int             `json:"totalClusters"`
	TotalDuplicates      int             `json:"totalDuplicates"`
	TotalReclaimableSize int64           `json:"totalReclaimableSize"`
	Offset               int             `json:"offset"`
	Limit                int             `json:"limit"`
	Clusters             []dedup.Cluster `json:"clusters"`
}

func (s *Server) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	searchText := q.Get("q")
	mediaTypeStr := q.Get("type")
	ext := q.Get("ext")
	presetStr := q.Get("preset")
	personalStr := q.Get("personal")
	thresholdStr := q.Get("threshold")
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	computeStr := q.Get("compute")

	threshold := 4 // Default: near-duplicate
	if thresholdStr != "" {
		if t, err := strconv.Atoi(thresholdStr); err == nil && t >= 0 && t <= 64 {
			threshold = t
		}
	}

	var mediaType uint8
	if mediaTypeStr == "image" {
		mediaType = scanner.MediaTypeImage
	} else if mediaTypeStr == "video" {
		mediaType = scanner.MediaTypeVideo
	}

	personalOnly := presetStr == "personal" || personalStr == "1" || personalStr == "true"

	query := indexer.Query{
		SearchText:   searchText,
		MediaType:    mediaType,
		Extension:    ext,
		PersonalOnly: personalOnly,
	}

	// 1. Fetch matching items (bounded to max 25,000 for sub-second clustering)
	matchedItems := s.config.Indexer.GetMatchingItems(query, indexer.SortConfig{}, 25000)
	totalMatched := len(matchedItems)
	if totalMatched == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DuplicatesResponse{Limit: limit})
		return
	}

	hashes := make([]uint64, totalMatched)
	hasHash := make([]bool, totalMatched)
	var unhashedIndices []int
	hashedCount := 0

	cacheInstance := s.config.Pipeline.Cache()

	for i, it := range matchedItems {
		if cacheInstance != nil {
			key := cache.GenerateKey(it.Path, it.Size, it.ModTime)
			if h, found := cacheInstance.GetHash(key); found {
				hashes[i] = h
				hasHash[i] = true
				hashedCount++
				continue
			}
		}
		unhashedIndices = append(unhashedIndices, i)
	}

	// 2. Compute missing hashes on-demand if compute=1 or small candidate pool (<= 120 items)
	shouldCompute := computeStr == "1" || computeStr == "true" || (len(unhashedIndices) > 0 && len(unhashedIndices) <= 120)
	if shouldCompute && len(unhashedIndices) > 0 {
		// If set is small (<= 250 items), compute synchronously
		if len(unhashedIndices) <= 250 {
			numWorkers := runtime.NumCPU()
			if numWorkers > 8 {
				numWorkers = 8
			}
			workCh := make(chan int, len(unhashedIndices))
			for _, idx := range unhashedIndices {
				workCh <- idx
			}
			close(workCh)

			reqCtx := r.Context()
			var wg sync.WaitGroup
			for wIdx := 0; wIdx < numWorkers; wIdx++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range workCh {
						select {
						case <-reqCtx.Done():
							return
						default:
						}
						it := matchedItems[i]
						if it.IsCloud {
							continue
						}
						h, err := s.config.Pipeline.ExtractHash(it.Path, it.MediaType, it.Size, it.ModTime)
						if err == nil {
							hashes[i] = h
							hasHash[i] = true
						}
					}
				}()
			}
			wg.Wait()

			// Recalculate hashedCount
			hashedCount = 0
			for _, valid := range hasHash {
				if valid {
					hashedCount++
				}
			}
		} else {
			// Trigger background computation
			s.startBackgroundHashing(matchedItems, unhashedIndices)
		}
	}

	// 3. Cluster items using MIH and DSU
	clusters := dedup.ClusterItems(matchedItems, hashes, hasHash, threshold)
	paginated := dedup.PaginateClusters(clusters, offset, limit)

	s.dedupMu.Lock()
	isComputing := s.dedupRunning
	s.dedupMu.Unlock()

	resp := DuplicatesResponse{
		TotalItems:           totalMatched,
		HashedItems:          hashedCount,
		Computing:            isComputing,
		TotalClusters:        paginated.TotalClusters,
		TotalDuplicates:      paginated.TotalDuplicates,
		TotalReclaimableSize: paginated.TotalReclaimableSize,
		Offset:               paginated.Offset,
		Limit:                paginated.Limit,
		Clusters:             paginated.Clusters,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) startBackgroundHashing(items []indexer.ItemView, unhashed []int) {
	s.dedupMu.Lock()
	if s.dedupRunning {
		s.dedupMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.dedupCancel = cancel
	s.dedupRunning = true
	s.dedupTotal = int64(len(unhashed))
	s.dedupProcessed.Store(0)
	s.dedupWg.Add(1)
	s.dedupMu.Unlock()

	go func() {
		defer func() {
			s.dedupMu.Lock()
			s.dedupRunning = false
			s.dedupCancel = nil
			s.dedupMu.Unlock()
			s.dedupWg.Done()
		}()

		numWorkers := runtime.NumCPU()
		if numWorkers > 8 {
			numWorkers = 8
		}

		workCh := make(chan int, len(unhashed))
		for _, idx := range unhashed {
			workCh <- idx
		}
		close(workCh)

		var workerWg sync.WaitGroup
		for wIdx := 0; wIdx < numWorkers; wIdx++ {
			workerWg.Add(1)
			go func() {
				defer workerWg.Done()
				for i := range workCh {
					select {
					case <-ctx.Done():
						return
					default:
					}

					it := items[i]
					if !it.IsCloud {
						_, _ = s.config.Pipeline.ExtractHash(it.Path, it.MediaType, it.Size, it.ModTime)
					}
					s.dedupProcessed.Add(1)
				}
			}()
		}
		workerWg.Wait()
	}()
}

func (s *Server) handleDedupCompute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Cancel bool `json:"cancel"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Cancel {
		s.dedupMu.Lock()
		if s.dedupCancel != nil {
			s.dedupCancel()
		}
		s.dedupMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancellation initiated"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleDedupStatus(w http.ResponseWriter, r *http.Request) {
	s.dedupMu.Lock()
	running := s.dedupRunning
	total := s.dedupTotal
	s.dedupMu.Unlock()

	resp := map[string]interface{}{
		"running":   running,
		"total":     total,
		"processed": s.dedupProcessed.Load(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
