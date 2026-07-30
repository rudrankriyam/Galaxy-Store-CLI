package samsung

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

const maxErrorBodyBytes = 1 << 20

// APIError is a non-2xx response from the Galaxy Store Developer API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

// Error returns a stable, secret-safe summary of the API failure.
func (err *APIError) Error() string {
	status := http.StatusText(err.StatusCode)
	if status == "" {
		status = "HTTP error"
	}
	summary := fmt.Sprintf("Galaxy Store API: HTTP %d %s", err.StatusCode, status)
	if err.Code != "" {
		summary += " (" + err.Code + ")"
	}
	if err.Message != "" {
		summary += ": " + err.Message
	}
	if err.RequestID != "" {
		summary += " [request-id: " + err.RequestID + "]"
	}
	return summary
}

func decodeAPIError(response *http.Response) *APIError {
	apiError := &APIError{
		StatusCode: response.StatusCode,
		RequestID: firstHeader(
			response.Header,
			"X-Request-Id",
			"X-Request-ID",
			"Request-Id",
			"Request-ID",
			"X-Samsung-Request-Id",
		),
	}

	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	if readErr != nil {
		apiError.Message = "unable to read error response"
		return apiError
	}

	apiError.Code, apiError.Message = samsungErrorFields(body)
	if apiError.Message == "" {
		apiError.Message = http.StatusText(response.StatusCode)
	}
	apiError.Code = safeErrorText(apiError.Code, 128)
	apiError.Message = safeErrorText(apiError.Message, 2048)
	apiError.RequestID = safeErrorText(apiError.RequestID, 256)
	return apiError
}

func samsungErrorFields(body []byte) (string, string) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", ""
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return "", ""
	}

	// Samsung APIs use several envelopes across Content Publish, IAP, GSS,
	// and Orders. Prefer an explicit nested error, then an errors collection,
	// then the top-level response fields.
	for _, key := range []string{"error", "errorInfo", "failure"} {
		if nested, ok := object[key].(map[string]any); ok {
			if code, message := fieldsFromObject(nested); code != "" || message != "" {
				return code, message
			}
		}
	}
	if errorsValue, ok := object["errors"].([]any); ok && len(errorsValue) > 0 {
		if nested, ok := errorsValue[0].(map[string]any); ok {
			if code, message := fieldsFromObject(nested); code != "" || message != "" {
				return code, message
			}
		}
	}
	return fieldsFromObject(object)
}

func fieldsFromObject(object map[string]any) (string, string) {
	code := firstString(
		object,
		"errorCode",
		"error_code",
		"code",
		"responseCode",
		"resultCode",
		"statusCode",
	)
	message := firstString(
		object,
		"errorMessage",
		"errorMsg",
		"error_message",
		"message",
		"responseMessage",
		"resultMessage",
		"detail",
		"description",
	)
	return code, message
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, exists := object[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		case float64:
			return fmt.Sprintf("%v", typed)
		}
	}
	return ""
}

func firstHeader(header http.Header, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(header.Get(key)); value != "" {
			return value
		}
	}
	return ""
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

func redactAPIError(apiError *APIError, secrets ...string) {
	if apiError == nil {
		return
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		apiError.Code = strings.ReplaceAll(apiError.Code, secret, "[REDACTED]")
		apiError.Message = strings.ReplaceAll(apiError.Message, secret, "[REDACTED]")
		apiError.RequestID = strings.ReplaceAll(apiError.RequestID, secret, "[REDACTED]")
	}
}
