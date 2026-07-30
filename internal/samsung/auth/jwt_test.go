package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	testRSAKeyOnce sync.Once
	testRSAKey     *rsa.PrivateKey
	testRSAKeyErr  error
)

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testRSAKeyOnce.Do(func() {
		testRSAKey, testRSAKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testRSAKeyErr != nil {
		t.Fatalf("generate test RSA key: %v", testRSAKeyErr)
	}
	return testRSAKey
}

func pkcs1PEM(t *testing.T) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey(t)),
	})
}

func pkcs8PEM(t *testing.T) []byte {
	t.Helper()
	bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey(t))
	if err != nil {
		t.Fatalf("marshal PKCS#8 key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: bytes})
}

func TestSignJWTCreatesRequiredSamsungClaims(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 34, 56, 789, time.UTC)
	signed, err := SignJWT(JWTConfig{
		ServiceAccountID: "service-account-id",
		Scopes:           []Scope{ScopePublishing, ScopeGSS},
		PrivateKeyPEM:    pkcs1PEM(t),
		Lifetime:         15 * time.Minute,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}

	parsed, err := jwt.ParseWithClaims(signed, &Claims{}, func(token *jwt.Token) (any, error) {
		return &rsaKey(t).PublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}), jwt.WithTimeFunc(func() time.Time {
		return now
	}))
	if err != nil {
		t.Fatalf("parse signed JWT: %v", err)
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		t.Fatalf("claims type = %T, want *Claims", parsed.Claims)
	}
	if claims.Issuer != "service-account-id" {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
	if len(claims.Scopes) != 2 || claims.Scopes[0] != ScopePublishing || claims.Scopes[1] != ScopeGSS {
		t.Fatalf("scopes = %#v", claims.Scopes)
	}
	wantIssuedAt := now.Truncate(time.Second)
	if !claims.IssuedAt.Time.Equal(wantIssuedAt) {
		t.Fatalf("iat = %v, want %v", claims.IssuedAt.Time, wantIssuedAt)
	}
	if want := wantIssuedAt.Add(15 * time.Minute); !claims.ExpiresAt.Time.Equal(want) {
		t.Fatalf("exp = %v, want %v", claims.ExpiresAt.Time, want)
	}
	if parsed.Method.Alg() != "RS256" {
		t.Fatalf("alg = %q, want RS256", parsed.Method.Alg())
	}
}

func TestSignJWTUsesDefaultTenMinuteLifetime(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	signed, err := SignJWT(JWTConfig{
		ServiceAccountID: "service-account-id",
		Scopes:           []Scope{ScopePublishing},
		PrivateKeyPEM:    pkcs8PEM(t),
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}
	parsed, err := jwt.ParseWithClaims(signed, &Claims{}, func(*jwt.Token) (any, error) {
		return &rsaKey(t).PublicKey, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("parse signed JWT: %v", err)
	}
	claims := parsed.Claims.(*Claims)
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != DefaultJWTLifetime {
		t.Fatalf("lifetime = %v, want %v", got, DefaultJWTLifetime)
	}
}

func TestSignJWTRejectsInvalidInputsWithoutLeakingPrivateKey(t *testing.T) {
	privateKey := string(pkcs1PEM(t))
	tests := []struct {
		name   string
		config JWTConfig
	}{
		{name: "missing service account", config: JWTConfig{Scopes: []Scope{ScopePublishing}, PrivateKeyPEM: []byte(privateKey)}},
		{name: "missing scope", config: JWTConfig{ServiceAccountID: "account", PrivateKeyPEM: []byte(privateKey)}},
		{name: "unknown scope", config: JWTConfig{ServiceAccountID: "account", Scopes: []Scope{"admin"}, PrivateKeyPEM: []byte(privateKey)}},
		{name: "duplicate scope", config: JWTConfig{ServiceAccountID: "account", Scopes: []Scope{ScopeGSS, ScopeGSS}, PrivateKeyPEM: []byte(privateKey)}},
		{name: "negative lifetime", config: JWTConfig{ServiceAccountID: "account", Scopes: []Scope{ScopeGSS}, PrivateKeyPEM: []byte(privateKey), Lifetime: -time.Second}},
		{name: "over max lifetime", config: JWTConfig{ServiceAccountID: "account", Scopes: []Scope{ScopeGSS}, PrivateKeyPEM: []byte(privateKey), Lifetime: MaxJWTLifetime + time.Second}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := SignJWT(test.config)
			if err == nil {
				t.Fatal("SignJWT() error = nil")
			}
			if strings.Contains(err.Error(), privateKey) || strings.Contains(err.Error(), "BEGIN RSA PRIVATE KEY") {
				t.Fatalf("error leaked private key: %v", err)
			}
		})
	}
}

func TestParsePrivateKeySupportsPKCS1AndPKCS8(t *testing.T) {
	for _, privateKeyPEM := range [][]byte{pkcs1PEM(t), pkcs8PEM(t)} {
		parsed, err := ParsePrivateKey(privateKeyPEM)
		if err != nil {
			t.Fatalf("ParsePrivateKey() error = %v", err)
		}
		if parsed.N.Cmp(rsaKey(t).N) != 0 {
			t.Fatal("parsed key does not match input")
		}
	}
}

func TestParsePrivateKeyRejectsNonRSAAndTrailingData(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
	if err != nil {
		t.Fatalf("marshal ECDSA key: %v", err)
	}
	nonRSA := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := ParsePrivateKey(nonRSA); err == nil || !strings.Contains(err.Error(), "not RSA") {
		t.Fatalf("non-RSA error = %v", err)
	}

	withTrailingData := append(pkcs1PEM(t), []byte("unexpected")...)
	if _, err := ParsePrivateKey(withTrailingData); err == nil || !strings.Contains(err.Error(), "unexpected data") {
		t.Fatalf("trailing-data error = %v", err)
	}
}
