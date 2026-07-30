// Package receipts verifies Samsung IAP purchases using the public,
// unauthenticated receipt endpoint.
package receipts

import (
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
	"unicode"
)

const (
	// VerificationURL is Samsung's fixed HTTPS receipt verification endpoint.
	VerificationURL = "https://iap.samsungapps.com/iap/v6/receipt"

	StatusSuccess = "success"
	StatusFail    = "fail"
	StatusCancel  = "cancel"

	defaultRequestTimeout = 30 * time.Second
	maxPurchaseIDBytes    = 1024
	maxResponseBodyBytes  = 1 << 20
)

var verificationBase, _ = url.Parse(VerificationURL)

// Option configures a Service.
type Option func(*Service) error

// WithRequestTimeout sets the receipt request timeout.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(service *Service) error {
		if timeout <= 0 {
			return errors.New("request timeout must be positive")
		}
		service.requestTimeout = timeout
		return nil
	}
}

// Service verifies Samsung IAP receipts against the fixed Samsung host.
type Service struct {
	httpClient     *http.Client
	requestTimeout time.Duration
}

// New creates a receipt verification service. The supplied HTTP client is
// copied so the caller's redirect policy is not modified.
func New(httpClient *http.Client, options ...Option) (*Service, error) {
	var configured http.Client
	if httpClient != nil {
		configured = *httpClient
	}

	previousRedirectPolicy := configured.CheckRedirect
	configured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !allowedVerificationURL(request.URL) {
			return errors.New("redirect target is not an allowed Samsung IAP endpoint")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}

	service := &Service{
		httpClient:     &configured,
		requestTimeout: defaultRequestTimeout,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, fmt.Errorf("configure Samsung IAP receipt service: %w", err)
		}
	}
	return service, nil
}

// Receipt is Samsung's documented receipt verification response.
type Receipt struct {
	ItemID                 string `json:"itemId,omitempty"`
	PaymentID              string `json:"paymentId,omitempty"`
	OrderID                string `json:"orderId,omitempty"`
	PackageName            string `json:"packageName,omitempty"`
	ItemName               string `json:"itemName,omitempty"`
	ItemDescription        string `json:"itemDesc,omitempty"`
	ItemType               string `json:"itemType,omitempty"`
	CountryCode            string `json:"countryCode,omitempty"`
	PurchaseDate           string `json:"purchaseDate,omitempty"`
	PaymentAmount          string `json:"paymentAmount,omitempty"`
	Status                 string `json:"status"`
	PaymentMethod          string `json:"paymentMethod,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	ConsumeYN              string `json:"consumeYN,omitempty"`
	ConsumeDate            string `json:"consumeDate,omitempty"`
	ConsumeDeviceModel     string `json:"consumeDeviceModel,omitempty"`
	AcknowledgeYN          string `json:"acknowledgeYN,omitempty"`
	AcknowledgeDate        string `json:"acknowledgeDate,omitempty"`
	AcknowledgeDeviceModel string `json:"acknowledgeDeviceModel,omitempty"`
	PassThroughParameter   string `json:"passThroughParam,omitempty"`
	CurrencyCode           string `json:"currencyCode,omitempty"`
	CurrencyUnit           string `json:"currencyUnit,omitempty"`
	CancelDate             string `json:"cancelDate,omitempty"`
	ObfuscatedAccountID    string `json:"obfuscatedAccountId,omitempty"`
	ObfuscatedProfileID    string `json:"obfuscatedProfileId,omitempty"`
	ErrorCode              int    `json:"errorCode,omitempty"`
	ErrorMessage           string `json:"errorMessage,omitempty"`
}

// UnmarshalJSON accepts Samsung's documented comsumeDate typo as well as the
// consumeDate field used by its examples and current responses.
func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	type receiptAlias Receipt
	var decoded struct {
		receiptAlias
		MisspelledConsumeDate string `json:"comsumeDate"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*receipt = Receipt(decoded.receiptAlias)
	if receipt.ConsumeDate == "" {
		receipt.ConsumeDate = decoded.MisspelledConsumeDate
	}
	return nil
}

// Successful reports whether Samsung verified a completed purchase.
func (receipt Receipt) Successful() bool {
	return receipt.Status == StatusSuccess
}

