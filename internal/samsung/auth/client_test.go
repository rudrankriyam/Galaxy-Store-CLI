package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeSendsJWTAndDecodesAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/auth/accessToken" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer signed-jwt" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := request.Header.Get("service-account-id"); got != "" {
			t.Errorf("service-account-id = %q, want empty", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ok":true,"createdItem":{"accessToken":"access-token"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, server.Client())
	response, err := client.Exchange(context.Background(), "signed-jwt")
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if response.CreatedItem.AccessToken != "access-token" {
		t.Fatalf("access token = %q", response.CreatedItem.AccessToken)
	}
}

func TestCheckAndRevokeSendRequiredHeaders(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if got := request.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("service-account-id"); got != "service-account" {
			t.Errorf("service-account-id = %q", got)
		}
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, server.Client())
	check, err := client.Check(context.Background(), "service-account", "access-token")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !check.OK {
		t.Fatal("Check().OK = false")
	}
	revoke, err := client.Revoke(context.Background(), "service-account", "access-token")
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if !revoke.OK {
		t.Fatal("Revoke().OK = false")
	}

	want := []string{
		"GET /auth/checkAccessToken",
		"DELETE /auth/revokeAccessToken",
	}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestAPIErrorIsTypedAndRedactsSecrets(t *testing.T) {
	const (
		serviceAccountID = "secret-service-account"
		accessToken      = "secret-access-token"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{
			"code":"AUTH_REQUIRE",
			"message":"invalid secret-access-token for secret-service-account",
			"from":"asgw"
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, server.Client())
	_, err := client.Check(context.Background(), serviceAccountID, accessToken)
	if err == nil {
		t.Fatal("Check() error = nil")
	}
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiError.StatusCode != http.StatusUnauthorized || apiError.Code != "AUTH_REQUIRE" {
		t.Fatalf("API error = %#v", apiError)
	}
	for _, secret := range []string{serviceAccountID, accessToken} {
		if strings.Contains(err.Error(), secret) || strings.Contains(apiError.Message, secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestTransportErrorDoesNotRenderSecrets(t *testing.T) {
	const secret = "secret-access-token"
	client := newTestClient(t, "https://example.invalid", roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed using %s", request.Header.Get("Authorization"))
	}))

	_, err := client.Check(context.Background(), "service-account", secret)
	if err == nil {
		t.Fatal("Check() error = nil")
	}
	var requestError *RequestError
	if !errors.As(err, &requestError) {
		t.Fatalf("error type = %T, want *RequestError", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked access token: %v", err)
	}
}

func TestClientRejectsInvalidInputsBeforeRequest(t *testing.T) {
	client := newTestClient(t, "https://example.com", roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP request should not be sent")
		return nil, nil
	}))

	if _, err := client.Exchange(context.Background(), " "); err == nil {
		t.Fatal("Exchange() error = nil")
	}
	if _, err := client.Check(context.Background(), "", "token"); err == nil {
		t.Fatal("Check() missing service account error = nil")
	}
	if _, err := client.Revoke(context.Background(), "account", ""); err == nil {
		t.Fatal("Revoke() missing token error = nil")
	}
}

func TestClientRejectsMalformedSuccessResponse(t *testing.T) {
	tests := []string{
		``,
		`not json`,
		`{"ok":false}`,
		`{"ok":true,"createdItem":{"accessToken":""}}`,
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, body)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, server.Client())
			if _, err := client.Exchange(context.Background(), "signed-jwt"); err == nil {
				t.Fatal("Exchange() error = nil")
			}
		})
	}
}

func TestNewClientValidatesBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"relative",
		"https://user:pass@example.com",
		"https://example.com?query=value",
		"https://example.com#fragment",
	} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := NewClient(ClientOptions{BaseURL: baseURL}); err == nil {
				t.Fatal("NewClient() error = nil")
			}
		})
	}
}

func newTestClient(t *testing.T, baseURL string, httpClient HTTPDoer) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{BaseURL: baseURL, HTTPClient: httpClient})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}
