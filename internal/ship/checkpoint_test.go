package ship

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCheckpointStoreRoundTripIsPrivate(t *testing.T) {
	fixture := newFixture(t)
	plan, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state", "ship.json")
	store, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := newCheckpoint(plan)
	checkpoint.complete(StepValidateTarget)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint permissions = %o", info.Mode().Perm())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.matches(plan) || !loaded.has(StepValidateTarget) {
		t.Fatalf("loaded checkpoint = %#v", loaded)
	}
}

func TestFileCheckpointStoreRejectsSymlinkAndLoosePermissions(t *testing.T) {
	fixture := newFixture(t)
	plan, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	realPath := filepath.Join(directory, "real.json")
	realStore, err := NewFileCheckpointStore(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := realStore.Save(newCheckpoint(plan)); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(directory, "link.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkStore, err := NewFileCheckpointStore(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkStore.Load(); err == nil ||
		!strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("Load symlink error = %v", err)
	}
	if err := linkStore.Save(newCheckpoint(plan)); err == nil ||
		!strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("Save symlink error = %v", err)
	}

	if err := os.Chmod(realPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := realStore.Load(); err == nil ||
		!strings.Contains(err.Error(), "too permissive") {
		t.Fatalf("Load permissions error = %v", err)
	}
}

func TestFileCheckpointStoreRejectsUnknownAndOversizedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ship.json")
	store, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load unknown field error = %v", err)
	}
	if err := os.WriteFile(path, make([]byte, maximumCheckpointSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load oversized error = %v", err)
	}
}

func TestFileCheckpointStoreNotFound(t *testing.T) {
	store, err := NewFileCheckpointStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Load()
	if !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("Load error = %v", err)
	}
}

func TestValidateCheckpointRejectsAmbiguousSubmitWithoutMarker(t *testing.T) {
	fixture := newFixture(t)
	plan, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := newCheckpoint(plan)
	checkpoint.AmbiguousSubmission = true
	if err := validateCheckpoint(checkpoint); err == nil ||
		!strings.Contains(err.Error(), "pending submit_review") {
		t.Fatalf("validateCheckpoint error = %v", err)
	}
}
