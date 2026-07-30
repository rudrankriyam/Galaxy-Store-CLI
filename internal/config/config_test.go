package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPathUsesAbsoluteOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(configPathEnvVar, path)

	got, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
}

func TestPathRejectsRelativeOverride(t *testing.T) {
	t.Setenv(configPathEnvVar, "relative/config.json")
	if _, err := Path(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Path() error = %v, want ErrInvalid", err)
	}
}

func TestSaveAndLoadAtRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := &Config{
		DefaultProfile: "production",
		Profiles: map[string]Profile{
			"production": {
				ServiceAccountID: "service-account-id",
				PrivateKeyPath:   "/secure/galaxy-store.pem",
				Scopes:           []string{"publishing", "gss"},
			},
		},
	}

	if err := SaveAt(path, want); err != nil {
		t.Fatalf("SaveAt() error = %v", err)
	}
	got, err := LoadAt(path)
	if err != nil {
		t.Fatalf("LoadAt() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadAt() = %#v, want %#v", got, want)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if gotMode := info.Mode().Perm(); gotMode != 0o600 {
			t.Fatalf("config mode = %#o, want 0600", gotMode)
		}
	}
}

func TestSaveAtRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	err := SaveAt(path, cfg)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite symlink") {
		t.Fatalf("SaveAt() error = %v, want symlink refusal", err)
	}
}

func TestLoadAtNotFound(t *testing.T) {
	_, err := LoadAt(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadAt() error = %v, want ErrNotFound", err)
	}
}

func TestLoadAtRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows systems")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"profiles":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAt(path)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("LoadAt() error = %v, want ErrInvalid symlink refusal", err)
	}
}

func TestLoadAtRejectsNonRegularFile(t *testing.T) {
	_, err := LoadAt(t.TempDir())
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("LoadAt() error = %v, want ErrInvalid regular-file refusal", err)
	}
}

func TestLoadAtRejectsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not authoritative on Windows")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"profiles":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAt(path)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("LoadAt() error = %v, want ErrInvalid permission refusal", err)
	}
}

func TestLoadAtRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{"profiles":{},"access_token":"must-not-live-here"}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadAt(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("LoadAt() error = %v, want ErrInvalid", err)
	}
}

func TestSaveAtAtomicallyReplacesExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first := &Config{
		DefaultProfile: "first",
		Profiles: map[string]Profile{
			"first": {ServiceAccountID: "first-account"},
		},
	}
	second := &Config{
		DefaultProfile: "second",
		Profiles: map[string]Profile{
			"second": {ServiceAccountID: "second-account"},
		},
	}

	if err := SaveAt(path, first); err != nil {
		t.Fatalf("first SaveAt() error = %v", err)
	}
	if err := SaveAt(path, second); err != nil {
		t.Fatalf("second SaveAt() error = %v", err)
	}
	got, err := LoadAt(path)
	if err != nil {
		t.Fatalf("LoadAt() error = %v", err)
	}
	if !reflect.DeepEqual(got, second) {
		t.Fatalf("LoadAt() = %#v, want %#v", got, second)
	}
}

func TestValidateDefaultProfileMustExist(t *testing.T) {
	cfg := &Config{DefaultProfile: "missing"}
	if err := cfg.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateProfileMetadata(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
	}{
		{name: "missing service account", profile: Profile{PrivateKeyPath: "/key.pem"}},
		{name: "empty scope", profile: Profile{ServiceAccountID: "account", PrivateKeyPath: "/key.pem", Scopes: []string{""}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Profiles: map[string]Profile{"default": tt.profile}}
			if err := cfg.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateAllowsKeychainOnlyProfile(t *testing.T) {
	cfg := &Config{
		DefaultProfile: "default",
		Profiles: map[string]Profile{
			"default": {ServiceAccountID: "account"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestProfileNamesAreSorted(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"zeta": {}, "alpha": {}, "middle": {}}}
	got := cfg.ProfileNames()
	want := []string{"alpha", "middle", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProfileNames() = %v, want %v", got, want)
	}
}
