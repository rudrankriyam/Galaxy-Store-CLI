//go:build windows

package config

import "io/fs"

func configFilePermissionsTooPermissive(fs.FileMode) bool {
	// Windows ACLs, rather than POSIX permission bits, are authoritative.
	return false
}
