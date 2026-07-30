// Package receiptscmd implements unauthenticated Samsung IAP receipt commands.
package receiptscmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/receipts"
)

const maxPurchaseIDBytes = 1024

// Service is the unauthenticated Samsung IAP receipt API used by commands.
type Service interface {
	Verify(context.Context, string) (*receipts.Receipt, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps receipt commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenService func() (Service, error)
}

// DefaultDependencies creates production receipt dependencies. No Galaxy Store
// profile, access token, or service account is resolved.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
) Dependencies {
	return Dependencies{
		Stderr:  stderr,
		Printer: output.NewPrinter(stdout, isTerminal),
		OpenService: func() (Service, error) {
			return receipts.New(nil)
		},
	}
}

// NewCommand creates the receipts group intended for gsc iap receipts.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	verify := newVerifyCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "receipts",
		ShortUsage:  "gsc iap receipts <command> [flags]",
		ShortHelp:   "Verify Samsung IAP purchase receipts.",
		Subcommands: []*ffcli.Command{verify},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("iap receipts requires a command: verify")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newVerifyCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap receipts verify", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var purchaseID string
	var outputValue string
	flags.StringVar(&purchaseID, "purchase-id", "", "Required Samsung IAP purchase ID")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "verify",
		ShortUsage: "gsc iap receipts verify --purchase-id ID [--output FORMAT]",
		ShortHelp:  "Verify one purchase against Samsung's public receipt endpoint.",
		LongHelp: `Verify one purchase against Samsung's public receipt endpoint.

Receipt verification is an unauthenticated HTTPS request to Samsung IAP. It
does not read a credential profile or send an access token.

Examples:
  gsc iap receipts verify --purchase-id PURCHASE_ID
  gsc iap receipts verify --purchase-id PURCHASE_ID --output table`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap receipts verify does not accept positional arguments")
			}
			return runVerify(ctx, dependencies, verifyOptions{
				PurchaseID: purchaseID,
				Output:     outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type verifyOptions struct {
	PurchaseID string
	Output     string
}

func runVerify(
	ctx context.Context,
	dependencies Dependencies,
	options verifyOptions,
) error {
	if err := validatePurchaseID(options.PurchaseID); err != nil {
		return err
	}
	format, err := output.ParseFormat(options.Output)
	if err != nil {
		return shared.UsageErrorf("%v", err)
	}
	switch {
	case dependencies.Printer == nil:
		return errors.New("receipt command output printer is not configured")
	case dependencies.OpenService == nil:
		return errors.New("receipt command service factory is not configured")
	}

	service, err := dependencies.OpenService()
	if err != nil {
		return fmt.Errorf("open Samsung IAP receipt service: %w", err)
	}
	if service == nil {
		return errors.New("open Samsung IAP receipt service: service is nil")
	}
	receipt, err := service.Verify(ctx, options.PurchaseID)
	if err != nil {
		return redactError(err, options.PurchaseID)
	}
	if receipt == nil {
		return errors.New("samsung returned an invalid receipt response")
	}
	safeReceipt := *receipt
	safeReceipt.ErrorMessage = redactText(safeReceipt.ErrorMessage, options.PurchaseID)
	if err := dependencies.Printer.Print(
		format,
		receiptResult{Receipt: &safeReceipt},
	); err != nil {
		return err
	}
	if !safeReceipt.Successful() {
		return fmt.Errorf(
			"samsung IAP receipt verification returned status %s",
			safeReceipt.Status,
		)
	}
	return nil
}

func validatePurchaseID(purchaseID string) error {
	if err := shared.RequireValue("--purchase-id", purchaseID); err != nil {
		return err
	}
	if purchaseID != strings.TrimSpace(purchaseID) {
		return shared.UsageErrorf("--purchase-id must not contain surrounding whitespace")
	}
	if len(purchaseID) > maxPurchaseIDBytes {
		return shared.UsageErrorf("--purchase-id is too long")
	}
	for _, character := range purchaseID {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return shared.UsageErrorf("--purchase-id must not contain whitespace or control characters")
		}
	}
	return nil
}

type receiptResult struct {
	*receipts.Receipt
}

func (result receiptResult) OutputHeaders() []string {
	return []string{
		"STATUS",
		"ITEM ID",
		"PACKAGE",
		"ITEM TYPE",
		"ORDER ID",
		"PAYMENT ID",
		"PAYMENT",
		"MODE",
		"CONSUMED",
		"ACKNOWLEDGED",
		"PURCHASE DATE",
		"CANCEL DATE",
		"ERROR CODE",
		"ERROR",
	}
}

func (result receiptResult) OutputRows() [][]string {
	if result.Receipt == nil {
		return nil
	}
	errorCode := ""
	if result.ErrorCode != 0 {
		errorCode = fmt.Sprintf("%d", result.ErrorCode)
	}
	return [][]string{{
		result.Status,
		result.ItemID,
		result.PackageName,
		result.ItemType,
		result.OrderID,
		result.PaymentID,
		strings.TrimSpace(strings.Join([]string{result.PaymentAmount, result.CurrencyCode}, " ")),
		result.Mode,
		result.ConsumeYN,
		result.AcknowledgeYN,
		result.PurchaseDate,
		result.CancelDate,
		errorCode,
		result.ErrorMessage,
	}}
}

type secretSafeError struct {
	err     error
	message string
}

func (err *secretSafeError) Error() string {
	return err.message
}

func (err *secretSafeError) Unwrap() error {
	return err.err
}

func redactError(err error, purchaseID string) error {
	if err == nil {
		return nil
	}
	message := redactText(err.Error(), purchaseID)
	return &secretSafeError{err: err, message: message}
}

func redactText(message string, purchaseID string) string {
	for _, secret := range []string{
		purchaseID,
		url.QueryEscape(purchaseID),
	} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var _ output.RowSource = receiptResult{}
