package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumBundleFileSize = 16 << 20

// WriteOptions controls safe bundle replacement.
type WriteOptions struct {
	Overwrite bool
}

// ReadBundle reads and validates a complete three-file metadata bundle.
func ReadBundle(directory string) (*Bundle, error) {
	directory, err := safeBundlePath(directory)
	if err != nil {
		return nil, err
	}
	directoryInfo, err := requireDirectory(directory)
	if err != nil {
		return nil, err
	}

	manifestData, err := readRegularFile(
		filepath.Join(directory, ManifestFilename),
	)
	if err != nil {
		return nil, err
	}
	metadataData, err := readRegularFile(
		filepath.Join(directory, MetadataFilename),
	)
	if err != nil {
		return nil, err
	}
	sourceData, err := readRegularFile(filepath.Join(directory, SourceFilename))
	if err != nil {
		return nil, err
	}
	currentDirectoryInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("recheck metadata bundle directory: %w", err)
	}
	if currentDirectoryInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(directoryInfo, currentDirectoryInfo) {
		return nil, errors.New("metadata bundle changed while it was being read")
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("read %s: invalid JSON: %w", ManifestFilename, err)
	}
	bundle := &Bundle{
		Manifest: manifest,
		Metadata: metadataData,
		Source:   sourceData,
	}
	if err := validateBundle(*bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

// WriteBundle validates and atomically installs a complete metadata bundle.
// Existing bundles and all symlink targets are refused unless the caller
// explicitly opts into replacing a regular three-file bundle.
func WriteBundle(
	directory string,
	bundle Bundle,
	options WriteOptions,
) error {
	if err := validateBundle(bundle); err != nil {
		return err
	}
	directory, err := safeBundlePath(directory)
	if err != nil {
		return err
	}

	exists, err := inspectDestination(directory, options.Overwrite)
	if err != nil {
		return err
	}
	parent := filepath.Dir(directory)
	if err := ensureRegularParent(parent); err != nil {
		return err
	}

	staging, err := os.MkdirTemp(parent, "."+filepath.Base(directory)+".tmp-")
	if err != nil {
		return fmt.Errorf("create metadata staging directory: %w", err)
	}
	stagingOwned := true
	defer func() {
		if stagingOwned {
			_ = os.RemoveAll(staging)
		}
	}()

	manifestData, err := json.MarshalIndent(bundle.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", ManifestFilename, err)
	}
	manifestData = append(manifestData, '\n')
	metadataData, err := indentJSON(bundle.Metadata)
	if err != nil {
		return fmt.Errorf("encode %s: %w", MetadataFilename, err)
	}
	sourceData, err := indentJSON(bundle.Source)
	if err != nil {
		return fmt.Errorf("encode %s: %w", SourceFilename, err)
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{name: ManifestFilename, data: manifestData},
		{name: MetadataFilename, data: metadataData},
		{name: SourceFilename, data: sourceData},
	} {
		if err := writeNewFile(filepath.Join(staging, file.name), file.data); err != nil {
			return err
		}
	}

	if !exists {
		if err := os.Rename(staging, directory); err != nil {
			return fmt.Errorf("install metadata bundle: %w", err)
		}
		stagingOwned = false
		return nil
	}

	backup, err := reserveSiblingPath(parent, filepath.Base(directory)+".old-")
	if err != nil {
		return err
	}
	if err := os.Rename(directory, backup); err != nil {
		return fmt.Errorf("prepare metadata bundle replacement: %w", err)
	}
	if err := os.Rename(staging, directory); err != nil {
		restoreErr := os.Rename(backup, directory)
		if restoreErr != nil {
			return fmt.Errorf(
				"install metadata bundle: %w; restore previous bundle: %w",
				err,
				restoreErr,
			)
		}
		return fmt.Errorf("install metadata bundle: %w", err)
	}
	stagingOwned = false
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced metadata bundle: %w", err)
	}
	return nil
}

