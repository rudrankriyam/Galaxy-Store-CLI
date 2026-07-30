// Package beta provides closed-beta operations from Samsung's Content Publish
// API.
package beta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	betaTestPath = "/seller/v2/content/betaTest"
	maxPageSize  = 1000
	maxBatchSize = 1000
	defaultLimit = 1000
)

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
}

// Service manages Galaxy Store closed beta testers.
type Service struct {
	client JSONClient
}

// New creates a closed-beta service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// ListOptions selects one Samsung-provided beta tester page.
type ListOptions struct {
	ContentID string
	AppStatus string
	Offset    int
	Limit     int
}

// TestingURL contains the distribution links returned by Samsung.
type TestingURL struct {
	Android      string `json:"android,omitempty"`
	InstantPlay2 string `json:"instantPlay2,omitempty"`
}

// Test describes one closed beta tester page.
type Test struct {
	TotalNumberOfBetaTesters int        `json:"totalNumberOfBetaTesters"`
	BetaTesters              []string   `json:"betaTesters"`
	FeedbackChannel          string     `json:"feedbackChannel,omitempty"`
	BetaTestingURL           TestingURL `json:"betaTestingUrl"`
}

// UpdateInput describes an exact betaTest PUT. FeedbackChannel is a pointer so
// callers can distinguish omission from an explicitly empty value.
type UpdateInput struct {
	ContentID       string
	AddTesters      []string
	DeleteTesters   []string
	FeedbackChannel *string
}

type updateRequest struct {
	ContentID              string   `json:"contentId"`
	BetaTestersToBeAdded   []string `json:"betaTestersToBeAdded,omitempty"`
	BetaTestersToBeDeleted []string `json:"betaTestersToBeDeleted,omitempty"`
	FeedbackChannel        *string  `json:"feedbackChannel,omitempty"`
}

// UpdateResult lists individual tester IDs Samsung could not add or delete.
type UpdateResult struct {
	AdditionFailedTesters []string `json:"additionFailedTesters"`
	DeletionFailedTesters []string `json:"deletionFailedTesters"`
}

// TesterFailuresError prevents an HTTP-level success from hiding individual
// tester failures. Update returns both this error and the populated result.
type TesterFailuresError struct {
	AdditionFailed []string
	DeletionFailed []string
}

func (e *TesterFailuresError) Error() string {
	return fmt.Sprintf(
		"closed beta update had tester failures: %d addition, %d deletion",
		len(e.AdditionFailed),
		len(e.DeletionFailed),
	)
}

type envelope[T any] struct {
	ResultCode    string `json:"resultCode"`
	ResultMessage string `json:"resultMessage"`
	Data          T      `json:"data"`
}

// Get returns one page of closed-beta information. appStatus must be explicitly
// SALE or REGISTRATION because both variants can exist simultaneously.
func (service *Service) Get(ctx context.Context, options ListOptions) (*Test, error) {
	if err := validateContentID(options.ContentID); err != nil {
		return nil, err
	}
	status, err := normalizeAppStatus(options.AppStatus)
	if err != nil {
		return nil, err
	}
	if options.Offset < 0 {
		return nil, errors.New("beta tester offset cannot be negative")
	}
	limit := options.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxPageSize {
		return nil, fmt.Errorf("beta tester limit must be between 1 and %d", maxPageSize)
	}

	query := make(url.Values)
	query.Set("contentId", options.ContentID)
	query.Set("appStatus", status)
	query.Set("offset", strconv.Itoa(options.Offset))
	query.Set("limit", strconv.Itoa(limit))

	var response envelope[Test]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		betaTestPath+"?"+query.Encode(),
		nil,
		&response,
	); err != nil {
		return nil, fmt.Errorf("view closed beta test: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("view closed beta test: %w", err)
	}
	if response.Data.BetaTesters == nil {
		response.Data.BetaTesters = []string{}
	}
	return &response.Data, nil
}

// Update adds or removes testers and optionally changes the feedback channel.
// Samsung may accept the request but reject individual account IDs; those IDs
// are returned in UpdateResult and surfaced as TesterFailuresError.
func (service *Service) Update(ctx context.Context, input UpdateInput) (*UpdateResult, error) {
	if err := validateContentID(input.ContentID); err != nil {
		return nil, err
	}
	if len(input.AddTesters) == 0 && len(input.DeleteTesters) == 0 && input.FeedbackChannel == nil {
		return nil, errors.New("closed beta update must add testers, delete testers, or set feedback channel")
	}
	if err := validateTesters("beta testers to add", input.AddTesters); err != nil {
		return nil, err
	}
	if err := validateTesters("beta testers to delete", input.DeleteTesters); err != nil {
		return nil, err
	}

	request := updateRequest{
		ContentID:              input.ContentID,
		BetaTestersToBeAdded:   append([]string(nil), input.AddTesters...),
		BetaTestersToBeDeleted: append([]string(nil), input.DeleteTesters...),
		FeedbackChannel:        input.FeedbackChannel,
	}
	var response envelope[UpdateResult]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPut,
		betaTestPath,
		request,
		&response,
	); err != nil {
		return nil, fmt.Errorf("update closed beta test: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("update closed beta test: %w", err)
	}
	if response.Data.AdditionFailedTesters == nil {
		response.Data.AdditionFailedTesters = []string{}
	}
	if response.Data.DeletionFailedTesters == nil {
		response.Data.DeletionFailedTesters = []string{}
	}
	if len(response.Data.AdditionFailedTesters) != 0 || len(response.Data.DeletionFailedTesters) != 0 {
		return &response.Data, &TesterFailuresError{
			AdditionFailed: append([]string(nil), response.Data.AdditionFailedTesters...),
			DeletionFailed: append([]string(nil), response.Data.DeletionFailedTesters...),
		}
	}
	return &response.Data, nil
}

func (response envelope[T]) success() error {
	if response.ResultCode == "0000" {
		return nil
	}
	if response.ResultCode == "" {
		return errors.New("response is missing resultCode")
	}
	return fmt.Errorf("samsung result %s: %s", response.ResultCode, response.ResultMessage)
}

func validateTesters(label string, testers []string) error {
	if len(testers) > maxBatchSize {
		return fmt.Errorf("%s cannot exceed %d accounts per request", label, maxBatchSize)
	}
	for _, tester := range testers {
		if tester == "" || tester != strings.TrimSpace(tester) {
			return fmt.Errorf("%s must not contain blank or padded account IDs", label)
		}
	}
	return nil
}

func validateContentID(contentID string) error {
	if contentID != strings.TrimSpace(contentID) || len(contentID) != 12 {
		return errors.New("content ID must contain exactly 12 digits")
	}
	for _, character := range contentID {
		if character < '0' || character > '9' {
			return errors.New("content ID must contain exactly 12 digits")
		}
	}
	return nil
}

func normalizeAppStatus(value string) (string, error) {
	if value != strings.TrimSpace(value) {
		return "", errors.New("app status must be exactly SALE or REGISTRATION")
	}
	switch value {
	case "SALE", "REGISTRATION":
		return value, nil
	default:
		return "", errors.New("app status must be exactly SALE or REGISTRATION")
	}
}
