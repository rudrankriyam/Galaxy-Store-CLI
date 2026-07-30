package receiptscmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/receipts"
)

type fakeService struct {
	result      *receipts.Receipt
	err         error
	verifyCalls int
	purchaseID  string
}

func (service *fakeService) Verify(
	_ context.Context,
	purchaseID string,
) (*receipts.Receipt, error) {
	service.verifyCalls++
	service.purchaseID = purchaseID
	return service.result, service.err
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "receipts" || len(command.Subcommands) != 1 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "verify" {
		t.Fatalf("subcommand = %q, want verify", command.Subcommands[0].Name)
	}
	if command.Subcommands[0].FlagSet.Lookup("profile") != nil {
		t.Fatal("unauthenticated receipt verification must not expose --profile")
	}
}

func TestVerifyPassesPurchaseIDAndPreservesJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{result: &receipts.Receipt{
		ItemID:              "item-1",
		PaymentID:           "payment-1",
		OrderID:             "order-1",
		PackageName:         "com.example.app",
		Status:              receipts.StatusSuccess,
		PaymentAmount:       "9.99",
		CurrencyCode:        "USD",
		AcknowledgeYN:       "Y",
		ObfuscatedAccountID: "account",
	}}
	var openCalls int
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		OpenService: func() (Service, error) {
			openCalls++
			return service, nil
		},
	}

	if err := execute(
		NewCommand(dependencies),
		"verify",
		"--purchase-id", "private-purchase-id",
		"--output", "json",
	); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if openCalls != 1 || service.verifyCalls != 1 ||
		service.purchaseID != "private-purchase-id" {
		t.Fatalf(
			"open calls = %d, verify calls = %d, ID = %q",
			openCalls,
			service.verifyCalls,
			service.purchaseID,
		)
	}
	for _, expected := range []string{
		`"itemId":"item-1"`,
		`"status":"success"`,
		`"obfuscatedAccountId":"account"`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("JSON missing %q: %s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "private-purchase-id") {
		t.Fatalf("output leaked purchase ID: %s", stdout.String())
	}
}

func TestVerifyTableAndMarkdownHaveUsefulColumns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		format   string
		expected []string
	}{
		{
			name:   "table",
			format: "table",
			expected: []string{
				"STATUS",
				"ITEM ID",
				"PACKAGE",
				"PAYMENT",
				"success",
				"com.example.app",
				"9.99 USD",
			},
		},
		{
			name:   "markdown",
			format: "markdown",
			expected: []string{
				"| STATUS | ITEM ID | PACKAGE | ITEM TYPE | ORDER ID | PAYMENT ID | PAYMENT | MODE | CONSUMED | ACKNOWLEDGED | PURCHASE DATE | CANCEL DATE | ERROR CODE | ERROR |",
				"| success | item-1 | com.example.app | Item | order-1 | payment-1 | 9.99 USD | PRODUCTION | N | Y | 2026-07-30 10:00:00 |  |  |  |",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			service := &fakeService{result: &receipts.Receipt{
				ItemID:        "item-1",
				PaymentID:     "payment-1",
				OrderID:       "order-1",
				PackageName:   "com.example.app",
				ItemType:      "Item",
				Status:        receipts.StatusSuccess,
				PaymentAmount: "9.99",
				CurrencyCode:  "USD",
				Mode:          "PRODUCTION",
				ConsumeYN:     "N",
				AcknowledgeYN: "Y",
				PurchaseDate:  "2026-07-30 10:00:00",
			}}
			dependencies := testDependencies(&stdout, service)

			if err := execute(
				NewCommand(dependencies),
				"verify",
				"--purchase-id", "purchase-id",
				"--output", test.format,
			); err != nil {
				t.Fatalf("verify: %v", err)
			}
			for _, expected := range test.expected {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("%s missing %q:\n%s", test.format, expected, stdout.String())
				}
			}
		})
	}
}

func TestValidationRunsBeforeServiceConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "positional", args: []string{"verify", "--purchase-id", "purchase-id", "extra"}},
		{name: "missing ID", args: []string{"verify"}},
		{name: "padded ID", args: []string{"verify", "--purchase-id", " purchase-id "}},
		{name: "whitespace ID", args: []string{"verify", "--purchase-id", "purchase id"}},
		{name: "control ID", args: []string{"verify", "--purchase-id", "purchase\nid"}},
		{name: "oversized ID", args: []string{"verify", "--purchase-id", strings.Repeat("x", maxPurchaseIDBytes+1)}},
		{name: "bad output", args: []string{"verify", "--purchase-id", "purchase-id", "--output", "yaml"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				OpenService: func() (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want *shared.UsageError", err, err)
			}
			if openCalls != 0 {
				t.Fatalf("service opened %d times", openCalls)
			}
		})
	}
}

func TestVerifyRedactsPurchaseIDFromServiceErrors(t *testing.T) {
	t.Parallel()

	const purchaseID = "private-purchase-id"
	service := &fakeService{
		err: errors.New("verification failed for " + purchaseID),
	}
	dependencies := testDependencies(io.Discard, service)
	err := execute(
		NewCommand(dependencies),
		"verify",
		"--purchase-id", purchaseID,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), purchaseID) {
		t.Fatalf("error leaked purchase ID: %v", err)
	}
}

func TestFailedAndCanceledReceiptsPrintDataAndReturnFailure(t *testing.T) {
	t.Parallel()

	tests := []string{receipts.StatusFail, receipts.StatusCancel}
	for _, status := range tests {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			const purchaseID = "private-purchase-id"
			var stdout bytes.Buffer
			service := &fakeService{result: &receipts.Receipt{
				Status:       status,
				ErrorCode:    9135,
				ErrorMessage: "not valid for " + purchaseID,
			}}
			dependencies := testDependencies(&stdout, service)

			err := execute(
				NewCommand(dependencies),
				"verify",
				"--purchase-id", purchaseID,
				"--output", "json",
			)
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("error = %v, want status %q", err, status)
			}
			if strings.Contains(err.Error(), purchaseID) ||
				strings.Contains(stdout.String(), purchaseID) {
				t.Fatalf("purchase ID leaked; error=%v output=%s", err, stdout.String())
			}
			for _, expected := range []string{
				`"status":"` + status + `"`,
				`"errorCode":9135`,
				`"errorMessage":"not valid for [REDACTED]"`,
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("JSON missing %q: %s", expected, stdout.String())
				}
			}
		})
	}
}

func TestServiceAndPrinterFailuresAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("service unavailable")
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		OpenService: func() (Service, error) {
			return nil, sentinel
		},
	}
	if err := execute(
		NewCommand(dependencies),
		"verify",
		"--purchase-id", "purchase-id",
	); !errors.Is(err, sentinel) {
		t.Fatalf("open error = %v, want sentinel", err)
	}

	service := &fakeService{result: &receipts.Receipt{Status: receipts.StatusSuccess}}
	dependencies = testDependencies(io.Discard, service)
	dependencies.Printer = errorPrinter{err: sentinel}
	if err := execute(
		NewCommand(dependencies),
		"verify",
		"--purchase-id", "purchase-id",
	); !errors.Is(err, sentinel) {
		t.Fatalf("printer error = %v, want sentinel", err)
	}
}

type errorPrinter struct {
	err error
}

func (printer errorPrinter) Print(output.Format, any) error {
	return printer.err
}

func testDependencies(stdout io.Writer, service Service) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		OpenService: func() (Service, error) {
			return service, nil
		},
	}
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
