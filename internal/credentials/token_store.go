package credentials

import (
	"errors"
	"fmt"
	"strings"

	"github.com/99designs/keyring"
)

const (
	keyringService    = "gsc"
	keyringItemLabel  = "Galaxy Store CLI access token"
	keyringItemPrefix = "gsc:access-token:"
)

// ErrTokenNotFound indicates that a profile has no access token in secure
// storage.
var ErrTokenNotFound = errors.New("access token not found")

// TokenStore persists Galaxy Store access tokens outside config.json.
type TokenStore interface {
	Get(profile string) (string, error)
	Set(profile string, accessToken string) error
	Delete(profile string) error
}

// KeyringTokenStore stores access tokens in the operating system credential
// manager through 99designs/keyring.
type KeyringTokenStore struct {
	keyring keyring.Keyring
}

// NewKeyringTokenStore opens an OS-native secure credential backend. It does
// not permit plaintext file or pass-command backends.
func NewKeyringTokenStore() (*KeyringTokenStore, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName:                    keyringService,
		KeychainTrustApplication:       true,
		KeychainSynchronizable:         false,
		KeychainAccessibleWhenUnlocked: true,
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,
			keyring.WinCredBackend,
			keyring.SecretServiceBackend,
			keyring.KWalletBackend,
			keyring.KeyCtlBackend,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open OS keychain: %w", err)
	}
	return newKeyringTokenStore(ring), nil
}

func newKeyringTokenStore(ring keyring.Keyring) *KeyringTokenStore {
	return &KeyringTokenStore{keyring: ring}
}

// Get loads the access token for profile.
func (s *KeyringTokenStore) Get(profile string) (string, error) {
	key, err := tokenKey(profile)
	if err != nil {
		return "", err
	}
	if s == nil || s.keyring == nil {
		return "", fmt.Errorf("OS keychain is not initialized")
	}
	item, err := s.keyring.Get(key)
	if err != nil {
		if errors.Is(err, keyring.ErrKeyNotFound) {
			return "", ErrTokenNotFound
		}
		return "", fmt.Errorf("read access token from OS keychain: %w", err)
	}
	token := strings.TrimSpace(string(item.Data))
	if token == "" {
		return "", fmt.Errorf("%w: stored token for profile %q is empty", ErrIncomplete, profile)
	}
	return token, nil
}

// Set stores the access token for profile.
func (s *KeyringTokenStore) Set(profile string, accessToken string) error {
	key, err := tokenKey(profile)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return fmt.Errorf("%w: access token is empty", ErrIncomplete)
	}
	if s == nil || s.keyring == nil {
		return fmt.Errorf("OS keychain is not initialized")
	}
	if err := s.keyring.Set(keyring.Item{
		Key:         key,
		Data:        []byte(token),
		Label:       keyringItemLabel,
		Description: "Galaxy Store Developer API access token for gsc profile " + profile,
	}); err != nil {
		return fmt.Errorf("store access token in OS keychain: %w", err)
	}
	return nil
}

// Delete removes the access token for profile. A missing token is a successful
// no-op, which makes logout and credential replacement idempotent.
func (s *KeyringTokenStore) Delete(profile string) error {
	key, err := tokenKey(profile)
	if err != nil {
		return err
	}
	if s == nil || s.keyring == nil {
		return fmt.Errorf("OS keychain is not initialized")
	}
	if err := s.keyring.Remove(key); err != nil && !errors.Is(err, keyring.ErrKeyNotFound) {
		return fmt.Errorf("delete access token from OS keychain: %w", err)
	}
	return nil
}

func tokenKey(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", fmt.Errorf("%w: profile is required", ErrIncomplete)
	}
	if strings.ContainsAny(profile, `/\`) || strings.ContainsFunc(profile, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) {
		return "", fmt.Errorf("%w: invalid profile %q", ErrIncomplete, profile)
	}
	return keyringItemPrefix + profile, nil
}

func keyringUnavailable(err error) bool {
	return errors.Is(err, keyring.ErrNoAvailImpl)
}
