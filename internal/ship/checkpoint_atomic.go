package ship

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writeCheckpointAtomic(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := ensureCheckpointParent(parent); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("shipping checkpoint must be a regular file, not a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect shipping checkpoint: %w", err)
	}

	file, err := os.CreateTemp(parent, ".gsc-ship-checkpoint-*")
	if err != nil {
		return fmt.Errorf("create shipping checkpoint temporary file: %w", err)
	}
	tempPath := file.Name()
	installed := false
	defer func() {
		_ = file.Close()
		if !installed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure shipping checkpoint: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write shipping checkpoint: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync shipping checkpoint: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close shipping checkpoint: %w", err)
	}
	if err := replaceCheckpointFile(tempPath, path); err != nil {
		return fmt.Errorf("install shipping checkpoint: %w", err)
	}
	installed = true
	if err := syncCheckpointDirectory(parent); err != nil {
		return fmt.Errorf("sync shipping checkpoint directory: %w", err)
	}
	return nil
}

func ensureCheckpointParent(parent string) error {
	info, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("create shipping checkpoint directory: %w", err)
		}
		info, err = os.Lstat(parent)
	}
	if err != nil {
		return fmt.Errorf("inspect shipping checkpoint directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("shipping checkpoint directory must be a directory, not a symlink")
	}
	return nil
}
