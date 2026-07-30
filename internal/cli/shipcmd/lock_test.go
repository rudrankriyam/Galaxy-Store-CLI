package shipcmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileLockRefusesConcurrentRunAndCanBeReacquired(t *testing.T) {
	t.Parallel()
	checkpoint := filepath.Join(t.TempDir(), "private", "checkpoint.json")
	first, err := AcquireFileLock(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := checkpoint + ".lock"
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock mode = %o", info.Mode().Perm())
	}
	if _, err := AcquireFileLock(checkpoint); !errors.Is(err, ErrRunLocked) {
		t.Fatalf("concurrent acquire error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains after release: %v", err)
	}
	second, err := AcquireFileLock(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release = %v", err)
	}
}

func TestFileLockRejectsSymlinkParent(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := AcquireFileLock(filepath.Join(link, "checkpoint.json"))
	if err == nil {
		t.Fatal("AcquireFileLock accepted a symlink parent")
	}
}
