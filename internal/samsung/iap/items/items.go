// Package items manages one-time products through Samsung's IAP Publish API.
//
// The Publish API does not manage subscription products. Mutations take effect
// immediately, including while an app is for sale.
package items

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	iapvalidate "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/internal/validate"
)

const itemsPathPrefix = "/iap/v6/applications/"

const (
	// TypeItem is the only product type Samsung currently accepts for creation.
	TypeItem = "ITEM"

	// StatusPublished makes an item available for sale.
	StatusPublished = "PUBLISHED"
	// StatusUnpublished makes an item unavailable for sale.
	StatusUnpublished = "UNPUBLISHED"
	// StatusRemoved identifies an item removed from Galaxy Store.
	StatusRemoved = "REMOVED"
)

var decimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(
		context.Context,
		string,
		string,
		any,
		any,
	) (*http.Response, error)
}

// Service manages an app's one-time IAP item catalog.
type Service struct {
	client JSONClient
}

// New creates an IAP item service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// Item is Samsung's IAP item representation. Raw preserves the full response
// object so callers do not lose fields introduced by the API.
type Item struct {
	ID                string                     `json:"id"`
	Title             string                     `json:"title,omitempty"`
	Description       string                     `json:"description,omitempty"`
	Type              string                     `json:"type,omitempty"`
	Status            string                     `json:"status,omitempty"`
	ItemPaymentMethod *ItemPaymentMethod         `json:"itemPaymentMethod,omitempty"`
	USDPrice          json.Number                `json:"usdPrice,omitempty"`
	Prices            []Price                    `json:"prices,omitempty"`
	Raw               json.RawMessage            `json:"-"`
	UnknownFields     map[string]json.RawMessage `json:"-"`
}

// ItemPaymentMethod describes whether Samsung can automatically charge the
// item through a phone bill.
type ItemPaymentMethod struct {
	PhoneBillStatus bool `json:"phoneBillStatus"`
}

// Price is one territory's item price.
type Price struct {
	CountryID  string `json:"countryId"`
	Currency   string `json:"currency,omitempty"`
	LocalPrice string `json:"localPrice"`
}

// UnmarshalJSON keeps Samsung's complete object while projecting stable fields.
func (item *Item) UnmarshalJSON(data []byte) error {
	type itemWire struct {
		ID                string             `json:"id"`
		Title             string             `json:"title"`
		Description       string             `json:"description"`
		Type              string             `json:"type"`
		Status            string             `json:"status"`
		ItemPaymentMethod *ItemPaymentMethod `json:"itemPaymentMethod"`
		USDPrice          json.Number        `json:"usdPrice"`
		Prices            []Price            `json:"prices"`
	}
	var wire itemWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, known := range []string{
		"id",
		"title",
		"description",
		"type",
		"status",
		"itemPaymentMethod",
		"usdPrice",
		"prices",
	} {
		delete(fields, known)
	}
	if len(fields) == 0 {
		fields = nil
	}

	*item = Item{
		ID:                wire.ID,
		Title:             wire.Title,
		Description:       wire.Description,
		Type:              wire.Type,
		Status:            wire.Status,
		ItemPaymentMethod: wire.ItemPaymentMethod,
		USDPrice:          wire.USDPrice,
		Prices:            wire.Prices,
		Raw:               append(json.RawMessage(nil), data...),
		UnknownFields:     fields,
	}
	return nil
}

// MarshalJSON emits Samsung's original object when Item came from the API.
func (item Item) MarshalJSON() ([]byte, error) {
	if len(item.Raw) != 0 {
		if !json.Valid(item.Raw) {
			return nil, errors.New("item raw response is invalid JSON")
		}
		return append([]byte(nil), item.Raw...), nil
	}
	type itemAlias Item
	return json.Marshal(itemAlias(item))
}

// ListOptions selects a required Samsung server-side page.
type ListOptions struct {
	Page int
	Size int
}

// ListResult is returned by Samsung's item-list endpoint.
type ListResult struct {
	Items      []Item `json:"itemList"`
	TotalCount int    `json:"totalCount"`
}

// FullRequest is the complete create/replace body. Replace overwrites all
// existing item information other than Samsung's internal content ID.
type FullRequest struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Description       string            `json:"description"`
	Type              string            `json:"type,omitempty"`
	Status            string            `json:"status"`
	ItemPaymentMethod ItemPaymentMethod `json:"itemPaymentMethod"`
	USDPrice          json.Number       `json:"usdPrice"`
	Prices            []Price           `json:"prices"`
}

