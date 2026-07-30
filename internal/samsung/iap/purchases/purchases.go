// Package purchases consumes and acknowledges Samsung IAP purchases.
package purchases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/internal/validate"
)

const purchasePathPrefix = "/iap/v6/applications/"

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
}

// Service processes completed Samsung IAP purchases.
type Service struct {
	client JSONClient
}

// New creates a purchase processing service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// Request identifies the purchase in the endpoint and optionally supplies
// additional purchases for Samsung's batch form.
type Request struct {
	PackageName     string
	PurchaseID      string
	PurchasedIDList []string
}

// Item is one per-purchase result returned by Samsung.
type Item struct {
	PurchaseID   string `json:"purchaseId"`
	StatusCode   string `json:"statusCode"`
	StatusString string `json:"statusString"`
}

// Result is Samsung's purchase-processing response. Raw preserves all current
// and future response fields for lossless JSON output.
type Result struct {
	TotalCount       int             `json:"totalCount"`
	PurchaseItemList []Item          `json:"purchaseItemList"`
	Raw              json.RawMessage `json:"-"`
}

// UnmarshalJSON records the original response while populating stable fields.
func (result *Result) UnmarshalJSON(data []byte) error {
	type alias Result
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*result = Result(decoded)
	result.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits Samsung's complete original response when available.
func (result Result) MarshalJSON() ([]byte, error) {
	if len(result.Raw) != 0 {
		if !json.Valid(result.Raw) {
			return nil, errors.New("purchase result raw response is invalid JSON")
		}
		return append([]byte(nil), result.Raw...), nil
	}
	type alias Result
	return json.Marshal(alias(result))
}

// Consume reports one or more consumable purchases as consumed.
func (service *Service) Consume(ctx context.Context, request Request) (*Result, error) {
	return service.process(ctx, consumeAction, request)
}

// Acknowledge confirms that subscription entitlement was granted.
func (service *Service) Acknowledge(ctx context.Context, request Request) (*Result, error) {
	return service.process(ctx, acknowledgeAction, request)
}

type action string

const (
	consumeAction     action = "consume"
	acknowledgeAction action = "acknowledge"
)

type processRequest struct {
	Action          action   `json:"action"`
	PurchasedIDList []string `json:"purchasedIdList,omitempty"`
}

func (service *Service) process(ctx context.Context, selected action, request Request) (*Result, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}

	body := processRequest{
		Action:          selected,
		PurchasedIDList: append([]string(nil), request.PurchasedIDList...),
	}
	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPatch,
		purchasePathPrefix+request.PackageName+"/purchases/"+request.PurchaseID,
		body,
		&result,
	); err != nil {
		return nil, fmt.Errorf("%s Samsung IAP purchase: %w", selected, err)
	}
	return &result, nil
}

func validateRequest(request Request) error {
	if err := validate.PackageName(request.PackageName); err != nil {
		return err
	}
	if err := validate.PurchaseID(request.PurchaseID); err != nil {
		return err
	}

	for _, purchaseID := range request.PurchasedIDList {
		if err := validate.PurchaseID(purchaseID); err != nil {
			return fmt.Errorf("purchased ID list: %w", err)
		}
	}
	return nil
}
