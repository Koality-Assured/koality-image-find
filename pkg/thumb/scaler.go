package thumb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"

	"github.com/Koality-Assured/koality-image-find/pkg/winapi"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var (
	errDecompressionBomb = errors.New("image dimensions exceed safe limits (possible decompression bomb)")
	errUnsupportedFormat = errors.New("unsupported image format")
)

const (
	MaxSafeDimension = 8192
	MaxSafePixels    = 64 * 1024 * 1024 // 64 Megapixels
)

// DecodeAndScaleImage decodes an image with decompression-bomb protection and downscales it to fit
// within targetW x targetH while preserving aspect ratio.
func DecodeAndScaleImage(filePath string, targetW, targetH int) ([]byte, error) {
	f, err := os.Open(winapi.ToExtendedPath(filePath))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 1. Check dimensions first without allocating full pixel buffer
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}

	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errUnsupportedFormat
	}

	if cfg.Width > MaxSafeDimension || cfg.Height > MaxSafeDimension ||
		int64(cfg.Width)*int64(cfg.Height) > MaxSafePixels {
		return nil, errDecompressionBomb
	}

	// Rewind file pointer
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// 2. Full decode
	srcImg, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}

	// 3. Compute target dimensions
	origW := srcImg.Bounds().Dx()
	origH := srcImg.Bounds().Dy()

	if origW <= 0 || origH <= 0 {
		return nil, errUnsupportedFormat
	}

	scale := math.Min(float64(targetW)/float64(origW), float64(targetH)/float64(origH))
	if scale > 1.0 {
		scale = 1.0 // Don't upscale small images
	}

	newW := int(float64(origW) * scale)
	newH := int(float64(origH) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	// 4. Downscale using bilinear resampling
	dstImg := scaleBilinear(srcImg, newW, newH)

	// 5. Encode as JPEG Q=80
	var out bytes.Buffer
	err = jpeg.Encode(&out, dstImg, &jpeg.Options{Quality: 80})
	if err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// scaleBilinear performs fast bilinear interpolation from src to a new RGBA image.
func scaleBilinear(src image.Image, dstW, dstH int) *image.RGBA {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	xRatio := float64(srcW-1) / float64(dstW)
	yRatio := float64(srcH-1) / float64(dstH)

	for y := 0; y < dstH; y++ {
		srcY := float64(y) * yRatio
		yFloor := int(srcY)
		yDiff := srcY - float64(yFloor)
		yCeil := yFloor + 1
		if yCeil >= srcH {
			yCeil = yFloor
		}

		for x := 0; x < dstW; x++ {
			srcX := float64(x) * xRatio
			xFloor := int(srcX)
			xDiff := srcX - float64(xFloor)
			xCeil := xFloor + 1
			if xCeil >= srcW {
				xCeil = xFloor
			}

			c00 := colorToRGBA(src.At(bounds.Min.X+xFloor, bounds.Min.Y+yFloor))
			c10 := colorToRGBA(src.At(bounds.Min.X+xCeil, bounds.Min.Y+yFloor))
			c01 := colorToRGBA(src.At(bounds.Min.X+xFloor, bounds.Min.Y+yCeil))
			c11 := colorToRGBA(src.At(bounds.Min.X+xCeil, bounds.Min.Y+yCeil))

			r := bilerp(c00.R, c10.R, c01.R, c11.R, xDiff, yDiff)
			g := bilerp(c00.G, c10.G, c01.G, c11.G, xDiff, yDiff)
			b := bilerp(c00.B, c10.B, c01.B, c11.B, xDiff, yDiff)
			a := bilerp(c00.A, c10.A, c01.A, c11.A, xDiff, yDiff)

			dst.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	return dst
}

func colorToRGBA(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}

func bilerp(c00, c10, c01, c11 uint8, xDiff, yDiff float64) uint8 {
	f00 := float64(c00)
	f10 := float64(c10)
	f01 := float64(c01)
	f11 := float64(c11)

	top := f00*(1.0-xDiff) + f10*xDiff
	bottom := f01*(1.0-xDiff) + f11*xDiff
	val := top*(1.0-yDiff) + bottom*yDiff

	if val < 0 {
		return 0
	}
	if val > 255 {
		return 255
	}
	return uint8(val)
}
