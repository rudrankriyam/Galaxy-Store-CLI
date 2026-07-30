package samsung

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DeveloperAPIBaseURL is the only host used for Galaxy Store Developer API
	// requests.
	DeveloperAPIBaseURL = "https://devapi.samsungapps.com"

	// FileUploadURL is Samsung's dedicated binary and image upload endpoint.
	FileUploadURL = "https://seller.samsungapps.com/galaxyapi/fileUpload"

	// ServiceAccountIDHeader is required alongside the bearer token on every
	// Galaxy Store Developer API request.
	ServiceAccountIDHeader = "service-account-id"

	defaultRequestTimeout = 60 * time.Second
	defaultMaxRetries     = 2
	defaultRetryDelay     = 200 * time.Millisecond
	defaultMaxRetryDelay  = 2 * time.Second
)

var (
	developerAPIBase, _ = url.Parse(DeveloperAPIBaseURL)
	fileUploadURL, _    = url.Parse(FileUploadURL)
)

// TokenProvider returns a Galaxy Store Developer API access token.
type TokenProvider interface {
	Token(context.Context) (string, error)
}

// TokenProviderFunc adapts a function to TokenProvider.
type TokenProviderFunc func(context.Context) (string, error)

// Token calls f(ctx).
func (f TokenProviderFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

// Option configures a Client.
type Option func(*Client) error

// WithRequestTimeout sets a per-operation timeout. The timeout covers retries
// and retry backoff, not only an individual HTTP round trip.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(client *Client) error {
		if timeout <= 0 {
			return errors.New("request timeout must be positive")
		}
		client.requestTimeout = timeout
		return nil
	}
}

// WithMaxRetries sets the number of retries for safe GET and HEAD requests.
// Mutating requests are never retried.
func WithMaxRetries(maxRetries int) Option {
	return func(client *Client) error {
		if maxRetries < 0 {
			return errors.New("maximum retries cannot be negative")
		}
		client.maxRetries = maxRetries
		return nil
	}
}

// Client sends authenticated requests to the Galaxy Store Developer API.
type Client struct {
	httpClient       *http.Client
	tokenProvider    TokenProvider
	serviceAccountID string
	requestTimeout   time.Duration
	maxRetries       int
	retryDelay       time.Duration
	maxRetryDelay    time.Duration
	sleep            func(context.Context, time.Duration) error
}

// NewClient constructs a Galaxy Store Developer API client. httpClient may be
// nil to use an otherwise-default client. The injected client is copied so its
// redirect policy is not modified.
func NewClient(
	httpClient *http.Client,
	tokenProvider TokenProvider,
	serviceAccountID string,
	options ...Option,
) (*Client, error) {
	if tokenProvider == nil {
		return nil, errors.New("token provider is required")
	}
	if strings.TrimSpace(serviceAccountID) == "" {
		return nil, errors.New("service account ID is required")
	}

	var configuredHTTPClient http.Client
	if httpClient != nil {
		configuredHTTPClient = *httpClient
	}

	previousRedirectPolicy := configuredHTTPClient.CheckRedirect
	configuredHTTPClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !allowedURL(request.URL) {
			return errors.New("redirect target is not an allowed Galaxy Store host")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}

	client := &Client{
		httpClient:       &configuredHTTPClient,
		tokenProvider:    tokenProvider,
		serviceAccountID: strings.TrimSpace(serviceAccountID),
		requestTimeout:   defaultRequestTimeout,
		maxRetries:       defaultMaxRetries,
		retryDelay:       defaultRetryDelay,
		maxRetryDelay:    defaultMaxRetryDelay,
		sleep:            sleepWithContext,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(client); err != nil {
			return nil, fmt.Errorf("configure Galaxy Store client: %w", err)
		}
	}
	return client, nil
}

// NewRequest builds an authenticated JSON request to the fixed Developer API
// host. endpoint must be an absolute path, optionally with a query string.
func (client *Client) NewRequest(
	ctx context.Context,
	method string,
	endpoint string,
	body any,
) (*http.Request, error) {
	target, err := developerAPIURL(endpoint)
	if err != nil {
		return nil, err
	}

	var requestBody io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode request body: %w", marshalErr)
		}
		requestBody = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, target.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build Galaxy Store request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if err := client.authorize(ctx, request); err != nil {
		return nil, err
	}
	return request, nil
}

