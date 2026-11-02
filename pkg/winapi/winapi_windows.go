//go:build windows
// +build windows

package winapi

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modshell32  = windows.NewLazySystemDLL("shell32.dll")
	modole32    = windows.NewLazySystemDLL("ole32.dll")

	procFindFirstFileExW = modkernel32.NewProc("FindFirstFileExW")
	procFindNextFileW    = modkernel32.NewProc("FindNextFileW")
	procFindClose        = modkernel32.NewProc("FindClose")
	procGetDriveTypeW    = modkernel32.NewProc("GetDriveTypeW")

	procShellExecuteExW          = modshell32.NewProc("ShellExecuteExW")
	procSHOpenFolderAndSelect    = modshell32.NewProc("SHOpenFolderAndSelectItems")
	procSHParseDisplayName       = modshell32.NewProc("SHParseDisplayName")
	procILFree                   = modshell32.NewProc("ILFree")
	procSHCreateItemFromParsing  = modshell32.NewProc("SHCreateItemFromParsingName")

	procCoInitializeEx   = modole32.NewProc("CoInitializeEx")
	procCoUninitialize   = modole32.NewProc("CoUninitialize")
	procCoCreateInstance = modole32.NewProc("CoCreateInstance")
)

const (
	FindExInfoStandard = 0
	FindExInfoBasic    = 1

	FindExSearchNameMatch = 0

	FIND_FIRST_EX_CASE_SENSITIVE   = 1
	FIND_FIRST_EX_LARGE_FETCH      = 2
	FIND_FIRST_EX_ON_DISK_ENTRIES_ONLY = 4

	FILE_ATTRIBUTE_READONLY      = 0x00000001
	FILE_ATTRIBUTE_HIDDEN        = 0x00000002
	FILE_ATTRIBUTE_SYSTEM        = 0x00000004
	FILE_ATTRIBUTE_DIRECTORY     = 0x00000010
	FILE_ATTRIBUTE_ARCHIVE       = 0x00000020
	FILE_ATTRIBUTE_NORMAL        = 0x00000080
	FILE_ATTRIBUTE_TEMPORARY     = 0x00000100
	FILE_ATTRIBUTE_REPARSE_POINT = 0x00000400
	FILE_ATTRIBUTE_OFFLINE       = 0x00001000

	// Reparse Tags
	IO_REPARSE_TAG_MOUNT_POINT  = 0xA0000003
	IO_REPARSE_TAG_SYMLINK      = 0xA000000C
	IO_REPARSE_TAG_WCI          = 0x80000018
	IO_REPARSE_TAG_ONEDRIVE     = 0x80000021
	IO_REPARSE_TAG_STORAGE_SYNC = 0x8000001E
	IO_REPARSE_TAG_CLOUD        = 0x9000001A
	IO_REPARSE_TAG_CLOUD_1      = 0x9000101A
	IO_REPARSE_TAG_CLOUD_2      = 0x9000201A
	IO_REPARSE_TAG_CLOUD_3      = 0x9000301A
	IO_REPARSE_TAG_CLOUD_4      = 0x9000401A
	IO_REPARSE_TAG_CLOUD_5      = 0x9000501A
	IO_REPARSE_TAG_CLOUD_6      = 0x9000601A
	IO_REPARSE_TAG_CLOUD_7      = 0x9000701A
	IO_REPARSE_TAG_CLOUD_8      = 0x9000801A
	IO_REPARSE_TAG_CLOUD_9      = 0x9000901A
	IO_REPARSE_TAG_CLOUD_A      = 0x9000A01A
	IO_REPARSE_TAG_CLOUD_B      = 0x9000B01A
	IO_REPARSE_TAG_CLOUD_C      = 0x9000C01A
	IO_REPARSE_TAG_CLOUD_D      = 0x9000D01A
	IO_REPARSE_TAG_CLOUD_E      = 0x9000E01A
	IO_REPARSE_TAG_CLOUD_F      = 0x9000F01A

	// COM Apartment Threading Flags
	COINIT_APARTMENTTHREADED = 0x2
	COINIT_MULTITHREADED     = 0x0
	COINIT_DISABLE_OLE1DDE   = 0x4
)

