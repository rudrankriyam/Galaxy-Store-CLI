//go:build !windows

package ship

import (
	"io/fs"
	"os"
)

func checkpointPermissionsTooPermissive(mode fs.FileMode) bool {
	return mode.Perm()&0o077 != 0
}

func replaceCheckpointFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func syncCheckpointDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
