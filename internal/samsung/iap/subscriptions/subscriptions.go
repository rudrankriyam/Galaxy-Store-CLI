// Package subscriptions manages and inspects Samsung IAP subscriptions.
package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/internal/validate"
)

const subscriptionPathPrefix = "/iap/seller/v6/applications/"

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(context.Context, string, string, any, any) (*http.Response, error)
}

// Service reads and changes Samsung IAP subscriptions.
type Service struct {
	client JSONClient
}

// New creates a subscription service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// Reference identifies one subscription purchase.
type Reference struct {
	PackageName string
	PurchaseID  string
}

// Caller controls whether a canceled subscription can be resumed during its
// remaining paid period.
type Caller string

const (
	// CallerDefault omits caller and uses Samsung's admin default.
	CallerDefault Caller = ""
	// CallerAdmin prevents the customer from resubscribing before period end.
	CallerAdmin Caller = "admin"
	// CallerUser allows the customer to resubscribe before period end.
	CallerUser Caller = "user"
)

// Price is Samsung's current subscription price projection.
type Price struct {
	LocalCurrencyCode string      `json:"localCurrencyCode"`
	LocalPrice        json.Number `json:"localPrice"`
	SupplyPrice       json.Number `json:"supplyPrice"`
}

// NewPrice is the price nested inside a pending or applied price change.
type NewPrice struct {
	LocalCurrencyCode string      `json:"localCurrencyCode"`
	LocalPrice        json.Number `json:"localPrice"`
}

// PriceChange describes a subscription price transition.
type PriceChange struct {
	PriceChangeMode            string    `json:"priceChangeMode"`
	PriceChangeStatus          string    `json:"priceChangeStatus"`
	PriceChangeConsentDate     *string   `json:"priceChangeConsentDate"`
	ExpectedNewPriceChargeDate string    `json:"expectedNewPriceChargeDate"`
	NewPrice                   *NewPrice `json:"newPrice"`
}

// Status is Samsung's subscription status response. Raw preserves fields added
// by Samsung without losing them from JSON output.
type Status struct {
	SubscriptionPurchaseDate    string          `json:"subscriptionPurchaseDate"`
	SubscriptionEndDate         string          `json:"subscriptionEndDate"`
	SubscriptionStatus          string          `json:"subscriptionStatus"`
	SubscriptionFirstPurchaseID string          `json:"subscriptionFirstPurchaseID"`
	CountryCode                 string          `json:"countryCode"`
	Price                       *Price          `json:"price"`
	ItemID                      string          `json:"itemID"`
	FreeTrial                   string          `json:"freeTrial"`
	RealMode                    string          `json:"realMode"`
	LatestOrderID               string          `json:"latestOrderId"`
	LatestRenewalDate           string          `json:"latestRenewalDate"`
	TotalNumberOfTieredPayment  string          `json:"totalNumberOfTieredPayment"`
	CurrentPaymentPlan          string          `json:"currentPaymentPlan"`
	TotalNumberOfRenewalPayment string          `json:"totalNumberOfRenewalPayment"`
	CancelSubscriptionDate      string          `json:"cancelSubscriptionDate"`
	CancelSubscriptionReason    string          `json:"cancelSubscriptionReason"`
	GracePeriodYN               string          `json:"gracePeriodYN"`
	GracePeriodEndDate          *string         `json:"gracePeriodEndDate"`
	PriceChange                 *PriceChange    `json:"priceChange"`
	Raw                         json.RawMessage `json:"-"`
}

// UnmarshalJSON records the original status while populating stable fields.
func (status *Status) UnmarshalJSON(data []byte) error {
	type alias Status
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*status = Status(decoded)
	status.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits Samsung's complete original status when available.
func (status Status) MarshalJSON() ([]byte, error) {
	if len(status.Raw) != 0 {
		if !json.Valid(status.Raw) {
			return nil, errors.New("subscription status raw response is invalid JSON")
		}
		return append([]byte(nil), status.Raw...), nil
	}
	type alias Status
	return json.Marshal(alias(status))
}

// ActionResult is returned by cancel, refund, and revoke operations.
type ActionResult struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Raw     json.RawMessage `json:"-"`
}

// UnmarshalJSON records the original action result.
func (result *ActionResult) UnmarshalJSON(data []byte) error {
	type alias ActionResult
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*result = ActionResult(decoded)
	result.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits Samsung's complete original action result when available.
func (result ActionResult) MarshalJSON() ([]byte, error) {
	if len(result.Raw) != 0 {
		if !json.Valid(result.Raw) {
			return nil, errors.New("subscription action raw response is invalid JSON")
		}
		return append([]byte(nil), result.Raw...), nil
	}
	type alias ActionResult
	return json.Marshal(alias(result))
}

// GetStatus returns current subscription and purchase information.
func (service *Service) GetStatus(ctx context.Context, reference Reference) (*Status, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var status Status
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		subscriptionPath(reference),
		nil,
		&status,
	); err != nil {
		return nil, fmt.Errorf("get Samsung IAP subscription status: %w", err)
	}
	return &status, nil
}

// Cancel stops renewal after the current subscription period.
func (service *Service) Cancel(
	ctx context.Context,
	reference Reference,
	caller Caller,
) (*ActionResult, error) {
	if caller != CallerDefault && caller != CallerAdmin && caller != CallerUser {
		return nil, errors.New("subscription caller must be admin or user")
	}
	return service.apply(ctx, cancelAction, reference, caller)
}

// Refund reimburses the most recent payment without ending the subscription.
func (service *Service) Refund(ctx context.Context, reference Reference) (*ActionResult, error) {
	return service.apply(ctx, refundAction, reference, CallerDefault)
}

// Revoke immediately ends the subscription and refunds its most recent payment.
func (service *Service) Revoke(ctx context.Context, reference Reference) (*ActionResult, error) {
	return service.apply(ctx, revokeAction, reference, CallerDefault)
}

type action string

const (
	cancelAction action = "cancel"
	refundAction action = "refund"
	revokeAction action = "revoke"
)

type actionRequest struct {
	Action action `json:"action"`
	Caller Caller `json:"caller,omitempty"`
}

func (service *Service) apply(
	ctx context.Context,
	selected action,
	reference Reference,
	caller Caller,
) (*ActionResult, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var result ActionResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPatch,
		subscriptionPath(reference),
		actionRequest{Action: selected, Caller: caller},
		&result,
	); err != nil {
		return nil, fmt.Errorf("%s Samsung IAP subscription: %w", selected, err)
	}
	return &result, nil
}

func subscriptionPath(reference Reference) string {
	return subscriptionPathPrefix + reference.PackageName +
		"/purchases/subscriptions/" + reference.PurchaseID
}

func validateReference(reference Reference) error {
	if err := validate.PackageName(reference.PackageName); err != nil {
		return err
	}
	return validate.PurchaseID(reference.PurchaseID)
}
