// Package rollout provides staged rollout rate and binary operations from
// Samsung's Content Publish API.
package rollout

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
	ratePath   = "/seller/v2/content/stagedRolloutRate"
	binaryPath = "/seller/v2/content/stagedRolloutBinary"

	functionEnable  = "ENABLE_ROLLOUT"
	functionDisable = "DISABLE_ROLLOUT"
	functionAdd     = "ADD"
	functionRemove  = "REMOVE"
)

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
}

// Service manages Galaxy Store staged rollouts.
type Service struct {
	client JSONClient
}

// New creates a staged-rollout service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// CountryRate is a country-specific rollout percentage.
type CountryRate struct {
	CountryCode string `json:"countryCode"`
	RolloutRate int    `json:"rolloutRate"`
}

// Rate is the current default and country-specific staged rollout state.
type Rate struct {
	RolloutRate int           `json:"rolloutRate"`
	Countries   []CountryRate `json:"countries"`
}

// SetRateInput describes a monotonic staged rollout update.
type SetRateInput struct {
	ContentID   string
	AppStatus   string
	RolloutRate int
	Countries   []CountryRate
}

type rateRequest struct {
	ContentID   string        `json:"contentId"`
	Function    string        `json:"function"`
	AppStatus   string        `json:"appStatus"`
	RolloutRate *int          `json:"rolloutRate,omitempty"`
	Countries   []CountryRate `json:"countries,omitempty"`
}

// Binary is one app binary and its current staged rollout state.
type Binary struct {
	Sequence      int    `json:"seq"`
	VersionCode   string `json:"versionCode,omitempty"`
	VersionName   string `json:"versionName,omitempty"`
	FileName      string `json:"fileName,omitempty"`
	FileSize      string `json:"fileSize,omitempty"`
	RolloutStatus string `json:"rolloutStatus,omitempty"`
	AppStatus     string `json:"appStatus,omitempty"`
}

// BinaryList is the response from View Staged Rollout Binaries.
type BinaryList struct {
	Binaries []Binary `json:"binaries"`
}

// MutationResult describes the accepted state transition.
type MutationResult struct {
	ResultCode    string `json:"resultCode"`
	ResultMessage string `json:"resultMessage"`
	Function      string `json:"function"`
	Completed     bool   `json:"completed"`
}

type binaryRequest struct {
	ContentID string `json:"contentId"`
	Function  string `json:"function"`
	BinarySeq string `json:"binarySeq"`
}

type envelope[T any] struct {
	ResultCode    string `json:"resultCode"`
	ResultMessage string `json:"resultMessage"`
	Data          T      `json:"data"`
}

// ViewRate gets current rollout rates for an explicit app variant.
func (service *Service) ViewRate(ctx context.Context, contentID, appStatus string) (*Rate, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	status, err := normalizeAppStatus(appStatus)
	if err != nil {
		return nil, err
	}

	var response envelope[Rate]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		endpoint(ratePath, contentID, status),
		nil,
		&response,
	); err != nil {
		return nil, fmt.Errorf("view staged rollout rate: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("view staged rollout rate: %w", err)
	}
	if response.Data.Countries == nil {
		response.Data.Countries = []CountryRate{}
	}
	return &response.Data, nil
}

// SetRate reads the current state, rejects a decrease locally, then enables or
// advances the staged rollout. The PUT is a mutation and is not retried by the
// Samsung client.
func (service *Service) SetRate(ctx context.Context, input SetRateInput) (*MutationResult, error) {
	if err := validateContentID(input.ContentID); err != nil {
		return nil, err
	}
	status, err := normalizeAppStatus(input.AppStatus)
	if err != nil {
		return nil, err
	}
	if err := validatePercentage("rollout rate", input.RolloutRate); err != nil {
		return nil, err
	}
	if err := validateCountries(input.Countries); err != nil {
		return nil, err
	}

	current, err := service.ViewRate(ctx, input.ContentID, status)
	if err != nil {
		return nil, fmt.Errorf("validate staged rollout advance: %w", err)
	}
	if current.RolloutRate > 0 && input.RolloutRate <= current.RolloutRate {
		return nil, fmt.Errorf(
			"rollout rate must increase from %d to a greater value; requested %d",
			current.RolloutRate,
			input.RolloutRate,
		)
	}
	currentCountries := make(map[string]int, len(current.Countries))
	for _, country := range current.Countries {
		currentCountries[country.CountryCode] = country.RolloutRate
	}
	advancingExisting := status == "SALE" || current.RolloutRate > 0
	for _, country := range input.Countries {
		if previous, exists := currentCountries[country.CountryCode]; exists &&
			country.RolloutRate <= previous {
			return nil, fmt.Errorf(
				"rollout rate for %s must increase from %d to a greater value; requested %d",
				country.CountryCode,
				previous,
				country.RolloutRate,
			)
		}
		if advancingExisting && country.RolloutRate <= input.RolloutRate {
			return nil, fmt.Errorf(
				"rollout rate for %s must be greater than the default rollout rate %d",
				country.CountryCode,
				input.RolloutRate,
			)
		}
	}

	rate := input.RolloutRate
	request := rateRequest{
		ContentID:   input.ContentID,
		Function:    functionEnable,
		AppStatus:   status,
		RolloutRate: &rate,
		Countries:   append([]CountryRate(nil), input.Countries...),
	}
	return service.mutateRate(ctx, request, false)
}