// NewUploadRequest builds an authenticated POST request to Samsung's fixed file
// upload endpoint. The caller supplies the multipart content type, including
// its boundary.
func (client *Client) NewUploadRequest(
	ctx context.Context,
	body io.Reader,
	contentType string,
) (*http.Request, error) {
	if body == nil {
		return nil, errors.New("upload body is required")
	}
	if strings.TrimSpace(contentType) == "" {
		return nil, errors.New("upload content type is required")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, FileUploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("build Galaxy Store upload request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", contentType)
	if err := client.authorize(ctx, request); err != nil {
		return nil, err
	}
	return request, nil
}

// DoJSON builds, sends, and JSON-decodes one Developer API request.
func (client *Client) DoJSON(
	ctx context.Context,
	method string,
	endpoint string,
	body any,
	result any,
) (*http.Response, error) {
	request, err := client.NewRequest(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	return client.Do(request, result)
}

// Do sends request and decodes a successful JSON response into result. Response
// bodies are always closed before this method returns. The returned response
// remains useful for its status and headers.
func (client *Client) Do(request *http.Request, result any) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("request is required")
	}
	if !allowedURL(request.URL) {
		return nil, errors.New("request target is not an allowed Galaxy Store endpoint")
	}

	ctx, cancel := context.WithTimeout(request.Context(), client.requestTimeout)
	defer cancel()

	safeMethod := request.Method == http.MethodGet || request.Method == http.MethodHead
	replayableBody := request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
	safeToRetry := safeMethod && replayableBody
	var response *http.Response
	var err error
	for attempt := 0; ; attempt++ {
		attemptRequest := request.Clone(ctx)
		if attempt > 0 && request.Body != nil && request.Body != http.NoBody && request.GetBody != nil {
			attemptRequest.Body, err = request.GetBody()
			if err != nil {
				return response, fmt.Errorf("replay Galaxy Store request body: %w", err)
			}
		}
		response, err = client.httpClient.Do(attemptRequest)
		if err == nil && (!safeToRetry || !retryableStatus(response.StatusCode) || attempt >= client.maxRetries) {
			break
		}
		if err != nil && (!safeToRetry || attempt >= client.maxRetries || ctx.Err() != nil) {
			break
		}

		delay := client.backoff(attempt)
		if response != nil {
			delay = retryDelay(response.Header.Get("Retry-After"), time.Now(), delay, client.maxRetryDelay)
			drainAndClose(response.Body)
		}
		if sleepErr := client.sleep(ctx, delay); sleepErr != nil {
			return response, sleepErr
		}
	}

	if err != nil {
		if ctx.Err() != nil {
			return response, ctx.Err()
		}
		return response, fmt.Errorf("send Galaxy Store request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		apiError := decodeAPIError(response)
		authorization := request.Header.Get("Authorization")
		redactAPIError(
			apiError,
			authorization,
			strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer")),
			client.serviceAccountID,
		)
		return response, apiError
	}

	if result == nil || request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent {
		drain(response.Body)
		return response, nil
	}
	if err := decodeJSON(response.Body, result); err != nil {
		return response, fmt.Errorf("decode Galaxy Store response: %w", err)
	}
	return response, nil
}

func (client *Client) authorize(ctx context.Context, request *http.Request) error {
	token, err := client.tokenProvider.Token(ctx)
	if err != nil {
		return errors.New("get Galaxy Store access token")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("access token is empty")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(ServiceAccountIDHeader, client.serviceAccountID)
	return nil
}

func (client *Client) backoff(attempt int) time.Duration {
	delay := client.retryDelay
	for range attempt {
		if delay >= client.maxRetryDelay/2 {
			return client.maxRetryDelay
		}
		delay *= 2
	}
	if delay > client.maxRetryDelay {
		return client.maxRetryDelay
	}
	return delay
}

func developerAPIURL(endpoint string) (*url.URL, error) {
	if !strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, "//") {
		return nil, errors.New("endpoint must be an absolute path")
	}
	reference, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("endpoint is invalid")
	}
	if reference.IsAbs() || reference.Host != "" || reference.User != nil || reference.Fragment != "" {
		return nil, errors.New("endpoint must not contain a host, user, or fragment")
	}
	target := developerAPIBase.ResolveReference(reference)
	if !allowedURL(target) {
		return nil, errors.New("endpoint is not allowed")
	}
	return target, nil
}

func allowedURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" || target.User != nil {
		return false
	}
	switch target.Host {
	case developerAPIBase.Host:
		return true
	case fileUploadURL.Host:
		return target.Path == fileUploadURL.Path &&
			target.RawQuery == "" &&
			target.Fragment == ""
	default:
		return false
	}
}

func retryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryDelay(value string, now time.Time, fallback time.Duration, maximum time.Duration) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maximum)
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return min(max(retryAt.Sub(now), 0), maximum)
	}
	return min(fallback, maximum)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func decodeJSON(reader io.Reader, result any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(result); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("response contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func drain(reader io.Reader) {
	_, _ = io.Copy(io.Discard, reader)
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	drain(io.LimitReader(body, 64<<10))
	_ = body.Close()
}
