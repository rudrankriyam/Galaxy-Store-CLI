package samsung

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDoReturnsTypedAPIErrorForSamsungEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "content publish",
			body:        `{"errorCode":"4002","errorMsg":"Invalid content ID"}`,
			wantCode:    "4002",
			wantMessage: "Invalid content ID",
		},
		{
			name:        "nested IAP error",
			body:        `{"error":{"code":"IAP-403","message":"Not authorized"}}`,
			wantCode:    "IAP-403",
			wantMessage: "Not authorized",
		},
		{
			name:        "errors collection",
			body:        `{"errors":[{"error_code":"GSS-429","description":"Quota exceeded"}]}`,
			wantCode:    "GSS-429",
			wantMessage: "Quota exceeded",
		},
		{
			name:        "orders response fields",
			body:        `{"responseCode":"5001","responseMessage":"Order was not found"}`,
			wantCode:    "5001",
			wantMessage: "Order was not found",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(
					request,
					http.StatusBadRequest,
					test.body,
					http.Header{"X-Request-Id": []string{"request-123"}},
				), nil
			})
			client, err := NewClient(&http.Client{Transport: transport}, staticToken("token"), "account")
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			_, err = client.DoJSON(t.Context(), http.MethodPost, "/seller/v2/content", nil, nil)
			var apiError *APIError
			if !errors.As(err, &apiError) {
				t.Fatalf("error = %T %v, want *APIError", err, err)
			}
			if apiError.StatusCode != http.StatusBadRequest {
				t.Fatalf("StatusCode = %d", apiError.StatusCode)
			}
			if apiError.Code != test.wantCode {
				t.Fatalf("Code = %q, want %q", apiError.Code, test.wantCode)
			}
			if apiError.Message != test.wantMessage {
				t.Fatalf("Message = %q, want %q", apiError.Message, test.wantMessage)
			}
			if apiError.RequestID != "request-123" {
				t.Fatalf("RequestID = %q", apiError.RequestID)
			}
		})
	}
}

func TestAPIErrorFallbackDoesNotExposeResponseBody(t *testing.T) {
	t.Parallel()

	const secretBody = "not-json private-key-material"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusInternalServerError, secretBody, nil), nil
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("token"),
		"account",
		WithMaxRetries(0),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.DoJSON(t.Context(), http.MethodGet, "/seller/v2/content", nil, nil)
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), secretBody) {
		t.Fatalf("error exposed raw response body: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "HTTP 500") {
		t.Fatalf("error = %q, want HTTP status", got)
	}
}

func TestAPIErrorRedactsAuthenticationValuesEchoedByServer(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(
			request,
			http.StatusUnauthorized,
			`{"errorCode":"AUTH-access-secret","errorMessage":"Bearer access-secret account-secret rejected"}`,
			http.Header{"X-Request-Id": []string{"account-secret"}},
		), nil
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("access-secret"),
		"account-secret",
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.DoJSON(t.Context(), http.MethodGet, "/seller/v2/content", nil, nil)
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), "access-secret") || strings.Contains(err.Error(), "account-secret") {
		t.Fatalf("error leaked authentication values: %v", err)
	}
}

func TestAPIErrorSanitizesControlCharacters(t *testing.T) {
	t.Parallel()

	apiError := &APIError{
		StatusCode: http.StatusBadRequest,
		Code:       safeErrorText("BAD\nCODE", 128),
		Message:    safeErrorText("line one\r\nline two", 2048),
	}
	if strings.ContainsAny(apiError.Error(), "\r\n") {
		t.Fatalf("error contains control characters: %q", apiError.Error())
	}
}