// Complete ends a staged rollout by sending DISABLE_ROLLOUT. Samsung defines
// this operation as deployment to all users globally, not as pausing or
// withdrawing the release. It reads the current rates first because Samsung
// refuses completion while a country rate is higher than the default.
func (service *Service) Complete(ctx context.Context, contentID, appStatus string) (*MutationResult, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	status, err := normalizeAppStatus(appStatus)
	if err != nil {
		return nil, err
	}
	current, err := service.ViewRate(ctx, contentID, status)
	if err != nil {
		return nil, fmt.Errorf("validate staged rollout completion: %w", err)
	}
	for _, country := range current.Countries {
		if country.RolloutRate > current.RolloutRate {
			return nil, fmt.Errorf(
				"cannot complete staged rollout: %s rate %d exceeds default rate %d",
				country.CountryCode,
				country.RolloutRate,
				current.RolloutRate,
			)
		}
	}
	return service.mutateRate(ctx, rateRequest{
		ContentID: contentID,
		Function:  functionDisable,
		AppStatus: status,
	}, true)
}

func (service *Service) mutateRate(
	ctx context.Context,
	request rateRequest,
	completed bool,
) (*MutationResult, error) {
	var response envelope[struct{}]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPut,
		ratePath,
		request,
		&response,
	); err != nil {
		return nil, fmt.Errorf("update staged rollout rate: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("update staged rollout rate: %w", err)
	}
	return &MutationResult{
		ResultCode:    response.ResultCode,
		ResultMessage: response.ResultMessage,
		Function:      request.Function,
		Completed:     completed,
	}, nil
}

// ViewBinaries gets staged rollout binary state for an explicit app variant.
func (service *Service) ViewBinaries(
	ctx context.Context,
	contentID,
	appStatus string,
) (*BinaryList, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	status, err := normalizeAppStatus(appStatus)
	if err != nil {
		return nil, err
	}

	var response envelope[BinaryList]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		endpoint(binaryPath, contentID, status),
		nil,
		&response,
	); err != nil {
		return nil, fmt.Errorf("view staged rollout binaries: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("view staged rollout binaries: %w", err)
	}
	if response.Data.Binaries == nil {
		response.Data.Binaries = []Binary{}
	}
	return &response.Data, nil
}

// AddBinary adds a binary to the staged rollout.
func (service *Service) AddBinary(
	ctx context.Context,
	contentID,
	binarySequence string,
) (*MutationResult, error) {
	return service.updateBinary(ctx, contentID, binarySequence, functionAdd)
}

// RemoveBinary removes a binary from the staged rollout.
func (service *Service) RemoveBinary(
	ctx context.Context,
	contentID,
	binarySequence string,
) (*MutationResult, error) {
	return service.updateBinary(ctx, contentID, binarySequence, functionRemove)
}

func (service *Service) updateBinary(
	ctx context.Context,
	contentID,
	binarySequence,
	function string,
) (*MutationResult, error) {
	if err := validateContentID(contentID); err != nil {
		return nil, err
	}
	if err := validateBinarySequence(binarySequence); err != nil {
		return nil, err
	}

	request := binaryRequest{
		ContentID: contentID,
		Function:  function,
		BinarySeq: binarySequence,
	}
	var response envelope[struct{}]
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPut,
		binaryPath,
		request,
		&response,
	); err != nil {
		return nil, fmt.Errorf("update staged rollout binary: %w", err)
	}
	if err := response.success(); err != nil {
		return nil, fmt.Errorf("update staged rollout binary: %w", err)
	}
	return &MutationResult{
		ResultCode:    response.ResultCode,
		ResultMessage: response.ResultMessage,
		Function:      function,
	}, nil
}

func endpoint(path, contentID, appStatus string) string {
	query := make(url.Values)
	query.Set("contentId", contentID)
	query.Set("appStatus", appStatus)
	return path + "?" + query.Encode()
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

func validateCountries(countries []CountryRate) error {
	seen := make(map[string]bool, len(countries))
	for _, country := range countries {
		if len(country.CountryCode) != 3 || country.CountryCode != strings.ToUpper(country.CountryCode) {
			return fmt.Errorf("country code %q must contain exactly three uppercase ASCII letters", country.CountryCode)
		}
		for _, character := range country.CountryCode {
			if character < 'A' || character > 'Z' {
				return fmt.Errorf("country code %q must contain exactly three uppercase ASCII letters", country.CountryCode)
			}
		}
		if seen[country.CountryCode] {
			return fmt.Errorf("country code %q is duplicated", country.CountryCode)
		}
		seen[country.CountryCode] = true
		if err := validatePercentage("rollout rate for "+country.CountryCode, country.RolloutRate); err != nil {
			return err
		}
	}
	return nil
}

func validatePercentage(label string, value int) error {
	if value < 1 || value > 100 {
		return fmt.Errorf("%s must be between 1 and 100", label)
	}
	return nil
}

func validateBinarySequence(sequence string) error {
	if sequence == "" || sequence != strings.TrimSpace(sequence) {
		return errors.New("binary sequence must be a positive integer")
	}
	value, err := strconv.Atoi(sequence)
	if err != nil || value <= 0 {
		return errors.New("binary sequence must be a positive integer")
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
