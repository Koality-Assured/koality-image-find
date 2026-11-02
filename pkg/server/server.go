package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

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

// Close stops the HTTP server.
func (s *Server) Close() error {
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
		SearchText: searchText,
		MediaType:  mediaType,
		Extension:  ext,
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
