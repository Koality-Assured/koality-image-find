//go:build windows
// +build windows

package thumb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Koality-Assured/koality-image-find/pkg/winapi"
	"golang.org/x/sys/windows"
)

var (
	modgdi32  = windows.NewLazySystemDLL("gdi32.dll")
	moduser32 = windows.NewLazySystemDLL("user32.dll")

	procGetObjectW          = modgdi32.NewProc("GetObjectW")
	procGetDIBits           = modgdi32.NewProc("GetDIBits")
	procDeleteObject        = modgdi32.NewProc("DeleteObject")
	procCreateCompatibleDC  = modgdi32.NewProc("CreateCompatibleDC")
	procDeleteDC            = modgdi32.NewProc("DeleteDC")

	procGetDC          = moduser32.NewProc("GetDC")
	procReleaseDC      = moduser32.NewProc("ReleaseDC")
	procPeekMessageW   = moduser32.NewProc("PeekMessageW")
	procTranslateMsg   = moduser32.NewProc("TranslateMessage")
	procDispatchMsg    = moduser32.NewProc("DispatchMessageW")

	iidIShellItemImageFactory = windows.GUID{
		Data1: 0xbcc18b79,
		Data2: 0xba16,
		Data3: 0x442f,
		Data4: [8]byte{0x80, 0xc4, 0x7a, 0x14, 0x0e, 0xf8, 0x2c, 0x14},
	}
)

const (
	SIIGBF_RESIZETOFIT   = 0x00
	SIIGBF_BIGGERSIZEOK  = 0x01
	SIIGBF_MEMORYONLY    = 0x02
	SIIGBF_ICONONLY      = 0x04
	SIIGBF_THUMBNAILONLY = 0x08
	SIIGBF_INCACHEONLY   = 0x10

	DIB_RGB_COLORS = 0
	BI_RGB         = 0
	PM_REMOVE      = 0x0001
)

type SIZE struct {
	cx int32
	cy int32
}

type BITMAP struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

type BITMAPINFOHEADER struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [1]uint32
}

type MSG struct {
	Hwnd     uintptr
	Message  uint32
	Wparam   uintptr
	Lparam   uintptr
	Time     uint32
	Pt       struct{ X, Y int32 }
	LPrivate uint32
}

type shellRequest struct {
	filePath string
	size     int
	resp     chan shellResponse
}

type shellResponse struct {
	data []byte
	err  error
}

// ShellWorkerPool manages STA thread-pinned workers with a Win32 message pump.
type ShellWorkerPool struct {
	reqChan chan shellRequest
	stop    chan struct{}
	wg      sync.WaitGroup
}

var (
	defaultPool *ShellWorkerPool
	poolOnce    sync.Once
)

// GetShellPool returns the global STA shell worker pool.
func GetShellPool() *ShellWorkerPool {
	poolOnce.Do(func() {
		defaultPool = NewShellWorkerPool(2) // 2 STA workers to avoid shell lock contention
	})
	return defaultPool
}

// NewShellWorkerPool initializes N thread-pinned STA worker goroutines.
func NewShellWorkerPool(numWorkers int) *ShellWorkerPool {
	if numWorkers <= 0 {
		numWorkers = 2
	}
	p := &ShellWorkerPool{
		reqChan: make(chan shellRequest, 100),
		stop:    make(chan struct{}),
	}

	p.wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go p.workerLoop()
	}
	return p
}

// Close terminates the worker pool cleanly.
func (p *ShellWorkerPool) Close() {
	close(p.stop)
	p.wg.Wait()
}

func (p *ShellWorkerPool) workerLoop() {
	defer p.wg.Done()

	// 1. Pin worker to OS thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 2. Initialize COM Single-Threaded Apartment (STA)
	if err := winapi.CoInitialize(); err != nil {
		return
	}
	defer winapi.CoUninitialize()

	for {
		// Pump any pending Win32 RPC messages before/between tasks
		pumpMessages()

		select {
		case <-p.stop:
			return
		case req, ok := <-p.reqChan:
			if !ok {
				return
			}
			data, err := extractViaShellCOM(req.filePath, req.size)
			req.resp <- shellResponse{data: data, err: err}
			pumpMessages()
		}
	}
}