// Win32FindData matches Windows WIN32_FIND_DATAW structure
type Win32FindData struct {
	FileAttributes    uint32
	CreationTime      syscall.Filetime
	LastAccessTime    syscall.Filetime
	LastWriteTime     syscall.Filetime
	FileSizeHigh      uint32
	FileSizeLow       uint32
	Reserved0         uint32 // ReparseTag if FILE_ATTRIBUTE_REPARSE_POINT
	Reserved1         uint32
	FileName          [windows.MAX_PATH]uint16
	AlternateFileName [14]uint16
}

// FileSize returns the 64-bit file size.
func (d *Win32FindData) FileSize() int64 {
	return int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow)
}

// ModTime returns unix epoch seconds for the last write time.
func (d *Win32FindData) ModTime() int64 {
	return d.LastWriteTime.Nanoseconds() / 1e9
}

// NameString converts UTF-16 FileName to Go string.
func (d *Win32FindData) NameString() string {
	return syscall.UTF16ToString(d.FileName[:])
}

// IsDirectory checks if this entry is a directory.
func (d *Win32FindData) IsDirectory() bool {
	return (d.FileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0
}

// IsReparsePoint checks if this entry has a reparse point.
func (d *Win32FindData) IsReparsePoint() bool {
	return (d.FileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) != 0
}

// IsCloudPlaceholder detects if this file is a cloud placeholder (OneDrive/Dropbox/iCloud)
// that would trigger automatic network hydration if opened.
func (d *Win32FindData) IsCloudPlaceholder() bool {
	if (d.FileAttributes & FILE_ATTRIBUTE_OFFLINE) != 0 {
		return true
	}
	if !d.IsReparsePoint() {
		return false
	}
	tag := d.Reserved0
	switch tag {
	case IO_REPARSE_TAG_ONEDRIVE,
		IO_REPARSE_TAG_STORAGE_SYNC,
		IO_REPARSE_TAG_CLOUD,
		IO_REPARSE_TAG_CLOUD_1,
		IO_REPARSE_TAG_CLOUD_2,
		IO_REPARSE_TAG_CLOUD_3,
		IO_REPARSE_TAG_CLOUD_4,
		IO_REPARSE_TAG_CLOUD_5,
		IO_REPARSE_TAG_CLOUD_6,
		IO_REPARSE_TAG_CLOUD_7,
		IO_REPARSE_TAG_CLOUD_8,
		IO_REPARSE_TAG_CLOUD_9,
		IO_REPARSE_TAG_CLOUD_A,
		IO_REPARSE_TAG_CLOUD_B,
		IO_REPARSE_TAG_CLOUD_C,
		IO_REPARSE_TAG_CLOUD_D,
		IO_REPARSE_TAG_CLOUD_E,
		IO_REPARSE_TAG_CLOUD_F:
		return true
	}
	return false
}

// IsSkipDirectory checks if this directory should not be traversed
// (junction points, symlinks, dot folders, system volume information).
func (d *Win32FindData) IsSkipDirectory(name string) bool {
	if name == "." || name == ".." {
		return true
	}
	if strings.EqualFold(name, "System Volume Information") || strings.EqualFold(name, "$Recycle.Bin") {
		return true
	}
	if d.IsReparsePoint() {
		tag := d.Reserved0
		if tag == IO_REPARSE_TAG_MOUNT_POINT || tag == IO_REPARSE_TAG_SYMLINK {
			return true
		}
		if d.IsCloudPlaceholder() {
			return true
		}
	}
	return false
}

// FindFirstFileEx executes FindFirstFileExW with FindExInfoBasic and FIND_FIRST_EX_LARGE_FETCH
func FindFirstFileEx(pattern string, data *Win32FindData) (windows.Handle, error) {
	ptr, err := syscall.UTF16PtrFromString(ToExtendedPath(pattern))
	if err != nil {
		return windows.InvalidHandle, err
	}

	r0, _, errCode := procFindFirstFileExW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(FindExInfoBasic),
		uintptr(unsafe.Pointer(data)),
		uintptr(FindExSearchNameMatch),
		0,
		uintptr(FIND_FIRST_EX_LARGE_FETCH),
	)

	handle := windows.Handle(r0)
	if handle == windows.InvalidHandle {
		return windows.InvalidHandle, errCode
	}
	return handle, nil
}