// PatchPrice changes one territory's local price. Samsung's partial-update
// contract does not accept currency.
type PatchPrice struct {
	CountryID  string `json:"countryId"`
	LocalPrice string `json:"localPrice"`
}

// UpdateRequest changes only the fields explicitly represented here. A nil
// Title and nil Prices are omitted rather than sent as destructive zero values.
type UpdateRequest struct {
	ID     string       `json:"id"`
	Title  *string      `json:"title,omitempty"`
	Prices []PatchPrice `json:"prices,omitempty"`
}

// List returns one page of IAP items.
func (service *Service) List(
	ctx context.Context,
	packageName string,
	options ListOptions,
) (*ListResult, error) {
	if err := ValidatePackageName(packageName); err != nil {
		return nil, err
	}
	if options.Page < 1 {
		return nil, errors.New("page must be at least 1")
	}
	if options.Size < 1 {
		return nil, errors.New("size must be at least 1")
	}

	query := make(url.Values)
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("size", strconv.Itoa(options.Size))
	var result ListResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		itemsPath(packageName)+"?"+query.Encode(),
		nil,
		&result,
	); err != nil {
		return nil, fmt.Errorf("list Samsung IAP items: %w", err)
	}
	if result.Items == nil {
		result.Items = []Item{}
	}
	return &result, nil
}

// View returns one IAP item.
func (service *Service) View(
	ctx context.Context,
	packageName string,
	itemID string,
) (*Item, error) {
	if err := validatePackageAndItemID(packageName, itemID); err != nil {
		return nil, err
	}
	var result Item
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		itemPath(packageName, itemID),
		nil,
		&result,
	); err != nil {
		return nil, fmt.Errorf("view Samsung IAP item: %w", err)
	}
	if err := validateResponseID("view Samsung IAP item", result.ID, itemID); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create registers a one-time IAP item.
func (service *Service) Create(
	ctx context.Context,
	packageName string,
	request FullRequest,
) (*Item, error) {
	return service.fullMutation(ctx, http.MethodPost, "create", packageName, request)
}

// Replace replaces all existing information for an IAP item.
func (service *Service) Replace(
	ctx context.Context,
	packageName string,
	request FullRequest,
) (*Item, error) {
	return service.fullMutation(ctx, http.MethodPut, "replace", packageName, request)
}

func (service *Service) fullMutation(
	ctx context.Context,
	method string,
	operation string,
	packageName string,
	request FullRequest,
) (*Item, error) {
	if err := ValidatePackageName(packageName); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}

	var result Item
	if _, err := service.client.DoJSON(
		ctx,
		method,
		itemsPath(packageName),
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("%s Samsung IAP item: %w", operation, err)
	}
	if err := validateResponseID(operation+" Samsung IAP item", result.ID, request.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update applies Samsung's restricted partial update: title and/or local
// territory prices. Other item fields require Replace.
func (service *Service) Update(
	ctx context.Context,
	packageName string,
	request UpdateRequest,
) (*Item, error) {
	if err := ValidatePackageName(packageName); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}

	var result Item
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPatch,
		itemsPath(packageName),
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("update Samsung IAP item: %w", err)
	}
	if err := validateResponseID("update Samsung IAP item", result.ID, request.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete removes one IAP item.
func (service *Service) Delete(
	ctx context.Context,
	packageName string,
	itemID string,
) (*Item, error) {
	if err := validatePackageAndItemID(packageName, itemID); err != nil {
		return nil, err
	}
	var result Item
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodDelete,
		itemPath(packageName, itemID),
		nil,
		&result,
	); err != nil {
		return nil, fmt.Errorf("delete Samsung IAP item: %w", err)
	}
	if err := validateResponseID("delete Samsung IAP item", result.ID, itemID); err != nil {
		return nil, err
	}
	return &result, nil
}