// Canceled reports whether Samsung verified a canceled purchase.
func (receipt Receipt) Canceled() bool {
	return receipt.Status == StatusCancel
}

// HTTPError is a non-2xx response from the Samsung IAP receipt endpoint.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

// Error returns a stable, purchase-ID-safe error.
func (err *HTTPError) Error() string {
	status := http.StatusText(err.StatusCode)
	if status == "" {
		status = "HTTP error"
	}
	summary := fmt.Sprintf("Samsung IAP receipt API: HTTP %d %s", err.StatusCode, status)
	if err.Code != "" {
		summary += " (" + err.Code + ")"
	}
	if err.Message != "" {
		summary += ": " + err.Message
	}
	return summary
}

// Verify gets one receipt. A valid HTTP response with status fail or cancel is
// returned as a Receipt without converting that business result into a
// transport error.
func (service *Service) Verify(ctx context.Context, purchaseID string) (*Receipt, error) {
	if err := validatePurchaseID(purchaseID); err != nil {
		return nil, err
	}

	query := make(url.Values)
	query.Set("purchaseID", purchaseID)
	target := VerificationURL + "?" + query.Encode()

	requestContext, cancel := context.WithTimeout(ctx, service.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("build Samsung IAP receipt request")
	}
	request.Header.Set("Accept", "application/json")

	response, err := service.httpClient.Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return nil, requestContext.Err()
		}
		return nil, errors.New("send Samsung IAP receipt request")
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, decodeHTTPError(response, purchaseID)
	}

	var receipt Receipt
	if err := decodeReceiptJSON(response.Body, &receipt); err != nil {
		return nil, errors.New("decode Samsung IAP receipt response")
	}
	receipt.ErrorMessage = redact(receipt.ErrorMessage, purchaseID)
	switch receipt.Status {
	case StatusSuccess, StatusFail, StatusCancel:
		return &receipt, nil
	default:
		return nil, errors.New("receipt response has an unknown status")
	}
}

func validatePurchaseID(purchaseID string) error {
	if purchaseID == "" {
		return errors.New("purchase ID is required")
	}
	if purchaseID != strings.TrimSpace(purchaseID) {
		return errors.New("purchase ID must not contain surrounding whitespace")
	}
	if len(purchaseID) > maxPurchaseIDBytes {
		return errors.New("purchase ID is too long")
	}
	for _, character := range purchaseID {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return errors.New("purchase ID must not contain whitespace or control characters")
		}
	}
	return nil
}

func allowedVerificationURL(target *url.URL) bool {
	if target == nil ||
		target.Scheme != verificationBase.Scheme ||
		target.Host != verificationBase.Host ||
		target.User != nil ||
		target.Path != verificationBase.Path ||
		target.Fragment != "" {
		return false
	}
	query := target.Query()
	return len(query) == 1 &&
		len(query["purchaseID"]) == 1 &&
		query.Get("purchaseID") != ""
}

func decodeReceiptJSON(reader io.Reader, receipt *Receipt) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBodyBytes))
	if err := decoder.Decode(receipt); err != nil {
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

func decodeHTTPError(response *http.Response, purchaseID string) *HTTPError {
	result := &HTTPError{StatusCode: response.StatusCode}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes))
	if err != nil {
		result.Message = "unable to read error response"
		return result
	}
	var payload struct {
		ErrorCode    json.RawMessage `json:"errorCode"`
		ErrorMessage string          `json:"errorMessage"`
		Message      string          `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		result.Code = rawErrorCode(payload.ErrorCode)
		result.Message = payload.ErrorMessage
		if result.Message == "" {
			result.Message = payload.Message
		}
	}
	result.Code = safeErrorText(redact(result.Code, purchaseID), 128)
	result.Message = safeErrorText(redact(result.Message, purchaseID), 2048)
	return result
}

func rawErrorCode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		if _, err := strconv.ParseFloat(number.String(), 64); err == nil {
			return number.String()
		}
	}
	return ""
}

func redact(value string, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}

func safeErrorText(value string, maximum int) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > maximum {
		value = value[:maximum] + "..."
	}
	return value
}
