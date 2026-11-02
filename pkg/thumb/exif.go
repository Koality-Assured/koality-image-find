package thumb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

var (
	errNoExif         = errors.New("no exif app1 marker found")
	errInvalidExif    = errors.New("invalid exif header")
	errNoIFD1Thumb    = errors.New("no ifd1 thumbnail found")
	errMalformedThumb = errors.New("malformed thumbnail bounds")
)

// ExtractExifThumbnail attempts to extract the pre-computed embedded thumbnail (IFD1)
// from the first 64KB of a JPEG or RAW file.
// Returns the raw JPEG bytes of the embedded thumbnail if present.
func ExtractExifThumbnail(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read up to 64KB
	buf := make([]byte, 65536)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	if n < 128 {
		return nil, errNoExif
	}
	buf = buf[:n]

	// Check JPEG SOI
	if buf[0] != 0xFF || buf[1] != 0xD8 {
		return nil, errNoExif
	}

	// Locate APP1 marker (0xFF, 0xE1)
	pos := 2
	var app1Data []byte

	for pos < len(buf)-4 {
		if buf[pos] != 0xFF {
			break
		}
		marker := buf[pos+1]
		if marker == 0xE1 {
			// Found APP1
			length := int(binary.BigEndian.Uint16(buf[pos+2 : pos+4]))
			if pos+2+length > len(buf) {
				length = len(buf) - (pos + 2)
			}
			app1Data = buf[pos+4 : pos+2+length]
			break
		}
		if marker == 0xDA { // Start of scan, no more metadata
			break
		}
		length := int(binary.BigEndian.Uint16(buf[pos+2 : pos+4]))
		pos += 2 + length
	}

	if len(app1Data) < 16 {
		return nil, errNoExif
	}

	// Verify "Exif\0\0"
	if !bytes.Equal(app1Data[:6], []byte("Exif\x00\x00")) {
		return nil, errInvalidExif
	}

	tiffData := app1Data[6:]
	if len(tiffData) < 8 {
		return nil, errInvalidExif
	}

	// Determine byte order
	var order binary.ByteOrder
	if tiffData[0] == 'I' && tiffData[1] == 'I' {
		order = binary.LittleEndian
	} else if tiffData[0] == 'M' && tiffData[1] == 'M' {
		order = binary.BigEndian
	} else {
		return nil, errInvalidExif
	}

	if order.Uint16(tiffData[2:4]) != 42 {
		return nil, errInvalidExif
	}

	ifd0Offset := int(order.Uint32(tiffData[4:8]))
	if ifd0Offset < 8 || ifd0Offset+2 > len(tiffData) {
		return nil, errInvalidExif
	}

	// Read IFD0 entry count
	ifd0Count := int(order.Uint16(tiffData[ifd0Offset : ifd0Offset+2]))
	nextIFDOffsetPos := ifd0Offset + 2 + ifd0Count*12
	if nextIFDOffsetPos+4 > len(tiffData) {
		return nil, errNoIFD1Thumb
	}

	// IFD1 offset is located immediately after IFD0 tags
	ifd1Offset := int(order.Uint32(tiffData[nextIFDOffsetPos : nextIFDOffsetPos+4]))
	if ifd1Offset == 0 || ifd1Offset+2 > len(tiffData) {
		return nil, errNoIFD1Thumb
	}

	ifd1Count := int(order.Uint16(tiffData[ifd1Offset : ifd1Offset+2]))
	var thumbOffset, thumbLen int

	curr := ifd1Offset + 2
	for i := 0; i < ifd1Count && curr+12 <= len(tiffData); i++ {
		tag := order.Uint16(tiffData[curr : curr+2])
		val := order.Uint32(tiffData[curr+8 : curr+12])

		if tag == 0x0201 { // JPEGInterchangeFormat (thumbnail offset)
			thumbOffset = int(val)
		} else if tag == 0x0202 { // JPEGInterchangeFormatLength (thumbnail length)
			thumbLen = int(val)
		}
		curr += 12
	}

	if thumbOffset <= 0 || thumbLen <= 0 {
		return nil, errNoIFD1Thumb
	}

	// Validate bounds
	if thumbOffset+thumbLen > len(tiffData) {
		// If thumbnail exceeds 64KB buffer, read from the file directly at the exact position
		// Calculate file position of tiffData
		app1FilePos := int64(pos + 4 + 6 + thumbOffset)
		if thumbLen > 10*1024*1024 { // Sanity check: max 10MB thumbnail
			return nil, errMalformedThumb
		}

		thumbBuf := make([]byte, thumbLen)
		_, err := f.ReadAt(thumbBuf, app1FilePos)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(thumbBuf) >= 2 && thumbBuf[0] == 0xFF && thumbBuf[1] == 0xD8 {
			return thumbBuf, nil
		}
		return nil, errMalformedThumb
	}

	thumbData := tiffData[thumbOffset : thumbOffset+thumbLen]
	if len(thumbData) >= 2 && thumbData[0] == 0xFF && thumbData[1] == 0xD8 {
		return thumbData, nil
	}

	return nil, errMalformedThumb
}
