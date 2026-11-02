package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/Koality-Assured/koality-image-find/frontend"
	"github.com/Koality-Assured/koality-image-find/pkg/cache"
	"github.com/Koality-Assured/koality-image-find/pkg/indexer"
	"github.com/Koality-Assured/koality-image-find/pkg/scanner"
	"github.com/Koality-Assured/koality-image-find/pkg/server"
	"github.com/Koality-Assured/koality-image-find/pkg/thumb"
	"github.com/Koality-Assured/koality-image-find/pkg/winapi"
)

var (
	currentScanStats scanner.ScanStats
	statsMu          sync.RWMutex
	scanMu           sync.Mutex
	currentCancel    context.CancelFunc
)

func main() {
	var (
		rootsFlag     string
		noBrowserFlag bool
		workersFlag   int
		cacheDirFlag  string
	)

	flag.StringVar(&rootsFlag, "roots", "", "Comma-separated roots to scan (e.g. C:\\Users,D:\\Photos)")
	flag.BoolVar(&noBrowserFlag, "no-browser", false, "Do not auto-open desktop window")
	flag.IntVar(&workersFlag, "workers", runtime.NumCPU()*2, "Parallel directory worker threads")
	flag.StringVar(&cacheDirFlag, "cache-dir", "", "Thumbnail cache storage directory")
	flag.Parse()

	log.Println("==================================================")
	log.Println("  koality-image-find: Ultra-Fast Media Viewer")
	log.Println("==================================================")

	// Determine cache directory
	if cacheDirFlag == "" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		cacheDirFlag = filepath.Join(localAppData, "koality-image-find", "cache")
	}

	log.Printf("[Init] Cache directory: %s\n", cacheDirFlag)
	thumbCache, err := cache.Open(cacheDirFlag, 500)
	if err != nil {
		log.Fatalf("[Error] Failed to initialize thumbnail cache: %v\n", err)
	}
	defer thumbCache.Close()

	// Initialize Indexer & Pipeline
	idx := indexer.New()
	pipeline := thumb.NewPipeline(thumbCache)

	// Prepare roots
	var roots []string
	if rootsFlag != "" {
		for _, r := range strings.Split(rootsFlag, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, r)
			}
		}
	} else {
		// Default: scan UserProfile directories (Pictures, Videos, Downloads, Desktop, Documents)
		userProfile := os.Getenv("USERPROFILE")
		if userProfile != "" {
			candidateDirs := []string{
				filepath.Join(userProfile, "Pictures"),
				filepath.Join(userProfile, "Videos"),
				filepath.Join(userProfile, "Downloads"),
				filepath.Join(userProfile, "Desktop"),
				filepath.Join(userProfile, "Documents"),
			}
			for _, d := range candidateDirs {
				if _, err := os.Stat(d); err == nil {
					roots = append(roots, d)
				}
			}
			// If nothing found or want broader scope, fall back to user profile root
			if len(roots) == 0 {
				roots = append(roots, userProfile)
			}
		} else {
			roots = []string{`C:\`}
		}
	}

	log.Printf("[Config] Scan roots: %v\n", roots)

	// Static filesystem from frontend package
	staticFS := frontend.DistFS

	startScan := func(targetRoots []string) {
		scanMu.Lock()
		if currentCancel != nil {
			currentCancel()
		}
		scanCtx, cancel := context.WithCancel(context.Background())
		currentCancel = cancel
		scanMu.Unlock()

		go func() {
			log.Printf("[Scanner] Starting traversal across %d roots...\n", len(targetRoots))
			s := scanner.New(scanner.ScannerConfig{
				Roots:        targetRoots,
				MaxWorkers:   workersFlag,
				IncludeCloud: false, // Strict: Prevent OneDrive hydration
				BatchSize:    500,
				OnProgress: func(stats scanner.ScanStats) {
					statsMu.Lock()
					currentScanStats = stats
					statsMu.Unlock()
				},
			})

			finalStats, scanErr := s.Scan(scanCtx, func(batch []scanner.MediaFile) {
				idx.Add(batch)
			})

			statsMu.Lock()
			currentScanStats = finalStats
			currentScanStats.IsComplete = true
			statsMu.Unlock()

			if scanErr != nil && scanErr != context.Canceled {
				log.Printf("[Scanner] Completed with error: %v\n", scanErr)
			} else {
				log.Printf("[Scanner] Finished in %v: found %d images, %d videos in %d folders.\n",
					finalStats.Duration, finalStats.ImagesFound, finalStats.VideosFound, finalStats.FoldersScanned)
			}
		}()
	}

	// Initialize Loopback Server
	srv := server.New(server.ServerConfig{
		Indexer:      idx,
		Pipeline:     pipeline,
		StaticFS:     staticFS,
		OnStartScan:  startScan,
		CurrentStats: func() scanner.ScanStats {
			statsMu.RLock()
			defer statsMu.RUnlock()
			return currentScanStats
		},
	})

	serverURL, err := srv.Start()
	if err != nil {
		log.Fatalf("[Error] Failed to start HTTP server: %v\n", err)
	}
	defer srv.Close()

	appURL := fmt.Sprintf("%s/?token=%s", serverURL, srv.Token())
	log.Printf("[Server] Authenticated loopback listening at: %s\n", serverURL)
	log.Printf("[Security] Session token initialized (256-bit protected)\n")

	// Kick off initial file scan
	startScan(roots)

	// Launch desktop window
	if !noBrowserFlag {
		launchDesktopWindow(appURL)
	}

	// Wait for OS shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("[Shutdown] Gracefully closing resources...")
	scanMu.Lock()
	if currentCancel != nil {
		currentCancel()
	}
	scanMu.Unlock()
}

// launchDesktopWindow launches Microsoft Edge in --app mode or falls back to system browser.
func launchDesktopWindow(appURL string) {
	edgePaths := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	}

	for _, p := range edgePaths {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p, "--app="+appURL, "--window-size=1440,920")
			if err := cmd.Start(); err == nil {
				log.Printf("[UI] Launched native Edge desktop app window\n")
				return
			}
		}
	}

	// Fallback to ShellExecute default browser
	log.Printf("[UI] Opening default browser at: %s\n", appURL)
	_ = winapi.ShellExecute(0, "open", appURL, "", "", 1)
}
