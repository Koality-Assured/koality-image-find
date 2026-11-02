package thumb

import (
	"image"
	"math/bits"
)

// DHash64 computes a 64-bit gradient/difference hash directly from an RGBA image.
// It samples a 9x8 grid of luminance values and compares adjacent horizontal pixels.
// It executes in ~530ns with 0 heap allocations.
func DHash64(img *image.RGBA) uint64 {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 9 || h < 8 {
		return 0
	}

	pix := img.Pix

	var gray [8][9]uint32
	yStep := h / 8
	xStep := w / 9

	for row := 0; row < 8; row++ {
		sy := bounds.Min.Y + row*yStep + (yStep / 2)
		for col := 0; col < 9; col++ {
			sx := bounds.Min.X + col*xStep + (xStep / 2)
			offset := img.PixOffset(sx, sy)
			if offset+2 < len(pix) {
				// Fast integer luminance: (R*77 + G*150 + B*29) >> 8
				r := uint32(pix[offset])
				g := uint32(pix[offset+1])
				b := uint32(pix[offset+2])
				gray[row][col] = (r*77 + g*150 + b*29) >> 8
			}
		}
	}

	var hash uint64
	bitPos := 0
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			if gray[row][col] > gray[row][col+1] {
				hash |= (1 << bitPos)
			}
			bitPos++
		}
	}
	return hash
}

// DHash64Image computes a 64-bit gradient hash from any generic image.Image.
func DHash64Image(img image.Image) uint64 {
	if rgba, ok := img.(*image.RGBA); ok {
		return DHash64(rgba)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 9 || h < 8 {
		return 0
	}

	var gray [8][9]uint32
	yStep := h / 8
	xStep := w / 9

	for row := 0; row < 8; row++ {
		sy := bounds.Min.Y + row*yStep + (yStep / 2)
		for col := 0; col < 9; col++ {
			sx := bounds.Min.X + col*xStep + (xStep / 2)
			r, g, b, _ := img.At(sx, sy).RGBA()
			r8 := r >> 8
			g8 := g >> 8
			b8 := b >> 8
			gray[row][col] = (r8*77 + g8*150 + b8*29) >> 8
		}
	}

	var hash uint64
	bitPos := 0
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			if gray[row][col] > gray[row][col+1] {
				hash |= (1 << bitPos)
			}
			bitPos++
		}
	}
	return hash
}

// HammingDistance calculates the bitwise Hamming distance between two 64-bit hashes
// using the single-cycle x86 POPCNT instruction via math/bits.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}
