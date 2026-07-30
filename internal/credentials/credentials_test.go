package credentials

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
)

type memoryTokenStore struct {
	tokens map[string]string
	err    error
}

func (s *memoryTokenStore) Get(profile string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	token, ok := s.tokens[profile]
	if !ok {
		return "", ErrTokenNotFound
	}
	return token, nil
}

func (s *memoryTokenStore) Set(profile string, token string) error {
	if s.tokens == nil {
		s.tokens = make(map[string]string)
	}
	s.tokens[profile] = token
	return nil
}

func (s *memoryTokenStore) Delete(profile string) error {
	delete(s.tokens, profile)
	return nil
}

func clearCredentialEnv(t *testing.T) {
	t.Helper()
	t.Setenv(accessTokenEnv, "")
	t.Setenv(serviceAccountIDEnv, "")
	t.Setenv(profileEnv, "")
}

func TestExplicitTokenRequiresExplicitServiceAccount(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(serviceAccountIDEnv, "environment-account")

	_, err := ResolveFromConfig(
		Options{AccessToken: "explicit-token"},
		&config.Config{Profiles: map[string]config.Profile{}},
	)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("ResolveFromConfig() error = %v, want ErrIncomplete", err)
	}
}

func TestExplicitCredentialsDoNotMixWithEnvironmentOrConfig(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(accessTokenEnv, "environment-token")
	t.Setenv(serviceAccountIDEnv, "environment-account")

	got, err := ResolveFromConfig(
		Options{AccessToken: "explicit-token", ServiceAccountID: "explicit-account"},
		nil,
	)
	if err != nil {
		t.Fatalf("ResolveFromConfig() error = %v", err)
	}
	want := Credentials{
		Kind:             KindAccessToken,
		AccessToken:      "explicit-token",
		ServiceAccountID: "explicit-account",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveFromConfig() = %#v, want %#v", got, want)
	}
}

func TestPartialEnvironmentCredentialPairFailsBeforeProfile(t *testing.T) {
	tests := []struct {
		name             string
		accessToken      string
		serviceAccountID string
	}{
		{name: "token only", accessToken: "token"},
		{name: "service account only", serviceAccountID: "account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredentialEnv(t)
			t.Setenv(accessTokenEnv, tt.accessToken)
			t.Setenv(serviceAccountIDEnv, tt.serviceAccountID)

			cfg := &config.Config{
				DefaultProfile: "default",
				Profiles: map[string]config.Profile{
					"default": {ServiceAccountID: "profile-account", PrivateKeyPath: "/key.pem"},
				},
			}
			_, err := ResolveFromConfig(Options{}, cfg)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("ResolveFromConfig() error = %v, want ErrIncomplete", err)
			}
		})
	}
}

func TestCompleteEnvironmentCredentialPairDoesNotBorrowProfile(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(accessTokenEnv, "environment-token")
	t.Setenv(serviceAccountIDEnv, "environment-account")
	t.Setenv(profileEnv, "must-be-ignored")

	got, err := ResolveFromConfig(Options{}, nil)
	if err != nil {
		t.Fatalf("ResolveFromConfig() error = %v", err)
	}
	want := Credentials{
		Kind:             KindAccessToken,
		AccessToken:      "environment-token",
		ServiceAccountID: "environment-account",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveFromConfig() = %#v, want %#v", got, want)
	}
}

func TestExplicitProfileWinsOverEnvironmentCredentials(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(accessTokenEnv, "environment-token")
	t.Setenv(serviceAccountIDEnv, "environment-account")

	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {ServiceAccountID: "production-account", PrivateKeyPath: "/production.pem"},
			"staging":    {ServiceAccountID: "staging-account", PrivateKeyPath: "/staging.pem"},
		},
	}
	got, err := ResolveFromConfigWithStore(Options{Profile: "staging"}, cfg, nil)
	if err != nil {
		t.Fatalf("ResolveFromConfigWithStore() error = %v", err)
	}
	if got.Profile != "staging" || got.ServiceAccountID != "staging-account" || got.PrivateKeyPath != "/staging.pem" {
		t.Fatalf("ResolveFromConfigWithStore() = %#v", got)
	}
}

func TestExplicitUnknownProfileDoesNotFallThrough(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(accessTokenEnv, "environment-token")
	t.Setenv(serviceAccountIDEnv, "environment-account")

	cfg := &config.Config{
		DefaultProfile: "default",
		Profiles: map[string]config.Profile{
			"default": {ServiceAccountID: "account", PrivateKeyPath: "/key.pem"},
		},
	}
	_, err := ResolveFromConfigWithStore(Options{Profile: "missing"}, cfg, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveFromConfigWithStore() error = %v, want ErrNotFound", err)
	}
}