// ExtractThumbnail requests a thumbnail from the STA worker pool.
func (p *ShellWorkerPool) ExtractThumbnail(filePath string, size int, timeout time.Duration) ([]byte, error) {
	respChan := make(chan shellResponse, 1)
	req := shellRequest{
		filePath: filePath,
		size:     size,
		resp:     respChan,
	}

	select {
	case p.reqChan <- req:
	case <-time.After(timeout):
		return nil, errors.New("shell worker pool request timed out")
	}

	select {
	case res := <-respChan:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, errors.New("shell worker execution timed out")
	}
}

// pumpMessages drains the Win32 message queue for this STA thread.
func pumpMessages() {
	var msg MSG
	for {
		r0, _, _ := procPeekMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
			PM_REMOVE,
		)
		if r0 == 0 {
			break
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// extractViaShellCOM extracts thumbnail using IShellItemImageFactory.
func extractViaShellCOM(filePath string, size int) ([]byte, error) {
	var pItem uintptr
	err := winapi.SHCreateItemFromParsingName(filePath, &iidIShellItemImageFactory, &pItem)
	if err != nil || pItem == 0 {
		return nil, errors.New("SHCreateItemFromParsingName failed: " + err.Error())
	}

	// IShellItemImageFactory VTable:
	// 0: QueryInterface
	// 1: AddRef
	// 2: Release
	// 3: GetImage(SIZE size, SIIGBF flags, HBITMAP *phbm)
	vtable := *(**uintptr)(unsafe.Pointer(pItem))
	procRelease := *(*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(vtable)) + 2*unsafe.Sizeof(uintptr(0))))
	procGetImage := *(*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(vtable)) + 3*unsafe.Sizeof(uintptr(0))))

	defer syscall.SyscallN(procRelease, pItem)

	var hBitmap uintptr
	dim := int32(size)
	if dim <= 0 {
		dim = 256
	}

	// Pack SIZE into a single uintptr (cx in low 32 bits, cy in high 32 bits on 64-bit Windows)
	sizeArg := uintptr(uint32(dim)) | (uintptr(uint32(dim)) << 32)

	rGetImage, _, _ := syscall.SyscallN(
		procGetImage,
		pItem,
		sizeArg,
		uintptr(SIIGBF_THUMBNAILONLY|SIIGBF_BIGGERSIZEOK),
		uintptr(unsafe.Pointer(&hBitmap)),
	)

	if int32(rGetImage) < 0 || hBitmap == 0 {
		return nil, errors.New("IShellItemImageFactory::GetImage failed")
	}
	defer procDeleteObject.Call(hBitmap)

	// Convert HBITMAP to JPEG
	return hBitmapToJPEG(hBitmap)
}

// hBitmapToJPEG converts an HBITMAP to JPEG bytes.
func hBitmapToJPEG(hBitmap uintptr) ([]byte, error) {
	var bm BITMAP
	r0, _, _ := procGetObjectW.Call(
		hBitmap,
		uintptr(unsafe.Sizeof(bm)),
		uintptr(unsafe.Pointer(&bm)),
	)
	if r0 == 0 || bm.Width <= 0 || bm.Height <= 0 {
		return nil, errors.New("GetObjectW failed on HBITMAP")
	}

	hDC, _, _ := procGetDC.Call(0)
	if hDC == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer procReleaseDC.Call(0, hDC)

	memDC, _, _ := procCreateCompatibleDC.Call(hDC)
	if memDC == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	var bi BITMAPINFO
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = bm.Width
	bi.Header.Height = -bm.Height // Top-down DIB
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = BI_RGB

	w := int(bm.Width)
	h := int(bm.Height)
	pixels := make([]byte, w*h*4)

	rDIB, _, _ := procGetDIBits.Call(
		memDC,
		hBitmap,
		0,
		uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bi)),
		DIB_RGB_COLORS,
	)
	if rDIB == 0 {
		return nil, errors.New("GetDIBits failed")
	}

	// Convert BGRA to RGBA image
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		rowOffset := y * w * 4
		for x := 0; x < w; x++ {
			pixelOffset := rowOffset + x*4
			b := pixels[pixelOffset]
			g := pixels[pixelOffset+1]
			r := pixels[pixelOffset+2]
			a := pixels[pixelOffset+3]
			if a == 0 {
				a = 255 // Default opaque if alpha channel is unset
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
