package samsung

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func staticToken(token string) TokenProvider {
	return TokenProviderFunc(func(context.Context) (string, error) {
		return token, nil
	})
}

func TestNewClientValidatesConfigurationWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(nil, nil, "account"); err == nil {
		t.Fatal("expected missing token provider to fail")
	}
	if _, err := NewClient(nil, staticToken("secret"), " "); err == nil {
		t.Fatal("expected missing service account ID to fail")
	}
	if _, err := NewClient(nil, staticToken("secret"), "account", WithRequestTimeout(0)); err == nil {
		t.Fatal("expected invalid timeout to fail")
	}
	if _, err := NewClient(nil, staticToken("secret"), "account", WithMaxRetries(-1)); err == nil {
		t.Fatal("expected invalid retry count to fail")
	}

	provider := TokenProviderFunc(func(context.Context) (string, error) {
		return "", errors.New("provider failed with top-secret-token")
	})
	client, err := NewClient(nil, provider, "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.NewRequest(context.Background(), http.MethodGet, "/seller/v2/content", nil)
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if strings.Contains(err.Error(), "top-secret-token") {
		t.Fatalf("error leaked provider details: %v", err)
	}
}

func TestNewRequestUsesFixedHostAndBothAuthenticationHeaders(t *testing.T) {
	t.Parallel()

	client, err := NewClient(nil, staticToken("access-token"), " service-account ")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	request, err := client.NewRequest(
		context.Background(),
		http.MethodPost,
		"/seller/v2/content?contentId=example",
		map[string]string{"status": "FOR_SALE"},
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if got, want := request.URL.String(), DeveloperAPIBaseURL+"/seller/v2/content?contentId=example"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("Authorization"), "Bearer access-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := request.Header.Get(ServiceAccountIDHeader), "service-account"; got != want {
		t.Fatalf("%s = %q, want %q", ServiceAccountIDHeader, got, want)
	}
	if got, want := request.Header.Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	body, readErr := io.ReadAll(request.Body)
	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}
	if got, want := string(body), `{"status":"FOR_SALE"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestNewRequestRejectsTargetsOutsideFixedAllowlist(t *testing.T) {
	t.Parallel()

	client, err := NewClient(nil, staticToken("access-token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	for _, endpoint := range []string{
		"https://example.com/seller/v2/content",
		"//example.com/seller/v2/content",
		"seller/v2/content",
		"/seller/v2/content#fragment",
	} {
		endpoint := endpoint
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			if _, requestErr := client.NewRequest(context.Background(), http.MethodGet, endpoint, nil); requestErr == nil {
				t.Fatalf("expected endpoint %q to fail", endpoint)
			}
		})
	}

	request, requestErr := http.NewRequest(http.MethodGet, "https://example.com/path", nil)
	if requestErr != nil {
		t.Fatalf("build external request: %v", requestErr)
	}
	if _, requestErr = client.Do(request, nil); requestErr == nil {
		t.Fatal("expected Do to reject external host")
	}
}

func TestNewUploadRequestUsesOnlyDedicatedUploadEndpoint(t *testing.T) {
	t.Parallel()

	client, err := NewClient(nil, staticToken("access-token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	request, err := client.NewUploadRequest(
		context.Background(),
		strings.NewReader("multipart-body"),
		"multipart/form-data; boundary=boundary",
	)
	if err != nil {
		t.Fatalf("NewUploadRequest: %v", err)
	}
	if got := request.URL.String(); got != FileUploadURL {
		t.Fatalf("URL = %q, want %q", got, FileUploadURL)
	}
	if request.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", request.Method)
	}
	if request.Header.Get("Authorization") != "Bearer access-token" {
		t.Fatal("missing bearer authorization")
	}
	if request.Header.Get(ServiceAccountIDHeader) != "account" {
		t.Fatal("missing service account header")
	}

	if _, err := client.NewUploadRequest(context.Background(), nil, "multipart/form-data"); err == nil {
		t.Fatal("expected nil upload body to fail")
	}
	if _, err := client.NewUploadRequest(context.Background(), strings.NewReader("body"), " "); err == nil {
		t.Fatal("expected empty content type to fail")
	}
}

func TestDoDecodesJSONAndReturnsResponseMetadata(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatal("request lost Authorization header")
		}
		return response(request, http.StatusOK, `{"contentId":"abc","status":"SALE"}`, nil), nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, staticToken("access-token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var result struct {
		ContentID string `json:"contentId"`
		Status    string `json:"status"`
	}
	httpResponse, err := client.DoJSON(
		context.Background(),
		http.MethodGet,
		"/seller/v2/content",
		nil,
		&result,
	)
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if httpResponse.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", httpResponse.StatusCode)
	}
	if result.ContentID != "abc" || result.Status != "SALE" {
		t.Fatalf("decoded result = %+v", result)
	}
}

func TestDoRetriesOnlySafeMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		method       string
		wantAttempts int
		wantError    bool
	}{
		{name: "get", method: http.MethodGet, wantAttempts: 2},
		{name: "head", method: http.MethodHead, wantAttempts: 2},
		{name: "post", method: http.MethodPost, wantAttempts: 1, wantError: true},
		{name: "put", method: http.MethodPut, wantAttempts: 1, wantError: true},
		{name: "delete", method: http.MethodDelete, wantAttempts: 1, wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var attempts int
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return response(request, http.StatusServiceUnavailable, `{"errorCode":"TEMPORARY"}`, nil), nil
				}
				return response(request, http.StatusOK, `{}`, nil), nil
			})
			client, err := NewClient(
				&http.Client{Transport: transport},
				staticToken("token"),
				"account",
				WithMaxRetries(2),
			)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			client.sleep = func(context.Context, time.Duration) error { return nil }

			_, err = client.DoJSON(context.Background(), test.method, "/seller/v2/content", nil, nil)
			if test.wantError && err == nil {
				t.Fatal("expected request to fail")
			}
			if !test.wantError && err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if attempts != test.wantAttempts {
				t.Fatalf("attempts = %d, want %d", attempts, test.wantAttempts)
			}
		})
	}
}

func TestDoReplaysGETBodyOnRetry(t *testing.T) {
	t.Parallel()

	var bodies []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		bodies = append(bodies, string(body))
		if len(bodies) == 1 {
			return response(request, http.StatusRequestTimeout, `{}`, nil), nil
		}
		return response(request, http.StatusOK, `{}`, nil), nil
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("token"),
		"account",
		WithMaxRetries(1),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }

	payload := map[string]string{"contentId": "000000000001"}
	if _, err := client.DoJSON(t.Context(), http.MethodGet, "/seller/v2/content/comment", payload, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] == "" {
		t.Fatalf("request bodies = %q, want identical non-empty bodies", bodies)
	}
}

func TestDoDoesNotRetryUnreplayableGETBody(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return response(request, http.StatusServiceUnavailable, `{}`, nil), nil
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("token"),
		"account",
		WithMaxRetries(2),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		DeveloperAPIBaseURL+"/seller/v2/content/comment",
		io.NopCloser(strings.NewReader(`{"contentId":"000000000001"}`)),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if _, err := client.Do(request, nil); err == nil {
		t.Fatal("expected API error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoRetriesSafeTransportErrorsAndHonorsBoundedRetryAfter(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		attempts int
		delays   []time.Duration
	)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		switch attempts {
		case 1:
			return nil, errors.New("connection reset")
		case 2:
			return response(
				request,
				http.StatusTooManyRequests,
				`{"error":{"code":"RATE_LIMITED","message":"slow down"}}`,
				http.Header{"Retry-After": []string{"3600"}},
			), nil
		default:
			return response(request, http.StatusOK, `{}`, nil), nil
		}
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("token"),
		"account",
		WithMaxRetries(2),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	if _, err := client.DoJSON(context.Background(), http.MethodGet, "/seller/v2/content", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(delays) != 2 {
		t.Fatalf("delays = %v, want two entries", delays)
	}
	if delays[0] != defaultRetryDelay {
		t.Fatalf("first delay = %v, want %v", delays[0], defaultRetryDelay)
	}
	if delays[1] != defaultMaxRetryDelay {
		t.Fatalf("Retry-After delay = %v, want cap %v", delays[1], defaultMaxRetryDelay)
	}
}

func TestDoStopsAtContextDeadline(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client, err := NewClient(
		&http.Client{Transport: transport},
		staticToken("token"),
		"account",
		WithRequestTimeout(5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.DoJSON(context.Background(), http.MethodGet, "/seller/v2/content", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestDoRejectsRedirectOutsideAllowlist(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(
			request,
			http.StatusFound,
			"",
			http.Header{"Location": []string{"https://example.com/capture"}},
		), nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, staticToken("token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.DoJSON(context.Background(), http.MethodGet, "/seller/v2/content", nil, nil)
	if err == nil {
		t.Fatal("expected external redirect to fail")
	}
	if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "account") {
		t.Fatalf("redirect error leaked credentials: %v", err)
	}
}

func response(
	request *http.Request,
	status int,
	body string,
	header http.Header,
) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestDoRejectsMultipleJSONValues(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, `{} {}`, nil), nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, staticToken("token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var destination map[string]any
	_, err = client.DoJSON(context.Background(), http.MethodGet, "/seller/v2/content", nil, &destination)
	if err == nil {
		t.Fatal("expected multiple JSON values to fail")
	}
}

func TestDoDrainsSuccessfulResponseWhenNoResult(t *testing.T) {
	t.Parallel()

	body := &trackingReadCloser{Reader: bytes.NewBufferString(`{"ignored":true}`)}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, staticToken("token"), "account")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.DoJSON(context.Background(), http.MethodDelete, "/seller/v2/content", nil, nil); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (body *trackingReadCloser) Close() error {
	body.closed = true
	return nil
}
