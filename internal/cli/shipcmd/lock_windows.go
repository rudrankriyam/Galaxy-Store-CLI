//go:build windows

package shipcmd

import "os"

func removeOwnedLock(file *os.File, path string) error {
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}
