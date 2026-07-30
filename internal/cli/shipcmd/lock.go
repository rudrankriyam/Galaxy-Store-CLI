package shipcmd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrRunLocked indicates that another process owns the checkpoint's sibling
// lock and may be shipping the same target.
var ErrRunLocked = errors.New("shipping checkpoint is locked by another run")

type fileLock struct {
	path     string
	file     *os.File
	identity os.FileInfo
	released bool
}

// AcquireFileLock creates a private O_EXCL sibling lock for checkpointPath.
// The caller must release the returned lock.
func AcquireFileLock(checkpointPath string) (Lock, error) {
	if strings.TrimSpace(checkpointPath) == "" {
		return nil, errors.New("shipping checkpoint path is required for locking")
	}
	absolute, err := filepath.Abs(filepath.Clean(checkpointPath))
	if err != nil {
		return nil, fmt.Errorf("resolve shipping checkpoint lock path: %w", err)
	}
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, errors.New("shipping checkpoint path must not be a filesystem root")
	}
	parent := filepath.Dir(absolute)
	if err := ensureLockParent(parent); err != nil {
		return nil, err
	}
	path := absolute + ".lock"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%w: %s", ErrRunLocked, path)
	}
	if err != nil {
		return nil, fmt.Errorf("create shipping checkpoint lock: %w", err)
	}
	owned := false
	defer func() {
		if !owned {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("secure shipping checkpoint lock: %w", err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("create shipping checkpoint lock identity: %w", err)
	}
	if _, err := file.WriteString(hex.EncodeToString(token) + "\n"); err != nil {
		return nil, fmt.Errorf("write shipping checkpoint lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync shipping checkpoint lock: %w", err)
	}
	identity, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect shipping checkpoint lock: %w", err)
	}
	owned = true
	return &fileLock{path: path, file: file, identity: identity}, nil
}

func ensureLockParent(parent string) error {
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

func (lock *fileLock) Release() error {
	if lock == nil || lock.released {
		return nil
	}
	lock.released = true
	if lock.file == nil || lock.identity == nil {
		return errors.New("shipping checkpoint lock is not configured")
	}
	current, err := os.Lstat(lock.path)
	if errors.Is(err, os.ErrNotExist) {
		return lock.file.Close()
	}
	if err != nil {
		_ = lock.file.Close()
		return fmt.Errorf("inspect shipping checkpoint lock during release: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 ||
		!current.Mode().IsRegular() ||
		!os.SameFile(lock.identity, current) {
		_ = lock.file.Close()
		return errors.New("shipping checkpoint lock ownership changed; refusing to remove it")
	}
	if err := removeOwnedLock(lock.file, lock.path); err != nil {
		return fmt.Errorf("release shipping checkpoint lock: %w", err)
	}
	return nil
}
