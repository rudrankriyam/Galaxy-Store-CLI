// Package config loads and stores gsc configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	configDirName    = "gsc"
	configFileName   = "config.json"
	configPathEnvVar = "GSC_CONFIG_PATH"
)

var (
	// ErrNotFound indicates that no configuration file exists at the selected path.
	ErrNotFound = errors.New("configuration not found")
	// ErrInvalid indicates that the configuration cannot be used safely.
	ErrInvalid = errors.New("invalid configuration")
)

// Profile contains non-secret Galaxy Store authentication metadata. A profile
// can use an access token held by the OS keychain, a private key file, or both
// (the keychain token is preferred). Access tokens and private key contents are
// never persisted in config.json.
type Profile struct {
	ServiceAccountID string   `json:"service_account_id"`
	PrivateKeyPath   string   `json:"private_key_path,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
}

// Config contains named Galaxy Store credential profiles.
type Config struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`
}

// Path returns the active configuration path. GSC_CONFIG_PATH can select an
// alternate absolute path, which is useful for isolated CI environments.
func Path() (string, error) {
	if override := strings.TrimSpace(os.Getenv(configPathEnvVar)); override != "" {
		clean := filepath.Clean(override)
		if !filepath.IsAbs(clean) {
			return "", fmt.Errorf("%w: %s must be an absolute path", ErrInvalid, configPathEnvVar)
		}
		return clean, nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, configDirName, configFileName), nil
}

// Load loads configuration from the active path.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadAt(path)
}

// LoadAt loads configuration from path.
func LoadAt(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: config path is empty", ErrInvalid)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: decode config: %v", ErrInvalid, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: config must contain one JSON object", ErrInvalid)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]Profile)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save stores configuration at the active path.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveAt(path, cfg)
}

// SaveAt stores configuration at path using a private, atomic file write.
func SaveAt(path string, cfg *Config) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: config path is empty", ErrInvalid)
	}
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrInvalid)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Remove removes the active configuration file. Removing a missing file is a
// successful no-op.
func Remove() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove config: %w", err)
	}
	return nil
}

// Validate checks profile names, required metadata, and the default selection.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: config is nil", ErrInvalid)
	}

	defaultProfile := strings.TrimSpace(c.DefaultProfile)
	if defaultProfile != "" {
		if err := validateProfileName(defaultProfile); err != nil {
			return err
		}
		if _, ok := c.Profiles[defaultProfile]; !ok {
			return fmt.Errorf("%w: default profile %q does not exist", ErrInvalid, defaultProfile)
		}
	}

	for name, profile := range c.Profiles {
		if err := validateProfileName(name); err != nil {
			return err
		}
		if strings.TrimSpace(profile.ServiceAccountID) == "" {
			return fmt.Errorf("%w: profile %q has no service account ID", ErrInvalid, name)
		}
		for _, scope := range profile.Scopes {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf("%w: profile %q contains an empty scope", ErrInvalid, name)
			}
		}
	}
	return nil
}

// ProfileNames returns profile names in stable lexical order.
func (c *Config) ProfileNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func validateProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: profile name is empty", ErrInvalid)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("%w: profile name %q has surrounding whitespace", ErrInvalid, name)
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) {
		return fmt.Errorf("%w: profile name %q contains unsupported characters", ErrInvalid, name)
	}
	return nil
}
