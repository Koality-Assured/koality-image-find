package scanner

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Koality-Assured/koality-image-find/pkg/winapi"
	"golang.org/x/sys/windows"
)

// ScannerConfig defines configuration options for the full drive scanner.
type ScannerConfig struct {
	Roots        []string
	MaxWorkers   int
	IncludeCloud bool // Default false to prevent cloud placeholder hydration
	BatchSize    int
	OnProgress   func(stats ScanStats)
}

// Scanner performs multi-threaded directory traversal and media file discovery.
type Scanner struct {
	config ScannerConfig
}

// New creates a new Scanner instance.
func New(config ScannerConfig) *Scanner {
	if config.MaxWorkers <= 0 {
		config.MaxWorkers = runtime.NumCPU() * 2
		if config.MaxWorkers < 4 {
			config.MaxWorkers = 4
		}
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 500
	}
	return &Scanner{config: config}
}

// Scan executes the file scan across all configured roots and delivers batches of MediaFile.
func (s *Scanner) Scan(ctx context.Context, onBatch func([]MediaFile)) (ScanStats, error) {
	startTime := time.Now()
	var (
		foldersScanned atomic.Int64
		imagesFound    atomic.Int64
		videosFound    atomic.Int64
		errorsCount    atomic.Int64
		pendingDirs    atomic.Int64
	)

	// Directory work queue with large buffer
	dirChan := make(chan string, 100000)

	// Batch collector channel
	fileChan := make(chan MediaFile, 5000)

	// Progress ticker
	doneChan := make(chan struct{})
	if s.config.OnProgress != nil {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-doneChan:
					return
				case <-ticker.C:
					s.config.OnProgress(ScanStats{
						FoldersScanned:    foldersScanned.Load(),
						ImagesFound:       imagesFound.Load(),
						VideosFound:       videosFound.Load(),
						ErrorsEncountered: errorsCount.Load(),
						Duration:          time.Since(startTime),
						IsComplete:        false,
					})
				}
			}
		}()
	}

	// Batch aggregation goroutine
	var batchWg sync.WaitGroup
	batchWg.Add(1)
	go func() {
		defer batchWg.Done()
		batch := make([]MediaFile, 0, s.config.BatchSize)
		for file := range fileChan {
			batch = append(batch, file)
			if len(batch) >= s.config.BatchSize {
				onBatch(batch)
				batch = make([]MediaFile, 0, s.config.BatchSize)
			}
		}
		if len(batch) > 0 {
			onBatch(batch)
		}
	}()

	// Worker pool
	var workerWg sync.WaitGroup
	workerWg.Add(s.config.MaxWorkers)

	for i := 0; i < s.config.MaxWorkers; i++ {
		go func() {
			defer workerWg.Done()
			var findData winapi.Win32FindData

			for {
				select {
				case <-ctx.Done():
					return
				case dir, ok := <-dirChan:
					if !ok {
						return
					}

					foldersScanned.Add(1)
					searchPattern := filepath.Join(dir, "*")

					hFind, err := winapi.FindFirstFileEx(searchPattern, &findData)
					if err != nil || hFind == windows.InvalidHandle {
						errorsCount.Add(1)
						if pendingDirs.Add(-1) == 0 {
							close(dirChan)
						}
						continue
					}

					for {
						name := findData.NameString()

						if findData.IsDirectory() {
							if !findData.IsSkipDirectory(name) {
								subDir := filepath.Join(dir, name)
								pendingDirs.Add(1)
								select {
								case dirChan <- subDir:
								case <-ctx.Done():
									winapi.FindClose(hFind)
									return
								}
							}
						} else {
							// Regular file
							ext := filepath.Ext(name)
							mediaType, normExt := ClassifyExtension(ext)
							if mediaType != MediaTypeUnknown {
								isCloud := findData.IsCloudPlaceholder()
								if !isCloud || s.config.IncludeCloud {
									if mediaType == MediaTypeImage {
										imagesFound.Add(1)
									} else if mediaType == MediaTypeVideo {
										videosFound.Add(1)
									}

									select {
									case fileChan <- MediaFile{
										Path:      filepath.Join(dir, name),
										Name:      name,
										Ext:       normExt,
										MediaType: mediaType,
										Size:      findData.FileSize(),
										ModTime:   findData.ModTime(),
										IsCloud:   isCloud,
									}:
									case <-ctx.Done():
										winapi.FindClose(hFind)
										return
									}
								}
							}
						}

						if !winapi.FindNextFile(hFind, &findData) {
							break
						}
					}

					winapi.FindClose(hFind)

					if pendingDirs.Add(-1) == 0 {
						close(dirChan)
					}
				}
			}
		}()
	}

	// Seed roots
	validRoots := 0
	for _, root := range s.config.Roots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		pendingDirs.Add(1)
		validRoots++
		dirChan <- cleanRoot
	}

	if validRoots == 0 {
		close(dirChan)
	}

	// Wait for workers to finish
	workerWg.Wait()
	close(fileChan)
	batchWg.Wait()
	close(doneChan)

	finalStats := ScanStats{
		FoldersScanned:    foldersScanned.Load(),
		ImagesFound:       imagesFound.Load(),
		VideosFound:       videosFound.Load(),
		ErrorsEncountered: errorsCount.Load(),
		Duration:          time.Since(startTime),
		IsComplete:        ctx.Err() == nil,
	}

	if s.config.OnProgress != nil {
		s.config.OnProgress(finalStats)
	}

	return finalStats, ctx.Err()
}
