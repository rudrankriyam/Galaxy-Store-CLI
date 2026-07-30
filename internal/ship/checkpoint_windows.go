//go:build windows

package ship

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

func checkpointPermissionsTooPermissive(fs.FileMode) bool {
	return false
}

func replaceCheckpointFile(source string, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePointer,
		destinationPointer,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncCheckpointDirectory(string) error {
	return nil
}