// FindNextFile continues directory enumeration.
func FindNextFile(handle windows.Handle, data *Win32FindData) bool {
	r0, _, _ := procFindNextFileW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(data)),
	)
	return r0 != 0
}

// FindClose closes the directory find handle.
func FindClose(handle windows.Handle) error {
	r0, _, errCode := procFindClose.Call(uintptr(handle))
	if r0 == 0 {
		return errCode
	}
	return nil
}

// ToExtendedPath normalizes a Windows path to \\?\ extended format to bypass MAX_PATH 260 limit.
func ToExtendedPath(path string) string {
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, `\\?\`) {
		return clean
	}
	// UNC path: \\server\share -> \\?\UNC\server\share
	if strings.HasPrefix(clean, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(clean, `\\`)
	}
	// Drive path: C:\path -> \\?\C:\path
	if len(clean) >= 2 && clean[1] == ':' {
		return `\\?\` + clean
	}
	return clean
}

// FromExtendedPath strips \\?\ prefix for UI display.
func FromExtendedPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if strings.HasPrefix(path, `\\?\`) {
		return strings.TrimPrefix(path, `\\?\`)
	}
	return path
}

// CoInitialize initializes the COM library for Single-Threaded Apartment (STA).
func CoInitialize() error {
	r0, _, _ := procCoInitializeEx.Call(0, uintptr(COINIT_APARTMENTTHREADED|COINIT_DISABLE_OLE1DDE))
	// S_OK (0) or S_FALSE (1) means success or already initialized
	if int32(r0) < 0 {
		return syscall.Errno(r0)
	}
	return nil
}

// CoUninitialize uninitializes the COM library.
func CoUninitialize() {
	procCoUninitialize.Call()
}

// OpenFolderAndSelectItem selects a specific file in Windows File Explorer.
func OpenFolderAndSelectItem(fullPath string) error {
	itemPath := filepath.Clean(fullPath)
	pathPtr, err := syscall.UTF16PtrFromString(itemPath)
	if err != nil {
		return err
	}

	var pidl uintptr
	r0, _, _ := procSHParseDisplayName.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&pidl)),
		0,
		0,
	)
	if int32(r0) < 0 {
		// Fallback to ShellExecute explorer.exe
		return ShellExecute(0, "open", "explorer.exe", `/select,"`+itemPath+`"`, "", 1)
	}
	defer procILFree.Call(pidl)

	r1, _, _ := procSHOpenFolderAndSelect.Call(
		pidl,
		0,
		0,
		0,
	)
	if int32(r1) < 0 {
		return ShellExecute(0, "open", "explorer.exe", `/select,"`+itemPath+`"`, "", 1)
	}
	return nil
}

// ShellExecute runs a file or command via ShellExecuteExW.
func ShellExecute(hwnd uintptr, verb, file, params, dir string, showCmd int32) error {
	verbPtr, _ := syscall.UTF16PtrFromString(verb)
	filePtr, _ := syscall.UTF16PtrFromString(file)
	paramsPtr, _ := syscall.UTF16PtrFromString(params)
	dirPtr, _ := syscall.UTF16PtrFromString(dir)

	type SHELLEXECUTEINFOW struct {
		CbSize       uint32
		FMask        uint32
		Hwnd         uintptr
		LpVerb       *uint16
		LpFile       *uint16
		LpParameters *uint16
		LpDirectory  *uint16
		NShow        int32
		HInstApp     uintptr
		LpIDList     uintptr
		LpClass      *uint16
		HkeyClass    uintptr
		DwHotKey     uint32
		HIcon        uintptr
		HProcess     uintptr
	}

	info := SHELLEXECUTEINFOW{
		CbSize:       uint32(unsafe.Sizeof(SHELLEXECUTEINFOW{})),
		FMask:        0x00000040, // SEE_MASK_FLAG_NO_UI
		Hwnd:         hwnd,
		LpVerb:       verbPtr,
		LpFile:       filePtr,
		LpParameters: paramsPtr,
		LpDirectory:  dirPtr,
		NShow:        showCmd,
	}

	r0, _, errCode := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r0 == 0 {
		return errCode
	}
	return nil
}
