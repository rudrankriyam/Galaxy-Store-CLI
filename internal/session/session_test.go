package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestOpenBuildsAuthenticatedClientFromExistingAccessToken(t *testing.T) {
	t.Parallel()

	var resolvedOptions credentials.Options
	var request *http.Request
	factory, err := NewFactory(
		&http.Client{Transport: roundTripFunc(func(value *http.Request) (*http.Response, error) {
			request = value
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    value,
			}, nil
		})},
		func(options credentials.Options) (credentials.Credentials, error) {
			resolvedOptions = options
			return credentials.Credentials{
				Kind:             credentials.KindAccessToken,
				Profile:          "production",
				AccessToken:      "secret-access-token",
				ServiceAccountID: "service-account",
				Scopes:           []string{"publishing"},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	active, err := factory.Open(" production ")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if resolvedOptions.Profile != "production" {
		t.Fatalf("resolved profile = %q", resolvedOptions.Profile)
	}
	if active.Profile != "production" ||
		active.ServiceAccountID != "service-account" ||
		len(active.Scopes) != 1 {
		t.Fatalf("session metadata = %#v", active)
	}

	var response map[string]bool
	if _, err := active.Client.DoJSON(
		context.Background(),
		http.MethodGet,
		"/seller/contentList",
		nil,
		&response,
	); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if request == nil {
		t.Fatal("authenticated client sent no request")
	}
	if got, want := request.Header.Get("Authorization"), "Bearer secret-access-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("service-account-id"), "service-account"; got != want {
		t.Fatalf("service-account-id = %q, want %q", got, want)
	}
}

func TestOpenNeverMintsTokenFromServiceAccountProfile(t *testing.T) {
	t.Parallel()

	factory, err := NewFactory(nil, func(credentials.Options) (credentials.Credentials, error) {
		return credentials.Credentials{
			Kind:             credentials.KindServiceAccount,
			Profile:          "production",
			ServiceAccountID: "service-account",
			PrivateKeyPath:   "/private/seller.pem",
		}, nil
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	_, err = factory.Open("production")
	if err == nil || !strings.Contains(err.Error(), `gsc auth login`) {
		t.Fatalf("Open error = %v, want login guidance", err)
	}
	if strings.Contains(err.Error(), "/private/seller.pem") {
		t.Fatalf("Open error exposed private key path: %v", err)
	}
}

func TestOpenGuidesLoginForResolutionAndIncompleteTokenFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved credentials.Credentials
		err      error
	}{
		{name: "resolution error", err: credentials.ErrNotFound},
		{
			name: "empty token",
			resolved: credentials.Credentials{
				Kind:             credentials.KindAccessToken,
				ServiceAccountID: "service-account",
			},
		},
		{
			name: "empty service account",
			resolved: credentials.Credentials{
				Kind:        credentials.KindAccessToken,
				AccessToken: "secret",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			factory, err := NewFactory(nil, func(credentials.Options) (credentials.Credentials, error) {
				return test.resolved, test.err
			})
			if err != nil {
				t.Fatalf("NewFactory: %v", err)
			}
			_, err = factory.Open("")
			if err == nil || !strings.Contains(err.Error(), `gsc auth login`) {
				t.Fatalf("Open error = %v, want login guidance", err)
			}
		})
	}
}

func TestFactoryValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewFactory(nil, nil); err == nil {
		t.Fatal("expected nil resolver to fail")
	}
	var factory *Factory
	if _, err := factory.Open(""); err == nil {
		t.Fatal("expected nil factory to fail")
	}
}

func TestSessionTokenProviderHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	var transportCalls int
	factory, err := NewFactory(
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			transportCalls++
			return nil, errors.New("must not send")
		})},
		func(credentials.Options) (credentials.Credentials, error) {
			return credentials.Credentials{
				Kind:             credentials.KindAccessToken,
				AccessToken:      "secret",
				ServiceAccountID: "service-account",
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	active, err := factory.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := active.Client.NewRequest(ctx, http.MethodGet, "/seller/contentList", nil); err == nil {
		t.Fatal("NewRequest error = nil, want canceled token lookup failure")
	}
	if transportCalls != 0 {
		t.Fatalf("transport calls = %d, want 0", transportCalls)
	}
}
