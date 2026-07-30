// Package session resolves an existing Galaxy Store access token and constructs
// an authenticated API client. It never signs a JWT or mints a token.
package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
)

const loginGuidance = `run "gsc auth login" to create and securely store an access token`

// CredentialResolver resolves an environment credential pair or stored profile.
type CredentialResolver func(credentials.Options) (credentials.Credentials, error)

// Factory constructs authenticated Galaxy Store API sessions.
type Factory struct {
	httpClient *http.Client
	resolve    CredentialResolver
}

// Session contains a ready API client and non-secret credential metadata.
type Session struct {
	Client           *samsung.Client
	Profile          string
	ServiceAccountID string
	Scopes           []string
}

// NewFactory creates a session factory. A nil HTTP client uses Samsung's
// otherwise-default client.
func NewFactory(httpClient *http.Client, resolver CredentialResolver) (*Factory, error) {
	if resolver == nil {
		return nil, errors.New("credential resolver is required")
	}
	return &Factory{
		httpClient: httpClient,
		resolve:    resolver,
	}, nil
}

// DefaultFactory creates the production session factory.
func DefaultFactory() (*Factory, error) {
	return NewFactory(nil, credentials.Resolve)
}

// Open resolves an existing access token for profile and creates an
// authenticated Samsung client. A service-account private key is deliberately
// insufficient here: callers must run gsc auth login explicitly.
func (factory *Factory) Open(profile string) (*Session, error) {
	if factory == nil || factory.resolve == nil {
		return nil, errors.New("session factory is not configured")
	}

	resolved, err := factory.resolve(credentials.Options{
		Profile: strings.TrimSpace(profile),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve Galaxy Store access token: %w; %s", err, loginGuidance)
	}
	if resolved.Kind != credentials.KindAccessToken ||
		strings.TrimSpace(resolved.AccessToken) == "" {
		return nil, fmt.Errorf("no stored Galaxy Store access token is available; %s", loginGuidance)
	}
	if strings.TrimSpace(resolved.ServiceAccountID) == "" {
		return nil, fmt.Errorf("resolved Galaxy Store credentials have no service account ID; %s", loginGuidance)
	}

	accessToken := strings.TrimSpace(resolved.AccessToken)
	provider := samsung.TokenProviderFunc(func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			return accessToken, nil
		}
	})
	client, err := samsung.NewClient(
		factory.httpClient,
		provider,
		resolved.ServiceAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("create authenticated Galaxy Store client: %w", err)
	}
	return &Session{
		Client:           client,
		Profile:          resolved.Profile,
		ServiceAccountID: strings.TrimSpace(resolved.ServiceAccountID),
		Scopes:           append([]string(nil), resolved.Scopes...),
	}, nil
}
