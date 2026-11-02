package winapi

import (
	"testing"
)

func TestExtendedPath(t *testing.T) {
	tests := []struct {
		input           string
		wantExtended    string
		wantStripped    string
	}{
		{`C:\Photos\image.jpg`, `\\?\C:\Photos\image.jpg`, `C:\Photos\image.jpg`},
		{`\\server\share\photo.png`, `\\?\UNC\server\share\photo.png`, `\\server\share\photo.png`},
		{`\\?\C:\Photos\image.jpg`, `\\?\C:\Photos\image.jpg`, `C:\Photos\image.jpg`},
		{`\\?\UNC\server\share\photo.png`, `\\?\UNC\server\share\photo.png`, `\\server\share\photo.png`},
	}

	for _, tt := range tests {
		got := ToExtendedPath(tt.input)
		if got != tt.wantExtended {
			t.Errorf("ToExtendedPath(%q) = %q; want %q", tt.input, got, tt.wantExtended)
		}
		back := FromExtendedPath(got)
		if back != tt.wantStripped {
			t.Errorf("FromExtendedPath(%q) = %q; want %q", got, back, tt.wantStripped)
		}
	}
}

func TestCloudReparsePointDetection(t *testing.T) {
	// Standard local file
	localFile := &Win32FindData{
		FileAttributes: FILE_ATTRIBUTE_ARCHIVE,
		Reserved0:      0,
	}
	if localFile.IsCloudPlaceholder() {
		t.Errorf("localFile should NOT be detected as cloud placeholder")
	}

	// Offline file
	offlineFile := &Win32FindData{
		FileAttributes: FILE_ATTRIBUTE_ARCHIVE | FILE_ATTRIBUTE_OFFLINE,
	}
	if !offlineFile.IsCloudPlaceholder() {
		t.Errorf("offlineFile MUST be detected as cloud placeholder")
	}

	// OneDrive cloud placeholder reparse tag
	oneDrivePlaceholder := &Win32FindData{
		FileAttributes: FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_ARCHIVE,
		Reserved0:      IO_REPARSE_TAG_CLOUD,
	}
	if !oneDrivePlaceholder.IsCloudPlaceholder() {
		t.Errorf("OneDrive placeholder MUST be detected as cloud placeholder")
	}

	// Directory Junction (should be skipped from dir traversal, but not cloud placeholder)
	junction := &Win32FindData{
		FileAttributes: FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT,
		Reserved0:      IO_REPARSE_TAG_MOUNT_POINT,
	}
	if !junction.IsSkipDirectory("AppData") {
		t.Errorf("Junction directory MUST be skipped")
	}
	if junction.IsCloudPlaceholder() {
		t.Errorf("Junction directory is not a cloud placeholder")
	}
}
