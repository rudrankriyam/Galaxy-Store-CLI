//go:build !windows

package config

import "io/fs"

func configFilePermissionsTooPermissive(mode fs.FileMode) bool {
	return mode.Perm()&0o077 != 0
}
