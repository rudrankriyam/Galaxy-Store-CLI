package metadata

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

func TestWriteAndReadBundleRoundTrip(t *testing.T) {
	t.Parallel()

	bundle := mustBundle(t, sourceForSale(`"appTitle":"Original"`))
	directory := filepath.Join(t.TempDir(), "metadata")
	if err := WriteBundle(directory, bundle, WriteOptions{}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	for _, name := range []string{
		ManifestFilename,
		MetadataFilename,
		SourceFilename,
	} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("Stat %s: %v", name, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%s mode = %v, want regular", name, info.Mode())
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %o, want private", name, info.Mode().Perm())
		}
	}

	read, err := ReadBundle(directory)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if read.Manifest != bundle.Manifest {
		t.Fatalf("manifest = %+v, want %+v", read.Manifest, bundle.Manifest)
	}
	gotHash, err := CanonicalSHA256(read.Source)
	if err != nil {
		t.Fatalf("hash source: %v", err)
	}
	if gotHash != bundle.Manifest.SourceSHA256 {
		t.Fatalf("source hash = %s, want %s", gotHash, bundle.Manifest.SourceSHA256)
	}
}

func TestWriteBundleRequiresExplicitSafeOverwrite(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "metadata")
	first := mustBundle(t, sourceForSale(`"appTitle":"First"`))
	if err := WriteBundle(directory, first, WriteOptions{}); err != nil {
		t.Fatalf("WriteBundle first: %v", err)
	}
	second := mustBundle(t, sourceForSale(`"appTitle":"Second"`))

	err := WriteBundle(directory, second, WriteOptions{})
	if !errors.Is(err, ErrOverwrite) {
		t.Fatalf("overwrite error = %v, want ErrOverwrite", err)
	}
	read, err := ReadBundle(directory)
	if err != nil {
		t.Fatalf("ReadBundle preserved first: %v", err)
	}
	if !strings.Contains(string(read.Source), `"First"`) {
		t.Fatalf("bundle changed without overwrite: %s", read.Source)
	}

	if err := WriteBundle(
		directory,
		second,
		WriteOptions{Overwrite: true},
	); err != nil {
		t.Fatalf("WriteBundle overwrite: %v", err)
	}
	read, err = ReadBundle(directory)
	if err != nil {
		t.Fatalf("ReadBundle second: %v", err)
	}
	if !strings.Contains(string(read.Source), `"Second"`) {
		t.Fatalf("bundle was not replaced: %s", read.Source)
	}

	if err := os.WriteFile(
		filepath.Join(directory, "notes.txt"),
		[]byte("user data"),
		0o600,
	); err != nil {
		t.Fatalf("write unexpected file: %v", err)
	}
	err = WriteBundle(directory, first, WriteOptions{Overwrite: true})
	if err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("unsafe overwrite error = %v", err)
	}
}

func TestBundleFilesystemRejectsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundle := mustBundle(t, sourceForSale(""))
	realDirectory := filepath.Join(root, "real")
	if err := WriteBundle(realDirectory, bundle, WriteOptions{}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	linkDirectory := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, linkDirectory); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadBundle(linkDirectory); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ReadBundle symlink error = %v", err)
	}
	if err := WriteBundle(
		linkDirectory,
		bundle,
		WriteOptions{Overwrite: true},
	); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteBundle directory symlink error = %v", err)
	}

	metadataPath := filepath.Join(realDirectory, MetadataFilename)
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove metadata: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(realDirectory, SourceFilename),
		metadataPath,
	); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}
	if _, err := ReadBundle(realDirectory); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ReadBundle file symlink error = %v", err)
	}
	if err := WriteBundle(
		realDirectory,
		bundle,
		WriteOptions{Overwrite: true},
	); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteBundle file symlink error = %v", err)
	}
}

func TestReadBundleDetectsTampering(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "metadata")
	bundle := mustBundle(t, sourceForSale(`"appTitle":"Original"`))
	if err := WriteBundle(directory, bundle, WriteOptions{}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	tampered := sourceForSale(`"appTitle":"Tampered"`)
	encoded, err := json.MarshalIndent(json.RawMessage(tampered), "", "  ")
	if err != nil {
		t.Fatalf("encode tampered source: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, SourceFilename),
		encoded,
		0o600,
	); err != nil {
		t.Fatalf("tamper source: %v", err)
	}

	_, err = ReadBundle(directory)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("ReadBundle tamper error = %v", err)
	}
}

func mustBundle(t *testing.T, source json.RawMessage) Bundle {
	t.Helper()
	var record apps.App
	if err := json.Unmarshal(source, &record); err != nil {
		t.Fatalf("decode source record: %v", err)
	}
	bundle, err := NewBundle(
		record,
		time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	return *bundle
}
