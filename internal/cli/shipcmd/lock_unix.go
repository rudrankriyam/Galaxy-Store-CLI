//go:build !windows

package shipcmd

import "os"

func removeOwnedLock(file *os.File, path string) error {
	removeErr := os.Remove(path)
	closeErr := file.Close()
	if removeErr != nil {
		return removeErr
	}
	return closeErr
}
