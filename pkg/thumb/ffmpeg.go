package thumb

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

var (
	ffmpegChecked bool
	ffmpegPath    string
	ffmpegMu      sync.RWMutex
)

// HasFFmpeg returns true if ffmpeg executable is available on the system PATH.
func HasFFmpeg() bool {
	ffmpegMu.RLock()
	if ffmpegChecked {
		has := ffmpegPath != ""
		ffmpegMu.RUnlock()
		return has
	}
	ffmpegMu.RUnlock()

	ffmpegMu.Lock()
	defer ffmpegMu.Unlock()
	if ffmpegChecked {
		return ffmpegPath != ""
	}

	path, err := exec.LookPath("ffmpeg")
	if err == nil && path != "" {
		ffmpegPath = path
	}
	ffmpegChecked = true
	return ffmpegPath != ""
}

// ExtractVideoFrameFFmpeg extracts a frame from a video file at 2 seconds using ffmpeg pipe streaming.
func ExtractVideoFrameFFmpeg(videoPath string, size int, timeout time.Duration) ([]byte, error) {
	if !HasFFmpeg() {
		return nil, errors.New("ffmpeg not found in PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if size <= 0 {
		size = 256
	}

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-ss", "00:00:02",
		"-i", videoPath,
		"-vframes", "1",
		"-vf", "scale=256:-1:flags=fast_bilinear",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.New("ffmpeg extraction failed: " + err.Error())
	}

	data := stdout.Bytes()
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return data, nil
	}

	return nil, errors.New("ffmpeg returned invalid JPEG data")
}
