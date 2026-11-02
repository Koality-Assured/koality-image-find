# koality-image-find

[![Go Version](https://img.shields.io/badge/go-1.22+-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform](https://img.shields.io/badge/platform-windows-lightgrey.svg)](https://microsoft.com/windows)

**koality-image-find** is a high-performance Windows full-drive graphics image and video thumbnail viewer engineered to index, search, and visually browse hundreds of thousands of media files across local SSDs, HDDs, SD cards, and network shares in seconds.

---

## Key Features

- **Ultra-Fast Non-Elevated Traversal**: Multi-threaded Win32 `FindFirstFileExW` (`FindExInfoBasic` + `FIND_FIRST_EX_LARGE_FETCH`) scans 500,000+ files in seconds without requiring UAC administrator privileges.
- **Cloud Reparse Point Protection**: Safely detects and skips OneDrive, iCloud, and Dropbox placeholders (`IO_REPARSE_TAG_CLOUD_*`, `FILE_ATTRIBUTE_OFFLINE`) to prevent accidental, bandwidth-exhausting cloud downloads.
- **Junction & Loop Safe**: Avoids recursive directory junctions and symbolic link loops.
- **Sub-8ms Columnar Search**: Compact 32-byte in-memory records index 500,000 files in ~16MB RAM with parallel substring search across all CPU cores.
- **Multi-Tier Thumbnail Engine**:
  - **Tier 1 (Instant EXIF IFD1)**: Extracts pre-rendered embedded camera thumbnails in <0.3ms from the first 64KB APP1 header.
  - **Tier 2 (Pure Go Scaled Decoding)**: Scaled decoding with decompression bomb protection.
  - **Tier 3 (Windows Shell COM Pool)**: Thread-pinned Single-Threaded Apartment (STA) workers with an active Win32 message pump for native image (RAW, PSD, HEIC) and video (MP4, MOV, WMV, AVI) extraction via `IShellItemImageFactory`.
  - **Video Fallback**: Automatic discovery and streaming pipe invocation of FFmpeg for exotic containers (`.mkv`, `.webm`, `.flv`).
- **NTFS-Friendly `bbolt` Cache**: Memory-mapped key-value store prevents NTFS `$MFT` directory fragmentation and 4KB cluster slack space waste.
- **60+ FPS Virtual Grid**: Pure mathematical $O(1)$ virtual grid mounting only the visible 60–80 cards in the viewport.
- **Zero-Dependency Static Binary**: Compiles to a single standalone Windows executable (`koality-image-find.exe`) with `CGO_ENABLED=0` and embeds all frontend assets via `//go:embed`.
- **Authenticated Loopback API**: 256-bit cryptographically secure session tokens prevent unauthorized cross-origin access from external browsers.

---

## Architecture Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                 DESKTOP FRONTEND (WebView2 / Edge)          │
│  - Mathematical O(1) Virtual Grid (~80 visible DOM cards)   │
│  - Instant Search (<10ms debounce) & Facet Filters         │
│  - Fullscreen Lightbox (Image Zoom/Pan, Video Player)       │
└──────────────────────────────┬──────────────────────────────┘
                               │ Authenticated Loopback (Token Header)
┌──────────────────────────────▼──────────────────────────────┐
│                    GO BACKEND (Pure Go, Win32)              │
│                                                             │
│  ┌───────────────────────┐      ┌─────────────────────────┐ │
│  │   Scanner Engine      │      │     In-Memory Index     │ │
│  │ - FindFirstFileExW    │      │ - 32-byte ItemRecord    │ │
│  │ - Cloud Filter        │─────►│ - Packed String Store   │ │
│  │ - Junction Prevention │      │ - Parallel Search/Sort  │ │
│  └───────────────────────┘      └────────────┬────────────┘ │
│                                              │              │
│  ┌───────────────────────────────────────────▼────────────┐ │
│  │             Multi-Tier Thumbnail Pipeline              │ │
│  │  [Tier 1: EXIF IFD1] ──► [Tier 2: Pure-Go Scaled]     │ │
│  │  [Tier 3: Win32 STA Shell COM] ──► [FFmpeg Pipe]       │ │
│  └───────────────────────────┬────────────────────────────┘ │
│                              │                              │
│  ┌───────────────────────────▼────────────────────────────┐ │
│  │            Cache Layer: bbolt KV Store                 │ │
│  │  Key: SHA256(Path + Size + ModTime)                    │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

---

## Quick Start

### Prerequisites
- Windows 10 or Windows 11 (64-bit)
- Go 1.22+ (for building from source)
- Microsoft Edge (pre-installed on Windows 10/11)

### Building from Source

```powershell
git clone https://github.com/Koality-Assured/koality-image-find.git
cd koality-image-find
go build -ldflags="-s -w" -o koality-image-find.exe ./cmd/koality-image-find
```

### Running

```powershell
# Launch with default auto-detection (scans user profile and common picture/video folders)
./koality-image-find.exe

# Or specify specific root drives or directories:
./koality-image-find.exe -roots "C:\Users,D:\Photos,E:\Videos"
```

---

## Supported Formats

- **Graphics Images**: JPEG (`.jpg`, `.jpeg`), PNG (`.png`), GIF (`.gif`), WebP (`.webp`), Bitmap (`.bmp`), TIFF (`.tiff`, `.tif`), SVG (`.svg`), Icon (`.ico`), AVIF (`.avif`), HEIC/HEIF (`.heic`, `.heif`), Photoshop (`.psd`), Camera RAW (`.raw`, `.cr2`, `.nef`, `.arw`, `.dng`, `.orf`, `.rw2`).
- **Videos**: MP4 (`.mp4`), Matroska (`.mkv`), QuickTime (`.mov`), AVI (`.avi`), WebM (`.webm`), Windows Media (`.wmv`), Flash Video (`.flv`), MPEG Transport Stream (`.ts`, `.mts`), 3GP (`.3gp`).

---

## Security Model

- **Localhost Loopback Isolation**: Binds strictly to `127.0.0.1`.
- **Cryptographic Session Tokens**: Every launch generates a cryptographically random 256-bit token passed to the client interface. All REST and thumbnail endpoints reject requests missing the `X-Session-Token` header.
- **Path Whitelisting**: Thumbnail and desktop revelation endpoints only accept file paths that exist in the active verified index.
- **Safe Desktop Actions**: Uses Win32 `SHOpenFolderAndSelectItems` and `ShellExecuteExW` instead of invoking arbitrary command-line strings.

---

## License

Licensed under the [MIT License](LICENSE).
