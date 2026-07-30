// Package credentials resolves Galaxy Store credentials from flags,
// environment variables, and named config profiles.
package credentials

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
)

const (
	accessTokenEnv      = "GSC_ACCESS_TOKEN"
	serviceAccountIDEnv = "GSC_SERVICE_ACCOUNT_ID"
	profileEnv          = "GSC_PROFILE"
)

var (
	// ErrNotFound indicates that no usable access token or profile was found.
	ErrNotFound = errors.New("credentials not found")
	// ErrIncomplete indicates that a credential source omitted required data.
	ErrIncomplete = errors.New("incomplete credentials")
	// ErrInvalidPrivateKey indicates that a key is not an RSA private key.
	ErrInvalidPrivateKey = errors.New("invalid private key")
	// ErrInsecurePrivateKey indicates that a key file is readable by other users.
	ErrInsecurePrivateKey = errors.New("insecure private key permissions")
)

// Kind identifies how a request will authenticate.
type Kind string

const (
	// KindAccessToken uses a caller-provided Galaxy Store access token directly.
	KindAccessToken Kind = "access_token"
	// KindServiceAccount mints access tokens with a service account RSA key.
	KindServiceAccount Kind = "service_account"
)

// Options are explicit credential overrides, typically supplied by command
// flags. Explicit values take precedence over environment and config.
type Options struct {
	AccessToken      string
	ServiceAccountID string
	PrivateKeyPath   string
	Profile          string
	Scopes           []string
}

// Credentials contains resolved authentication material. Callers must never
// include AccessToken in logs or serialized command output.
type Credentials struct {
	Kind             Kind
	Profile          string
	AccessToken      string
	ServiceAccountID string
	PrivateKeyPath   string
	Scopes           []string
}

// Resolve selects one complete credential source. It never fills missing
// fields by mixing explicit options, environment variables, and profile data.
// Precedence is explicit credentials, explicit profile, environment credential
// pair, GSC_PROFILE, then the configured default or sole profile.
func Resolve(options Options) (Credentials, error) {
	if credentials, decided, err := resolveExplicitCredentials(options); decided {
		return credentials, err
	}

	explicitProfile := strings.TrimSpace(options.Profile)
	if explicitProfile != "" {
		cfg, err := config.Load()
		if err != nil {
			return Credentials{}, credentialConfigError(err)
		}
		store, err := openTokenStore()
		if err != nil {
			return Credentials{}, err
		}
		return resolveNamedProfile(explicitProfile, true, cfg, store)
	}

	envToken := strings.TrimSpace(os.Getenv(accessTokenEnv))
	envServiceAccountID := strings.TrimSpace(os.Getenv(serviceAccountIDEnv))
	if envToken != "" || envServiceAccountID != "" {
		if envToken == "" || envServiceAccountID == "" {
			return Credentials{}, fmt.Errorf(
				"%w: %s and %s must be set together",
				ErrIncomplete,
				accessTokenEnv,
				serviceAccountIDEnv,
			)
		}
		return Credentials{
			Kind:             KindAccessToken,
			AccessToken:      envToken,
			ServiceAccountID: envServiceAccountID,
		}, nil
	}

	cfg, err := config.Load()
	if err != nil {
		return Credentials{}, credentialConfigError(err)
	}
	store, err := openTokenStore()
	if err != nil {
		return Credentials{}, err
	}
	envProfile := strings.TrimSpace(os.Getenv(profileEnv))
	return resolveNamedProfile(envProfile, envProfile != "", cfg, store)
}

// ResolveFromConfig resolves credentials against an already-loaded config.
// It does not consult the OS keychain. Use ResolveFromConfigWithStore to
// include a deterministic token store.
func ResolveFromConfig(options Options, cfg *config.Config) (Credentials, error) {
	return ResolveFromConfigWithStore(options, cfg, nil)
}

// ResolveFromConfigWithStore resolves against fixed config and token-store
// snapshots while retaining the same strict precedence as Resolve.
func ResolveFromConfigWithStore(
	options Options,
	cfg *config.Config,
	store TokenStore,
) (Credentials, error) {
	if credentials, decided, err := resolveExplicitCredentials(options); decided {
		return credentials, err
	}

	explicitProfile := strings.TrimSpace(options.Profile)
	if explicitProfile != "" {
		return resolveNamedProfile(explicitProfile, true, cfg, store)
	}

	envToken := strings.TrimSpace(os.Getenv(accessTokenEnv))
	envServiceAccountID := strings.TrimSpace(os.Getenv(serviceAccountIDEnv))
	if envToken != "" || envServiceAccountID != "" {
		if envToken == "" || envServiceAccountID == "" {
			return Credentials{}, fmt.Errorf(
				"%w: %s and %s must be set together",
				ErrIncomplete,
				accessTokenEnv,
				serviceAccountIDEnv,
			)
		}
		return Credentials{
			Kind:             KindAccessToken,
			AccessToken:      envToken,
			ServiceAccountID: envServiceAccountID,
		}, nil
	}

	envProfile := strings.TrimSpace(os.Getenv(profileEnv))
	return resolveNamedProfile(envProfile, envProfile != "", cfg, store)
}