// Validate validates all fields required by create and replace.
func (request FullRequest) Validate() error {
	if err := ValidateItemID(request.ID); err != nil {
		return err
	}
	if strings.TrimSpace(request.Title) == "" {
		return errors.New("item title is required")
	}
	if request.Title != strings.TrimSpace(request.Title) {
		return errors.New("item title must not have surrounding whitespace")
	}
	if strings.TrimSpace(request.Description) == "" {
		return errors.New("item description is required")
	}
	if request.Type != "" && request.Type != TypeItem {
		if request.Type == "NON_CONSUMABLE" {
			return errors.New("item type NON_CONSUMABLE is no longer accepted by Samsung")
		}
		return fmt.Errorf("unsupported item type %q; expected %q", request.Type, TypeItem)
	}
	switch request.Status {
	case StatusPublished, StatusUnpublished, StatusRemoved:
	default:
		return fmt.Errorf(
			"unsupported item status %q; expected PUBLISHED, UNPUBLISHED, or REMOVED",
			request.Status,
		)
	}
	if err := validateUSDPrice(request.USDPrice); err != nil {
		return err
	}
	if len(request.Prices) == 0 {
		return errors.New("at least one item price is required")
	}
	seenCountries := make(map[string]struct{}, len(request.Prices))
	for index, price := range request.Prices {
		if err := validatePrice(price.CountryID, price.Currency, price.LocalPrice); err != nil {
			return fmt.Errorf("price %d: %w", index, err)
		}
		if _, exists := seenCountries[price.CountryID]; exists {
			return fmt.Errorf("price %d: duplicate country ID %q", index, price.CountryID)
		}
		seenCountries[price.CountryID] = struct{}{}
	}
	return nil
}

// Validate validates Samsung's restricted partial-update body.
func (request UpdateRequest) Validate() error {
	if err := ValidateItemID(request.ID); err != nil {
		return err
	}
	if request.Title == nil && request.Prices == nil {
		return errors.New("partial item update requires title or prices")
	}
	if request.Title != nil {
		if strings.TrimSpace(*request.Title) == "" {
			return errors.New("item title cannot be empty")
		}
		if *request.Title != strings.TrimSpace(*request.Title) {
			return errors.New("item title must not have surrounding whitespace")
		}
	}
	if request.Prices != nil && len(request.Prices) == 0 {
		return errors.New("partial item update prices cannot be empty")
	}
	seenCountries := make(map[string]struct{}, len(request.Prices))
	for index, price := range request.Prices {
		if err := validatePrice(price.CountryID, "", price.LocalPrice); err != nil {
			return fmt.Errorf("price %d: %w", index, err)
		}
		if _, exists := seenCountries[price.CountryID]; exists {
			return fmt.Errorf("price %d: duplicate country ID %q", index, price.CountryID)
		}
		seenCountries[price.CountryID] = struct{}{}
	}
	return nil
}

// ValidatePackageName validates an Android-style application package name.
func ValidatePackageName(packageName string) error {
	return iapvalidate.PackageName(packageName)
}

// ValidateItemID accepts opaque identifiers made from URL path-segment-safe
// characters without inventing an undocumented Samsung length limit.
func ValidateItemID(itemID string) error {
	if itemID == "" || itemID != strings.TrimSpace(itemID) {
		return errors.New("item ID is required and must not have surrounding whitespace")
	}
	for _, character := range itemID {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' || character == '~' {
			continue
		}
		return errors.New("item ID contains an invalid character")
	}
	return nil
}

func validatePackageAndItemID(packageName string, itemID string) error {
	if err := ValidatePackageName(packageName); err != nil {
		return err
	}
	return ValidateItemID(itemID)
}

func validateResponseID(operation string, actual string, expected string) error {
	if actual == "" {
		return fmt.Errorf("%s: Samsung returned no item ID", operation)
	}
	if actual != expected {
		return fmt.Errorf("%s: Samsung returned a different item ID", operation)
	}
	return nil
}

func validateUSDPrice(price json.Number) error {
	raw := string(price)
	if !decimalPattern.MatchString(raw) {
		return errors.New("USD price must be a decimal number")
	}
	value, ok := new(big.Rat).SetString(raw)
	maximum := big.NewRat(99999, 100)
	if !ok || value.Sign() < 0 || value.Cmp(maximum) > 0 {
		return errors.New("USD price must be between 0 and 999.99")
	}
	return nil
}

func validatePrice(countryID string, currency string, localPrice string) error {
	if !isThreeLetterCode(countryID) {
		return errors.New("country ID must contain exactly three uppercase ASCII letters")
	}
	if currency != "" && !isThreeLetterCode(currency) {
		return errors.New("currency must contain exactly three uppercase ASCII letters")
	}
	if !decimalPattern.MatchString(localPrice) {
		return errors.New("local price must be a non-negative decimal string")
	}
	return nil
}

func isThreeLetterCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func itemsPath(packageName string) string {
	return itemsPathPrefix + url.PathEscape(packageName) + "/items"
}

func itemPath(packageName string, itemID string) string {
	return itemsPath(packageName) + "/" + url.PathEscape(itemID)
}
