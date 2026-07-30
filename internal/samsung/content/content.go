// Package content implements the mutating Galaxy Store Content Publish APIs.
//
// Samsung can keep SALE and REGISTRATION variants for one content ID at the
// same time. This package therefore performs only explicit, one-to-one API
// operations and never guesses which variant a caller intended.
package content

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
)

const (
	contentUpdatePath       = "/seller/contentUpdate"
	contentSubmitPath       = "/seller/contentSubmit"
	contentStatusUpdatePath = "/seller/contentStatusUpdate"
	binaryPath              = "/seller/v2/content/binary"
	createUploadSessionPath = "/seller/createUploadSessionId"
	expectedFileUploadURL   = "https://seller.samsungapps.com/galaxyapi/fileUpload"
	successfulResultCode    = "0000"
	contentIdentifierLength = 12
)

// Client is the narrow Galaxy Store client surface used by this package.
type Client interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
	NewUploadRequest(context.Context, io.Reader, string) (*http.Request, error)
	Do(*http.Request, any) (*http.Response, error)
}

// Service mutates Galaxy Store app content.
type Service struct {
	client Client
}

// New creates a content service.
func New(client Client) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// Result is Samsung's common mutation response.
type Result struct {
	ResultCode    string          `json:"resultCode,omitempty"`
	ResultMessage string          `json:"resultMessage,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

// BinarySequence accepts the string or number form Samsung has used for
// binarySeq in its documentation and responses.
type BinarySequence string

// UnmarshalJSON decodes a Samsung binary sequence without losing precision.
func (sequence *BinarySequence) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*sequence = BinarySequence(text)
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return errors.New("binary sequence must be a string or number")
	}
	*sequence = BinarySequence(number.String())
	return nil
}

// AddBinaryResult is returned after registering an uploaded binary.
type AddBinaryResult struct {
	ResultCode    string `json:"resultCode,omitempty"`
	ResultMessage string `json:"resultMessage,omitempty"`
	Data          struct {
		BinarySequence BinarySequence `json:"binarySeq"`
	} `json:"data,omitempty"`
}

// AddBinaryRequest registers a file previously uploaded to Samsung.
type AddBinaryRequest struct {
	ContentID                   string `json:"contentId"`
	FileKey                     string `json:"filekey"`
	GMS                         string `json:"gms"`
	BinarySequenceForDeviceInfo string `json:"binarySeqForDeviceInfo,omitempty"`
}

// UpdateBinaryRequest changes Google Mobile Services metadata for a registered
// binary. Samsung's current endpoint does not replace the binary file.
type UpdateBinaryRequest struct {
	ContentID      string `json:"contentId"`
	BinarySequence string `json:"binarySeq"`
	GMS            string `json:"gms"`
}

// UploadSession identifies a 24-hour Samsung upload session.
type UploadSession struct {
	URL       string `json:"url"`
	SessionID string `json:"sessionId"`
}

// Update sends an exact contentUpdate JSON object after enforcing Samsung's
// required fields. Raw JSON is intentional: omitted, null, and empty
// collections have different semantics in this API and must remain distinct.
//
// binaryList is rejected unconditionally. Samsung deprecated that field in
// March 2025 and rejects it starting in July 2026; callers must use the v2
// binary methods instead.
func (service *Service) Update(
	ctx context.Context,
	contentID string,
	payload json.RawMessage,
) (*Result, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	if err := validateUpdatePayload(contentID, payload); err != nil {
		return nil, err
	}

	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		contentUpdatePath,
		payload,
		&result,
	); err != nil {
		return nil, fmt.Errorf("update Galaxy Store content: %w", err)
	}
	if err := result.validate("update Galaxy Store content"); err != nil {
		return nil, err
	}
	return &result, nil
}

// Submit sends a REGISTERING app to Samsung for review.
func (service *Service) Submit(ctx context.Context, contentID string) (*Result, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}

	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		contentSubmitPath,
		struct {
			ContentID string `json:"contentId"`
		}{ContentID: contentID},
		&result,
	); err != nil {
		return nil, fmt.Errorf("submit Galaxy Store content: %w", err)
	}
	if err := result.validate("submit Galaxy Store content"); err != nil {
		return nil, err
	}
	return &result, nil
}

// ChangeStatus distributes, suspends, or terminates an app.
func (service *Service) ChangeStatus(
	ctx context.Context,
	contentID string,
	status string,
) (*Result, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	status, err := normalizeContentStatus(status)
	if err != nil {
		return nil, err
	}

	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		contentStatusUpdatePath,
		struct {
			ContentID     string `json:"contentId"`
			ContentStatus string `json:"contentStatus"`
		}{
			ContentID:     contentID,
			ContentStatus: status,
		},
		&result,
	); err != nil {
		return nil, fmt.Errorf("change Galaxy Store content status: %w", err)
	}
	if err := result.validate("change Galaxy Store content status"); err != nil {
		return nil, err
	}
	return &result, nil
}

// AddBinary registers an uploaded binary using Samsung's current v2 endpoint.
func (service *Service) AddBinary(
	ctx context.Context,
	request AddBinaryRequest,
) (*AddBinaryResult, error) {
	if err := validateContentID(request.ContentID); err != nil {
		return nil, err
	}
	if err := requireOpaqueValue("file key", request.FileKey); err != nil {
		return nil, err
	}
	gms, err := normalizeGMS(request.GMS)
	if err != nil {
		return nil, err
	}
	if request.BinarySequenceForDeviceInfo != "" {
		if err := validateBinarySequence(
			"binary sequence for device information",
			request.BinarySequenceForDeviceInfo,
		); err != nil {
			return nil, err
		}
	}
	request.GMS = gms

	var result AddBinaryResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		binaryPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("add Galaxy Store binary: %w", err)
	}
	if err := validateResult(result.ResultCode, result.ResultMessage, "add Galaxy Store binary"); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateBinary changes a binary through Samsung's current v2 endpoint.
func (service *Service) UpdateBinary(
	ctx context.Context,
	request UpdateBinaryRequest,
) (*Result, error) {
	if err := validateContentID(request.ContentID); err != nil {
		return nil, err
	}
	if err := validateBinarySequence("binary sequence", request.BinarySequence); err != nil {
		return nil, err
	}
	gms, err := normalizeGMS(request.GMS)
	if err != nil {
		return nil, err
	}
	request.GMS = gms

	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPut,
		binaryPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("update Galaxy Store binary: %w", err)
	}
	if err := result.validate("update Galaxy Store binary"); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteBinary permanently removes a registered binary.
func (service *Service) DeleteBinary(
	ctx context.Context,
	contentID string,
	binarySequence string,
) (*Result, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	if err := validateBinarySequence("binary sequence", binarySequence); err != nil {
		return nil, err
	}

	query := make(url.Values)
	query.Set("contentId", contentID)
	query.Set("binarySeq", binarySequence)

	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodDelete,
		binaryPath+"?"+query.Encode(),
		nil,
		&result,
	); err != nil {
		return nil, fmt.Errorf("delete Galaxy Store binary: %w", err)
	}
	if err := result.validate("delete Galaxy Store binary"); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateUploadSession creates a Samsung file-upload session valid for 24 hours.
func (service *Service) CreateUploadSession(ctx context.Context) (*UploadSession, error) {
	var session UploadSession
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		createUploadSessionPath,
		nil,
		&session,
	); err != nil {
		return nil, fmt.Errorf("create Galaxy Store upload session: %w", err)
	}
	if err := requireOpaqueValue("upload session ID", session.SessionID); err != nil {
		return nil, fmt.Errorf("create Galaxy Store upload session: Samsung returned an invalid session: %w", err)
	}
	if session.URL != "" && session.URL != expectedFileUploadURL {
		return nil, errors.New("create Galaxy Store upload session: Samsung returned an unexpected upload URL")
	}
	return &session, nil
}

func validateUpdatePayload(contentID string, payload json.RawMessage) error {
	if len(bytes.TrimSpace(payload)) == 0 {
		return errors.New("content update payload is required")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return errors.New("content update payload must be a JSON object")
	}
	if fields == nil {
		return errors.New("content update payload must be a JSON object")
	}
	for name := range fields {
		if strings.EqualFold(name, "binaryList") {
			return errors.New("content update payload must not contain binaryList; use the v2 binary endpoints")
		}
	}

	payloadContentID, err := requiredStringField(fields, "contentId")
	if err != nil {
		return err
	}
	if payloadContentID != contentID {
		return errors.New("content update payload contentId does not match the requested content ID")
	}
	if _, err := requiredStringField(fields, "defaultLanguageCode"); err != nil {
		return err
	}
	paid, err := requiredStringField(fields, "paid")
	if err != nil {
		return err
	}
	if paid != "Y" && paid != "N" {
		return errors.New("content update paid must be Y or N")
	}
	publicationType, err := requiredStringField(fields, "publicationType")
	if err != nil {
		return err
	}
	switch publicationType {
	case "01", "02", "03":
		return nil
	default:
		return errors.New("content update publicationType must be 01, 02, or 03")
	}
}

func requiredStringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("content update %s is required", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("content update %s must be a non-empty string", name)
	}
	return value, nil
}

func validateContentID(contentID string) error {
	if len(contentID) != contentIdentifierLength || contentID != strings.TrimSpace(contentID) {
		return errors.New("content ID must contain exactly 12 digits")
	}
	for _, character := range contentID {
		if character < '0' || character > '9' {
			return errors.New("content ID must contain exactly 12 digits")
		}
	}
	return nil
}

func normalizeContentStatus(status string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch normalized {
	case "FOR_SALE", "SUSPENDED", "TERMINATED":
		return normalized, nil
	default:
		return "", errors.New("content status must be FOR_SALE, SUSPENDED, or TERMINATED")
	}
}

func normalizeGMS(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized != "Y" && normalized != "N" {
		return "", errors.New("GMS must be Y or N")
	}
	return normalized, nil
}

func validateBinarySequence(name string, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must contain only digits", name)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return fmt.Errorf("%s must contain only digits", name)
		}
	}
	return nil
}

func requireOpaqueValue(name string, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s is required and must not contain surrounding whitespace", name)
	}
	return nil
}

func (result Result) validate(operation string) error {
	return validateResult(result.ResultCode, result.ResultMessage, operation)
}

func validateResult(code string, message string, operation string) error {
	if code == "" || code == successfulResultCode {
		return nil
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%s: Samsung returned result code %s", operation, code)
	}
	return fmt.Errorf("%s: Samsung returned result code %s: %s", operation, code, message)
}