func TestProfilePrefersKeychainToken(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {
				ServiceAccountID: "service-account",
				PrivateKeyPath:   "/fallback.pem",
				Scopes:           []string{"publishing"},
			},
		},
	}
	store := &memoryTokenStore{tokens: map[string]string{"production": "keychain-token"}}

	got, err := ResolveFromConfigWithStore(Options{}, cfg, store)
	if err != nil {
		t.Fatalf("ResolveFromConfigWithStore() error = %v", err)
	}
	want := Credentials{
		Kind:             KindAccessToken,
		Profile:          "production",
		AccessToken:      "keychain-token",
		ServiceAccountID: "service-account",
		Scopes:           []string{"publishing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveFromConfigWithStore() = %#v, want %#v", got, want)
	}
}

func TestProfileFallsBackToPrivateKeyWhenTokenMissing(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {ServiceAccountID: "account", PrivateKeyPath: "/key.pem"},
		},
	}
	store := &memoryTokenStore{tokens: map[string]string{}}

	got, err := ResolveFromConfigWithStore(Options{}, cfg, store)
	if err != nil {
		t.Fatalf("ResolveFromConfigWithStore() error = %v", err)
	}
	if got.Kind != KindServiceAccount || got.PrivateKeyPath != "/key.pem" {
		t.Fatalf("ResolveFromConfigWithStore() = %#v", got)
	}
}

func TestKeychainOnlyProfileRequiresStoredToken(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {ServiceAccountID: "account"},
		},
	}
	_, err := ResolveFromConfigWithStore(Options{}, cfg, &memoryTokenStore{})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("ResolveFromConfigWithStore() error = %v, want ErrIncomplete", err)
	}
}

func TestProfileKeychainErrorDoesNotFallBack(t *testing.T) {
	clearCredentialEnv(t)
	storeFailure := errors.New("keychain denied")
	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {ServiceAccountID: "account", PrivateKeyPath: "/key.pem"},
		},
	}
	_, err := ResolveFromConfigWithStore(Options{}, cfg, &memoryTokenStore{err: storeFailure})
	if !errors.Is(err, storeFailure) {
		t.Fatalf("ResolveFromConfigWithStore() error = %v, want keychain error", err)
	}
}

func TestResolveFromConfigUsesOnlyProfileWhenUnambiguous(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		Profiles: map[string]config.Profile{
			"only": {ServiceAccountID: "account", PrivateKeyPath: "/key.pem"},
		},
	}

	got, err := ResolveFromConfig(Options{}, cfg)
	if err != nil {
		t.Fatalf("ResolveFromConfig() error = %v", err)
	}
	if got.Profile != "only" {
		t.Fatalf("profile = %q, want only", got.Profile)
	}
}

func TestResolveFromConfigRequiresProfileWhenAmbiguous(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		Profiles: map[string]config.Profile{
			"one": {ServiceAccountID: "one", PrivateKeyPath: "/one.pem"},
			"two": {ServiceAccountID: "two", PrivateKeyPath: "/two.pem"},
		},
	}
	_, err := ResolveFromConfig(Options{}, cfg)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveFromConfig() error = %v, want ErrNotFound", err)
	}
}

func TestResolveFromConfigRejectsUnknownProfile(t *testing.T) {
	clearCredentialEnv(t)
	cfg := &config.Config{
		Profiles: map[string]config.Profile{
			"default": {ServiceAccountID: "account", PrivateKeyPath: "/key.pem"},
		},
	}
	_, err := ResolveFromConfig(Options{Profile: "missing"}, cfg)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveFromConfig() error = %v, want ErrNotFound", err)
	}
}

func TestParsePrivateKeySupportsPKCS1AndPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	for _, tt := range []struct {
		name string
		data []byte
	}{
		{name: "PKCS1", data: pkcs1},
		{name: "PKCS8", data: pkcs8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePrivateKey(tt.data)
			if err != nil {
				t.Fatalf("ParsePrivateKey() error = %v", err)
			}
			if got.N.Cmp(key.N) != 0 {
				t.Fatal("parsed key does not match generated key")
			}
		})
	}
}

func TestParsePrivateKeyRejectsNonPEM(t *testing.T) {
	_, err := ParsePrivateKey([]byte("secret"))
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("ParsePrivateKey() error = %v, want ErrInvalidPrivateKey", err)
	}
}

func TestLoadPrivateKeyRejectsPermissiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not enforced on Windows")
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = LoadPrivateKey(path)
	if !errors.Is(err, ErrInsecurePrivateKey) {
		t.Fatalf("LoadPrivateKey() error = %v, want ErrInsecurePrivateKey", err)
	}
}

func TestLoadPrivateKeyReadsPrivateFile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey() error = %v", err)
	}
	if got.N.Cmp(key.N) != 0 {
		t.Fatal("loaded key does not match generated key")
	}
}
