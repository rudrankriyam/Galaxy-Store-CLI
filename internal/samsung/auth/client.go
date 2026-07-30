package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the Galaxy Store Developer API host.
	DefaultBaseURL = "https://devapi.samsungapps.com"

	maxResponseSize = 1 << 20
	defaultTimeout  = 60 * time.Second
)

// HTTPDoer is implemented by *http.Client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ClientOptions configures a Galaxy Store authentication client.
type ClientOptions struct {
	BaseURL    string
	HTTPClient HTTPDoer
}

// Client calls Samsung's access-token endpoints.
type Client struct {
	baseURL    string
	httpClient HTTPDoer
}

// AccessTokenResponse is returned after a successful JWT exchange.
type AccessTokenResponse struct {
	OK          bool                   `json:"ok"`
	CreatedItem CreatedAccessTokenItem `json:"createdItem"`
}

// CreatedAccessTokenItem contains the non-expiring Galaxy Store access token.
type CreatedAccessTokenItem struct {
	AccessToken string `json:"accessToken"`
}

// TokenStatusResponse is returned when checking or revoking an access token.
type TokenStatusResponse struct {
	OK bool `json:"ok"`
}

// NewClient creates a Galaxy Store authentication client.
func NewClient(options ClientOptions) (*Client, error) {
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("auth base URL must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("auth base URL must not include credentials, a query, or a fragment")
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}, nil
}

// Exchange trades a signed service-account JWT for a Galaxy Store access token.
func (client *Client) Exchange(ctx context.Context, signedJWT string) (*AccessTokenResponse, error) {
	signedJWT = strings.TrimSpace(signedJWT)
	if signedJWT == "" {
		return nil, errors.New("signed JWT is required")
	}

	request, err := client.newRequest(ctx, http.MethodPost, "/auth/accessToken", signedJWT, "")
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	var response AccessTokenResponse
	if err := client.doJSON(request, "exchange access token", []string{signedJWT}, &response); err != nil {
		return nil, err
	}
	if !response.OK || strings.TrimSpace(response.CreatedItem.AccessToken) == "" {
		return nil, errors.New("exchange access token: Samsung returned an invalid success response")
	}
	return &response, nil
}

// Check verifies that an access token is valid for a service account.
func (client *Client) Check(ctx context.Context, serviceAccountID string, accessToken string) (*TokenStatusResponse, error) {
	return client.tokenStatusRequest(ctx, http.MethodGet, "/auth/checkAccessToken", "check access token", serviceAccountID, accessToken)
}

// Revoke permanently revokes an access token.
func (client *Client) Revoke(ctx context.Context, serviceAccountID string, accessToken string) (*TokenStatusResponse, error) {
	return client.tokenStatusRequest(ctx, http.MethodDelete, "/auth/revokeAccessToken", "revoke access token", serviceAccountID, accessToken)
}

func (client *Client) tokenStatusRequest(
	ctx context.Context,
	method string,
	path string,
	operation string,
	serviceAccountID string,
	accessToken string,
) (*TokenStatusResponse, error) {
	serviceAccountID = strings.TrimSpace(serviceAccountID)
	accessToken = strings.TrimSpace(accessToken)
	if serviceAccountID == "" {
		return nil, errors.New("service account ID is required")
	}
	if accessToken == "" {
		return nil, errors.New("access token is required")
	}

	request, err := client.newRequest(ctx, method, path, accessToken, serviceAccountID)
	if err != nil {
		return nil, err
	}

	var response TokenStatusResponse
	if err := client.doJSON(request, operation, []string{accessToken, serviceAccountID}, &response); err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, fmt.Errorf("%s: Samsung returned an invalid success response", operation)
	}
	return &response, nil
}

func (client *Client) newRequest(
	ctx context.Context,
	method string,
	path string,
	bearerToken string,
	serviceAccountID string,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(nil))
	if err != nil {
		return nil, errors.New("build authentication request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearerToken)
	if serviceAccountID != "" {
		request.Header.Set("service-account-id", serviceAccountID)
	}
	return request, nil
}

func (client *Client) doJSON(request *http.Request, operation string, secrets []string, destination any) error {
	response, err := client.httpClient.Do(request)
	if err != nil {
		return &RequestError{Operation: operation, Err: err}
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return &RequestError{Operation: operation, Err: err}
	}
	if len(body) > maxResponseSize {
		return fmt.Errorf("%s: response exceeds %d bytes", operation, maxResponseSize)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(response.StatusCode, body, secrets)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("%s: Samsung returned an empty success response", operation)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("%s: Samsung returned invalid JSON", operation)
	}
	return nil
}