func resolveExplicitCredentials(options Options) (Credentials, bool, error) {
	accessToken := strings.TrimSpace(options.AccessToken)
	serviceAccountID := strings.TrimSpace(options.ServiceAccountID)
	privateKeyPath := strings.TrimSpace(options.PrivateKeyPath)
	if accessToken == "" && serviceAccountID == "" && privateKeyPath == "" {
		return Credentials{}, false, nil
	}

	if accessToken != "" {
		if serviceAccountID == "" {
			return Credentials{}, true, fmt.Errorf(
				"%w: an explicit access token requires an explicit service account ID",
				ErrIncomplete,
			)
		}
		if privateKeyPath != "" {
			return Credentials{}, true, fmt.Errorf(
				"%w: explicit access token and private key cannot be used together",
				ErrIncomplete,
			)
		}
		return Credentials{
			Kind:             KindAccessToken,
			Profile:          strings.TrimSpace(options.Profile),
			AccessToken:      accessToken,
			ServiceAccountID: serviceAccountID,
			Scopes:           append([]string(nil), options.Scopes...),
		}, true, nil
	}

	if serviceAccountID == "" || privateKeyPath == "" {
		return Credentials{}, true, fmt.Errorf(
			"%w: explicit service account ID and private key path must be set together",
			ErrIncomplete,
		)
	}
	return Credentials{
		Kind:             KindServiceAccount,
		Profile:          strings.TrimSpace(options.Profile),
		ServiceAccountID: serviceAccountID,
		PrivateKeyPath:   privateKeyPath,
		Scopes:           append([]string(nil), options.Scopes...),
	}, true, nil
}

func resolveNamedProfile(
	requested string,
	exact bool,
	cfg *config.Config,
	store TokenStore,
) (Credentials, error) {
	profileName, profile, err := selectProfile(requested, exact, cfg)
	if err != nil {
		return Credentials{}, err
	}

	if store != nil {
		token, tokenErr := store.Get(profileName)
		switch {
		case tokenErr == nil:
			if strings.TrimSpace(token) == "" {
				return Credentials{}, fmt.Errorf(
					"%w: keychain token for profile %q is empty",
					ErrIncomplete,
					profileName,
				)
			}
			return Credentials{
				Kind:             KindAccessToken,
				Profile:          profileName,
				AccessToken:      strings.TrimSpace(token),
				ServiceAccountID: strings.TrimSpace(profile.ServiceAccountID),
				Scopes:           append([]string(nil), profile.Scopes...),
			}, nil
		case errors.Is(tokenErr, ErrTokenNotFound):
			// A profile with no stored token may mint one from its private key.
		default:
			return Credentials{}, fmt.Errorf("load profile %q access token: %w", profileName, tokenErr)
		}
	}

	privateKeyPath := strings.TrimSpace(profile.PrivateKeyPath)
	if privateKeyPath == "" {
		return Credentials{}, fmt.Errorf(
			"%w: profile %q has no keychain token or private key path",
			ErrIncomplete,
			profileName,
		)
	}

	return Credentials{
		Kind:             KindServiceAccount,
		Profile:          profileName,
		ServiceAccountID: strings.TrimSpace(profile.ServiceAccountID),
		PrivateKeyPath:   privateKeyPath,
		Scopes:           append([]string(nil), profile.Scopes...),
	}, nil
}

func selectProfile(requested string, exact bool, cfg *config.Config) (string, config.Profile, error) {
	if cfg == nil {
		return "", config.Profile{}, fmt.Errorf("%w: config is nil", ErrNotFound)
	}
	if err := cfg.Validate(); err != nil {
		return "", config.Profile{}, err
	}

	profileName := strings.TrimSpace(requested)
	if profileName == "" && !exact {
		profileName = strings.TrimSpace(cfg.DefaultProfile)
	}
	if profileName == "" {
		if len(cfg.Profiles) == 1 {
			for name := range cfg.Profiles {
				profileName = name
			}
		} else {
			return "", config.Profile{}, fmt.Errorf(
				"%w: select a profile with --profile or %s",
				ErrNotFound,
				profileEnv,
			)
		}
	}
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		return "", config.Profile{}, fmt.Errorf("%w: profile %q does not exist", ErrNotFound, profileName)
	}
	return profileName, profile, nil
}

func credentialConfigError(err error) error {
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf(
			"%w: set %s and %s together or configure a profile",
			ErrNotFound,
			accessTokenEnv,
			serviceAccountIDEnv,
		)
	}
	return err
}

func openTokenStore() (TokenStore, error) {
	store, err := NewKeyringTokenStore()
	if err == nil {
		return store, nil
	}
	if keyringUnavailable(err) {
		return nil, nil
	}
	return nil, err
}

// OpenTokenStore opens the best available OS-native secure token store. A nil
// store with no error means this operating environment has no supported secure
// backend; callers may still use a complete environment credential pair.
func OpenTokenStore() (TokenStore, error) {
	return openTokenStore()
}

// LoadPrivateKey validates file permissions and parses a PKCS#1 or PKCS#8 RSA
// private key.
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: path is empty", ErrInvalidPrivateKey)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: stat key: %w", ErrInvalidPrivateKey, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: path is a directory", ErrInvalidPrivateKey)
	}
	if privateKeyPermissionsTooPermissive(info.Mode(), runtime.GOOS) {
		return nil, fmt.Errorf("%w: run chmod 600 %q", ErrInsecurePrivateKey, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read key: %w", ErrInvalidPrivateKey, err)
	}
	return ParsePrivateKey(data)
}

// ParsePrivateKey parses a PEM-encoded PKCS#1 or PKCS#8 RSA private key.
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: expected PEM data", ErrInvalidPrivateKey)
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: unexpected data after PEM block", ErrInvalidPrivateKey)
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: key is not RSA", ErrInvalidPrivateKey)
		}
		if err := rsaKey.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
		}
		return rsaKey, nil
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported RSA key encoding", ErrInvalidPrivateKey)
	}
	if err := rsaKey.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
	}
	return rsaKey, nil
}

func privateKeyPermissionsTooPermissive(mode fs.FileMode, goos string) bool {
	return goos != "windows" && mode.Perm()&0o077 != 0
}
