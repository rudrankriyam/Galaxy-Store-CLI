// Package auth implements Galaxy Store Developer API authentication.
package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// DefaultJWTLifetime is used when JWTConfig.Lifetime is zero.
	DefaultJWTLifetime = 10 * time.Minute
	// MaxJWTLifetime is the longest lifetime accepted by Samsung.
	MaxJWTLifetime = 20 * time.Minute
)

// Scope is a Galaxy Store service-account scope.
type Scope string

const (
	// ScopePublishing grants access to Content Publish and IAP APIs.
	ScopePublishing Scope = "publishing"
	// ScopeGSS grants access to Galaxy Store Statistics APIs.
	ScopeGSS Scope = "gss"
)

// JWTConfig contains the inputs needed to sign a Samsung authentication JWT.
type JWTConfig struct {
	ServiceAccountID string
	Scopes           []Scope
	PrivateKeyPEM    []byte
	Lifetime         time.Duration
	Now              func() time.Time
}

// Claims are the registered claims required by Galaxy Store.
type Claims struct {
	Scopes []Scope `json:"scopes"`
	jwt.RegisteredClaims
}

// SignJWT creates an RS256 JWT for exchange with Samsung's access-token API.
func SignJWT(config JWTConfig) (string, error) {
	serviceAccountID := strings.TrimSpace(config.ServiceAccountID)
	if serviceAccountID == "" {
		return "", errors.New("service account ID is required")
	}

	scopes, err := validateScopes(config.Scopes)
	if err != nil {
		return "", err
	}

	lifetime := config.Lifetime
	if lifetime == 0 {
		lifetime = DefaultJWTLifetime
	}
	if lifetime <= 0 {
		return "", errors.New("JWT lifetime must be greater than zero")
	}
	if lifetime > MaxJWTLifetime {
		return "", fmt.Errorf("JWT lifetime must not exceed %s", MaxJWTLifetime)
	}

	privateKey, err := ParsePrivateKey(config.PrivateKeyPEM)
	if err != nil {
		return "", err
	}

	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	issuedAt := now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(lifetime)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{
		Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    serviceAccountID,
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	})

	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", errors.New("sign JWT: signing failed")
	}
	return signed, nil
}

// ParsePrivateKey parses an unencrypted PKCS#1 or PKCS#8 PEM-encoded RSA key.
func ParsePrivateKey(privateKeyPEM []byte) (*rsa.PrivateKey, error) {
	if len(strings.TrimSpace(string(privateKeyPEM))) == 0 {
		return nil, errors.New("private key is required")
	}

	block, rest := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, errors.New("parse private key: expected PEM-encoded PKCS#1 or PKCS#8 key")
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("parse private key: unexpected data after PEM block")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("parse private key: invalid PKCS#1 RSA key")
		}
		return privateKey, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("parse private key: invalid PKCS#8 key")
		}
		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("parse private key: PKCS#8 key is not RSA")
		}
		return privateKey, nil
	default:
		return nil, fmt.Errorf("parse private key: unsupported PEM block type %q", block.Type)
	}
}

func validateScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return nil, errors.New("at least one scope is required")
	}

	validated := make([]Scope, 0, len(scopes))
	seen := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		switch scope {
		case ScopePublishing, ScopeGSS:
		default:
			return nil, fmt.Errorf("unsupported scope %q; expected %q or %q", scope, ScopePublishing, ScopeGSS)
		}
		if _, exists := seen[scope]; exists {
			return nil, fmt.Errorf("scope %q must not be repeated", scope)
		}
		seen[scope] = struct{}{}
		validated = append(validated, scope)
	}
	return validated, nil
}
