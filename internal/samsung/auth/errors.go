package auth

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIError describes a non-success response from Samsung.
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	From       string `json:"from,omitempty"`
}

func (err *APIError) Error() string {
	switch {
	case err.Code != "" && err.Message != "":
		return fmt.Sprintf("Samsung authentication failed (HTTP %d, %s): %s", err.StatusCode, err.Code, err.Message)
	case err.Code != "":
		return fmt.Sprintf("Samsung authentication failed (HTTP %d, %s)", err.StatusCode, err.Code)
	default:
		return fmt.Sprintf("Samsung authentication failed (HTTP %d)", err.StatusCode)
	}
}

// RequestError reports a transport failure without rendering credentials that
// may have been included in an underlying transport error.
type RequestError struct {
	Operation string
	Err       error
}

func (err *RequestError) Error() string {
	return err.Operation + ": request failed"
}

// Unwrap preserves errors.Is/errors.As behavior while keeping normal output safe.
func (err *RequestError) Unwrap() error {
	return err.Err
}

func decodeAPIError(statusCode int, body []byte, secrets []string) error {
	apiError := &APIError{StatusCode: statusCode}
	if err := json.Unmarshal(body, apiError); err != nil {
		return apiError
	}
	apiError.Code = redact(apiError.Code, secrets)
	apiError.Message = redact(apiError.Message, secrets)
	apiError.From = redact(apiError.From, secrets)
	return apiError
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}
