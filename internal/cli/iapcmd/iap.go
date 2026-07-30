// Package iapcmd implements Samsung IAP transaction commands.
package iapcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/orders"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/purchases"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/subscriptions"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// PurchaseService processes completed Samsung IAP purchases.
type PurchaseService interface {
	Consume(context.Context, purchases.Request) (*purchases.Result, error)
	Acknowledge(context.Context, purchases.Request) (*purchases.Result, error)
}

// SubscriptionService reads and changes Samsung IAP subscriptions.
type SubscriptionService interface {
	GetStatus(context.Context, subscriptions.Reference) (*subscriptions.Status, error)
	Cancel(context.Context, subscriptions.Reference, subscriptions.Caller) (*subscriptions.ActionResult, error)
	Refund(context.Context, subscriptions.Reference) (*subscriptions.ActionResult, error)
	Revoke(context.Context, subscriptions.Reference) (*subscriptions.ActionResult, error)
}

// OrderService reads Samsung IAP payment and refund records.
type OrderService interface {
	List(context.Context, orders.ListOptions) (*orders.Result, error)
}

// Services groups the IAP API surfaces created from one authenticated session.
type Services struct {
	Purchases     PurchaseService
	Subscriptions SubscriptionService
	Orders        OrderService
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps IAP commands deterministic and testable.
type Dependencies struct {
	Stderr       io.Writer
	Printer      Printer
	OpenServices func(profile string) (Services, error)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until local validation and mutation confirmation have succeeded.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
) (Dependencies, error) {
	factory, err := session.DefaultFactory()
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		Stderr:  stderr,
		Printer: output.NewPrinter(stdout, isTerminal),
		OpenServices: func(profile string) (Services, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return Services{}, openErr
			}
			purchaseService, purchaseErr := purchases.New(active.Client)
			if purchaseErr != nil {
				return Services{}, purchaseErr
			}
			subscriptionService, subscriptionErr := subscriptions.New(active.Client)
			if subscriptionErr != nil {
				return Services{}, subscriptionErr
			}
			orderService, orderErr := orders.New(active.Client)
			if orderErr != nil {
				return Services{}, orderErr
			}
			return Services{
				Purchases:     purchaseService,
				Subscriptions: subscriptionService,
				Orders:        orderService,
			}, nil
		},
	}, nil
}

// NewCommand creates the gsc iap command group for purchase processing,
// subscription management, and order reporting.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	command := &ffcli.Command{
		Name:       "iap",
		ShortUsage: "gsc iap <command> [flags]",
		ShortHelp:  "Process purchases, manage subscriptions, and inspect IAP orders.",
		Subcommands: []*ffcli.Command{
			newPurchasesCommand(dependencies, stderr),
			newSubscriptionsCommand(dependencies, stderr),
			newOrdersCommand(dependencies, stderr),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("iap requires a command: purchases, subscriptions, or orders")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func validateDependencies(dependencies Dependencies) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("iap command output printer is not configured")
	case dependencies.OpenServices == nil:
		return errors.New("iap command session factory is not configured")
	default:
		return nil
	}
}

func openServices(dependencies Dependencies, profile string) (Services, error) {
	services, err := dependencies.OpenServices(strings.TrimSpace(profile))
	if err != nil {
		return Services{}, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	return services, nil
}

type mutationPlanOutput struct {
	Action          string       `json:"action"`
	PackageName     string       `json:"packageName"`
	PurchaseID      string       `json:"purchaseId"`
	PurchasedIDList []string     `json:"purchasedIdList,omitempty"`
	Caller          string       `json:"caller,omitempty"`
	DryRun          bool         `json:"dryRun"`
	Plan            *shared.Plan `json:"plan"`
}

func (result mutationPlanOutput) OutputHeaders() []string {
	return []string{"ACTION", "PACKAGE", "PURCHASE ID", "STATUS"}
}

func (result mutationPlanOutput) OutputRows() [][]string {
	return [][]string{{
		result.Action,
		result.PackageName,
		result.PurchaseID,
		"planned",
	}}
}

type purchaseOutput struct {
	result *purchases.Result
}

func (result purchaseOutput) MarshalJSON() ([]byte, error) {
	if result.result == nil {
		return []byte("null"), nil
	}
	return json.Marshal(result.result)
}

func (result purchaseOutput) OutputHeaders() []string {
	return []string{"PURCHASE ID", "STATUS CODE", "STATUS"}
}

func (result purchaseOutput) OutputRows() [][]string {
	if result.result == nil {
		return nil
	}
	rows := make([][]string, len(result.result.PurchaseItemList))
	for index, item := range result.result.PurchaseItemList {
		rows[index] = []string{item.PurchaseID, item.StatusCode, item.StatusString}
	}
	return rows
}

type subscriptionStatusOutput struct {
	status *subscriptions.Status
}

func (result subscriptionStatusOutput) MarshalJSON() ([]byte, error) {
	if result.status == nil {
		return []byte("null"), nil
	}
	return json.Marshal(result.status)
}

func (result subscriptionStatusOutput) OutputHeaders() []string {
	return []string{"ITEM ID", "STATUS", "ENDS", "COUNTRY", "LATEST ORDER", "PLAN"}
}

func (result subscriptionStatusOutput) OutputRows() [][]string {
	if result.status == nil {
		return nil
	}
	return [][]string{{
		result.status.ItemID,
		result.status.SubscriptionStatus,
		result.status.SubscriptionEndDate,
		result.status.CountryCode,
		result.status.LatestOrderID,
		result.status.CurrentPaymentPlan,
	}}
}

type subscriptionActionOutput struct {
	result *subscriptions.ActionResult
}

func (result subscriptionActionOutput) MarshalJSON() ([]byte, error) {
	if result.result == nil {
		return []byte("null"), nil
	}
	return json.Marshal(result.result)
}

func (result subscriptionActionOutput) OutputHeaders() []string {
	return []string{"CODE", "MESSAGE"}
}

func (result subscriptionActionOutput) OutputRows() [][]string {
	if result.result == nil {
		return nil
	}
	return [][]string{{result.result.Code, result.result.Message}}
}

type ordersOutput struct {
	result *orders.Result
}

func (result ordersOutput) MarshalJSON() ([]byte, error) {
	if result.result == nil {
		return []byte("null"), nil
	}
	return json.Marshal(result.result)
}

func (result ordersOutput) OutputHeaders() []string {
	return []string{"ORDER ID", "PURCHASE ID", "PACKAGE", "ITEM", "STATUS", "USD PRICE", "ORDER TIME"}
}

func (result ordersOutput) OutputRows() [][]string {
	if result.result == nil {
		return nil
	}
	rows := make([][]string, len(result.result.OrderItemList))
	for index, order := range result.result.OrderItemList {
		rows[index] = []string{
			order.OrderID,
			order.PurchaseID,
			order.PackageName,
			order.ItemID,
			order.Status,
			order.USDPrice,
			order.OrderTime,
		}
	}
	return rows
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var (
	_ output.RowSource = mutationPlanOutput{}
	_ output.RowSource = purchaseOutput{}
	_ output.RowSource = subscriptionStatusOutput{}
	_ output.RowSource = subscriptionActionOutput{}
	_ output.RowSource = ordersOutput{}
)
