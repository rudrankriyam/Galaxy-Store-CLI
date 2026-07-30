// Package orders reads Samsung IAP payment and refund records.
package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/internal/validate"
)

const ordersPath = "/iap/seller/orders"

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
}

// Service reads Samsung IAP orders.
type Service struct {
	client JSONClient
}

// New creates an order service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// ListOptions selects a date, application, and page. Although Samsung uses
// POST, this operation is read-only.
type ListOptions struct {
	SellerSequence    string
	PackageName       string
	RequestDate       string
	ContinuationToken string
}

type listRequest struct {
	SellerSequence    string `json:"sellerSeq"`
	PackageName       string `json:"packageName,omitempty"`
	RequestDate       string `json:"requestDate,omitempty"`
	ContinuationToken string `json:"continuationToken,omitempty"`
}

// Order is the stable projection of a payment or refund entry.
type Order struct {
	OrderID              string `json:"orderId"`
	PurchaseID           string `json:"purchaseId"`
	ContentID            string `json:"contentId"`
	CountryID            string `json:"countryId"`
	PackageName          string `json:"packageName"`
	ItemID               string `json:"itemId"`
	ItemTitle            string `json:"itemTitle"`
	Status               string `json:"status"`
	OrderTime            string `json:"orderTime"`
	CompletionTime       string `json:"completionTime"`
	RefundTime           string `json:"refundTime"`
	LocalCurrency        string `json:"localCurrency"`
	LocalCurrencyCode    string `json:"localCurrencyCode"`
	LocalPrice           string `json:"localPrice"`
	USDPrice             string `json:"usdPrice"`
	ExchangeRate         string `json:"exchangeRate"`
	MCC                  string `json:"mcc"`
	SubscriptionOrderID  string `json:"subscriptionOrderId"`
	FreeTrialYN          string `json:"freeTrialYN"`
	TieredSubscriptionYN string `json:"tieredSubscriptionYN"`
}

// Result is one Samsung Orders API page. Raw preserves unknown response fields.
type Result struct {
	ContinuationToken *string         `json:"continuationToken"`
	OrderItemList     []Order         `json:"orderItemList"`
	Raw               json.RawMessage `json:"-"`
}

// UnmarshalJSON records the original page while populating stable fields.
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

// MarshalJSON emits Samsung's complete original page when available.
func (result Result) MarshalJSON() ([]byte, error) {
	if len(result.Raw) != 0 {
		if !json.Valid(result.Raw) {
			return nil, errors.New("orders result raw response is invalid JSON")
		}
		return append([]byte(nil), result.Raw...), nil
	}
	type alias Result
	return json.Marshal(alias(result))
}

// List returns up to 100 payment and refund records. Call again with the
// returned continuation token to read the next page.
func (service *Service) List(ctx context.Context, options ListOptions) (*Result, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}

	body := listRequest(options)
	var result Result
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		ordersPath,
		body,
		&result,
	); err != nil {
		return nil, fmt.Errorf("list Samsung IAP orders: %w", err)
	}
	return &result, nil
}

func validateOptions(options ListOptions) error {
	if err := validate.SellerSequence(options.SellerSequence); err != nil {
		return err
	}
	if options.PackageName != "" {
		if err := validate.PackageName(options.PackageName); err != nil {
			return err
		}
	}
	if err := validate.RequestDate(options.RequestDate); err != nil {
		return err
	}
	return validate.ContinuationToken(options.ContinuationToken)
}