func validateBundle(bundle Bundle) error {
	if err := validateManifest(bundle.Manifest); err != nil {
		return err
	}
	if err := ValidateEnvelope(bundle.Manifest.ContentID, bundle.Metadata); err != nil {
		return err
	}
	sourceFields, err := decodeObject(bundle.Source, "metadata source")
	if err != nil {
		return err
	}
	contentID, err := requiredString(sourceFields, "contentId")
	if err != nil {
		return fmt.Errorf("metadata source: %w", err)
	}
	status, err := requiredString(sourceFields, "appStatus")
	if err != nil {
		return fmt.Errorf("metadata source: %w", err)
	}
	if contentID != bundle.Manifest.ContentID ||
		AppStatus(status) != bundle.Manifest.AppStatus {
		return errors.New("metadata source identity does not match manifest")
	}
	hash, err := CanonicalSHA256(bundle.Source)
	if err != nil {
		return err
	}
	if hash != bundle.Manifest.SourceSHA256 {
		return errors.New("metadata source SHA-256 does not match manifest")
	}
	return nil
}

func safeBundlePath(directory string) (string, error) {
	if directory == "" {
		return "", errors.New("metadata bundle directory is required")
	}
	clean := filepath.Clean(directory)
	if clean == "." {
		return "", errors.New("metadata bundle directory must not be the current directory")
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("resolve metadata bundle directory: %w", err)
	}
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", errors.New("metadata bundle directory must not be a filesystem root")
	}
	return absolute, nil
}

func requireDirectory(directory string) (os.FileInfo, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("read metadata bundle directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("metadata bundle directory must not be a symlink")
	}
	if !info.IsDir() {
		return nil, errors.New("metadata bundle path must be a directory")
	}
	return info, nil
}

func inspectDestination(directory string, overwrite bool) (bool, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect metadata bundle directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("metadata bundle directory must not be a symlink")
	}
	if !info.IsDir() {
		return false, errors.New("metadata bundle path must be a directory")
	}
	if !overwrite {
		return false, ErrOverwrite
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, fmt.Errorf("inspect existing metadata bundle: %w", err)
	}
	allowed := map[string]struct{}{
		ManifestFilename: {},
		MetadataFilename: {},
		SourceFilename:   {},
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return false, fmt.Errorf(
				"refusing to overwrite metadata directory containing unexpected entry %q",
				entry.Name(),
			)
		}
		info, infoErr := os.Lstat(filepath.Join(directory, entry.Name()))
		if infoErr != nil {
			return false, fmt.Errorf("inspect existing metadata bundle: %w", infoErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf(
				"metadata bundle file %q must be a regular file, not a symlink",
				entry.Name(),
			)
		}
	}
	return true, nil
}

func ensureRegularParent(parent string) error {
	info, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("create metadata bundle parent: %w", err)
		}
		info, err = os.Lstat(parent)
	}
	if err != nil {
		return fmt.Errorf("inspect metadata bundle parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("metadata bundle parent must not be a symlink")
	}
	if !info.IsDir() {
		return errors.New("metadata bundle parent must be a directory")
	}
	return nil
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"metadata bundle file %q must be a regular file, not a symlink",
			filepath.Base(path),
		)
	}
	if info.Size() > maximumBundleFileSize {
		return nil, fmt.Errorf(
			"metadata bundle file %q exceeds %d bytes",
			filepath.Base(path),
			maximumBundleFileSize,
		)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read metadata bundle file %q: %w", filepath.Base(path), err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf(
			"metadata bundle file %q changed while it was being opened",
			filepath.Base(path),
		)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumBundleFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if len(data) > maximumBundleFileSize {
		return nil, fmt.Errorf(
			"metadata bundle file %q exceeds %d bytes",
			filepath.Base(path),
			maximumBundleFileSize,
		)
	}
	return data, nil
}

func indentJSON(value json.RawMessage) ([]byte, error) {
	var output bytes.Buffer
	if err := json.Indent(&output, bytes.TrimSpace(value), "", "  "); err != nil {
		return nil, err
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func writeNewFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync metadata bundle file %q: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close metadata bundle file %q: %w", filepath.Base(path), err)
	}
	return nil
}

func reserveSiblingPath(parent string, pattern string) (string, error) {
	path, err := os.MkdirTemp(parent, "."+pattern)
	if err != nil {
		return "", fmt.Errorf("reserve metadata backup path: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("reserve metadata backup path: %w", err)
	}
	return path, nil
}
