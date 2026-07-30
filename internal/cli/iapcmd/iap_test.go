package iapcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/orders"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/purchases"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/subscriptions"
)

type fakePurchaseService struct {
	consumeCalls      int
	consumeRequest    purchases.Request
	consumeResult     *purchases.Result
	consumeErr        error
	acknowledgeCalls  int
	acknowledgeInput  purchases.Request
	acknowledgeResult *purchases.Result
	acknowledgeErr    error
}

func (service *fakePurchaseService) Consume(
	_ context.Context,
	request purchases.Request,
) (*purchases.Result, error) {
	service.consumeCalls++
	service.consumeRequest = request
	return service.consumeResult, service.consumeErr
}

func (service *fakePurchaseService) Acknowledge(
	_ context.Context,
	request purchases.Request,
) (*purchases.Result, error) {
	service.acknowledgeCalls++
	service.acknowledgeInput = request
	return service.acknowledgeResult, service.acknowledgeErr
}

type fakeSubscriptionService struct {
	statusCalls  int
	statusInput  subscriptions.Reference
	statusResult *subscriptions.Status
	statusErr    error

	cancelCalls  int
	cancelInput  subscriptions.Reference
	cancelCaller subscriptions.Caller
	cancelResult *subscriptions.ActionResult
	cancelErr    error

	refundCalls  int
	refundInput  subscriptions.Reference
	refundResult *subscriptions.ActionResult
	refundErr    error

	revokeCalls  int
	revokeInput  subscriptions.Reference
	revokeResult *subscriptions.ActionResult
	revokeErr    error
}

func (service *fakeSubscriptionService) GetStatus(
	_ context.Context,
	reference subscriptions.Reference,
) (*subscriptions.Status, error) {
	service.statusCalls++
	service.statusInput = reference
	return service.statusResult, service.statusErr
}

func (service *fakeSubscriptionService) Cancel(
	_ context.Context,
	reference subscriptions.Reference,
	caller subscriptions.Caller,
) (*subscriptions.ActionResult, error) {
	service.cancelCalls++
	service.cancelInput = reference
	service.cancelCaller = caller
	return service.cancelResult, service.cancelErr
}

func (service *fakeSubscriptionService) Refund(
	_ context.Context,
	reference subscriptions.Reference,
) (*subscriptions.ActionResult, error) {
	service.refundCalls++
	service.refundInput = reference
	return service.refundResult, service.refundErr
}

func (service *fakeSubscriptionService) Revoke(
	_ context.Context,
	reference subscriptions.Reference,
) (*subscriptions.ActionResult, error) {
	service.revokeCalls++
	service.revokeInput = reference
	return service.revokeResult, service.revokeErr
}

type fakeOrderService struct {
	listCalls  int
	listInput  orders.ListOptions
	listResult *orders.Result
	listErr    error
}

func (service *fakeOrderService) List(
	_ context.Context,
	options orders.ListOptions,
) (*orders.Result, error) {
	service.listCalls++
	service.listInput = options
	return service.listResult, service.listErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "iap" {
		t.Fatalf("name = %q", command.Name)
	}
	if got := subcommandNames(command); !reflect.DeepEqual(
		got,
		[]string{"purchases", "subscriptions", "orders"},
	) {
		t.Fatalf("iap subcommands = %v", got)
	}
	if got := subcommandNames(command.Subcommands[0]); !reflect.DeepEqual(
		got,
		[]string{"consume", "acknowledge"},
	) {
		t.Fatalf("purchase subcommands = %v", got)
	}
	if got := subcommandNames(command.Subcommands[1]); !reflect.DeepEqual(
		got,
		[]string{"status", "cancel", "refund", "revoke"},
	) {
		t.Fatalf("subscription subcommands = %v", got)
	}
	if got := subcommandNames(command.Subcommands[2]); !reflect.DeepEqual(
		got,
		[]string{"list"},
	) {
		t.Fatalf("order subcommands = %v", got)
	}
}

func TestPurchaseCommandsUseDistinctServiceMethodsAndPreserveJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		assertCall func(*testing.T, *fakePurchaseService)
	}{
		{
			name:    "consume",
			command: "consume",
			assertCall: func(t *testing.T, service *fakePurchaseService) {
				t.Helper()
				if service.consumeCalls != 1 || service.acknowledgeCalls != 0 {
					t.Fatalf(
						"consume calls = %d, acknowledge calls = %d",
						service.consumeCalls,
						service.acknowledgeCalls,
					)
				}
				want := purchases.Request{
					PackageName:     "com.example.app",
					PurchaseID:      "purchase-1",
					PurchasedIDList: []string{"purchase-2", "purchase-3"},
				}
				if !reflect.DeepEqual(service.consumeRequest, want) {
					t.Fatalf("request = %+v, want %+v", service.consumeRequest, want)
				}
			},
		},
		{
			name:    "acknowledge",
			command: "acknowledge",
			assertCall: func(t *testing.T, service *fakePurchaseService) {
				t.Helper()
				if service.acknowledgeCalls != 1 || service.consumeCalls != 0 {
					t.Fatalf(
						"acknowledge calls = %d, consume calls = %d",
						service.acknowledgeCalls,
						service.consumeCalls,
					)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			result := rawPurchaseResult(t, `{
				"totalCount":1,
				"purchaseItemList":[{"purchaseId":"purchase-1","statusCode":"0","statusString":"success."}],
				"futureField":"preserved"
			}`)
			service := &fakePurchaseService{
				consumeResult:     result,
				acknowledgeResult: result,
			}
			var openedProfile string
			dependencies := testDependencies(
				&stdout,
				Services{Purchases: service},
				&openedProfile,
				nil,
			)

			err := execute(
				NewCommand(dependencies),
				"purchases", test.command,
				"--package-name", "com.example.app",
				"--purchase-id", "purchase-1",
				"--purchased-id", "purchase-2",
				"--purchased-id", "purchase-3",
				"--profile", "production",
				"--confirm",
				"--output", "json",
			)
			if err != nil {
				t.Fatalf("%s: %v", test.command, err)
			}
			if openedProfile != "production" {
				t.Fatalf("profile = %q", openedProfile)
			}
			test.assertCall(t, service)
			if !strings.Contains(stdout.String(), `"futureField":"preserved"`) {
				t.Fatalf("lossless output = %s", stdout.String())
			}
		})
	}
}

func TestSubscriptionStatusIsReadOnlyAndPreservesJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeSubscriptionService{
		statusResult: rawSubscriptionStatus(t, `{
			"subscriptionStatus":"ACTIVE",
			"subscriptionEndDate":"2026-08-30 12:00:00 GMT",
			"itemID":"monthly",
			"futureField":true
		}`),
	}
	var openedProfile string
	dependencies := testDependencies(
		&stdout,
		Services{Subscriptions: service},
		&openedProfile,
		nil,
	)

	err := execute(
		NewCommand(dependencies),
		"subscriptions", "status",
		"--package-name", "com.example.app",
		"--purchase-id", "purchase-1",
		"--profile", "production",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if openedProfile != "production" || service.statusCalls != 1 {
		t.Fatalf("profile = %q, calls = %d", openedProfile, service.statusCalls)
	}
	if service.statusInput != (subscriptions.Reference{
		PackageName: "com.example.app",
		PurchaseID:  "purchase-1",
	}) {
		t.Fatalf("reference = %+v", service.statusInput)
	}
	if !strings.Contains(stdout.String(), `"futureField":true`) {
		t.Fatalf("lossless output = %s", stdout.String())
	}
}

func TestSubscriptionMutationsUseDistinctMethodsAndCaller(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		extra      []string
		assertCall func(*testing.T, *fakeSubscriptionService)
	}{
		{
			name:  "cancel",
			extra: []string{"--caller", "user"},
			assertCall: func(t *testing.T, service *fakeSubscriptionService) {
				t.Helper()
				if service.cancelCalls != 1 || service.cancelCaller != subscriptions.CallerUser {
					t.Fatalf("cancel calls = %d, caller = %q", service.cancelCalls, service.cancelCaller)
				}
			},
		},
		{
			name: "refund",
			assertCall: func(t *testing.T, service *fakeSubscriptionService) {
				t.Helper()
				if service.refundCalls != 1 {
					t.Fatalf("refund calls = %d", service.refundCalls)
				}
			},
		},
		{
			name: "revoke",
			assertCall: func(t *testing.T, service *fakeSubscriptionService) {
				t.Helper()
				if service.revokeCalls != 1 {
					t.Fatalf("revoke calls = %d", service.revokeCalls)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			result := rawSubscriptionAction(t, `{
				"code":"0000",
				"message":"Success",
				"futureField":"preserved"
			}`)
			service := &fakeSubscriptionService{
				cancelResult: result,
				refundResult: result,
				revokeResult: result,
			}
			dependencies := testDependencies(
				&stdout,
				Services{Subscriptions: service},
				nil,
				nil,
			)
			args := []string{
				"subscriptions", test.name,
				"--package-name", "com.example.app",
				"--purchase-id", "purchase-1",
				"--confirm",
				"--output", "json",
			}
			args = append(args, test.extra...)
			if err := execute(NewCommand(dependencies), args...); err != nil {
				t.Fatalf("%s: %v", test.name, err)
			}
			test.assertCall(t, service)
			if !strings.Contains(stdout.String(), `"futureField":"preserved"`) {
				t.Fatalf("lossless output = %s", stdout.String())
			}
		})
	}
}

func TestEveryMutationRequiresConfirmationBeforeSession(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"purchases", "consume"},
		{"purchases", "acknowledge"},
		{"subscriptions", "cancel"},
		{"subscriptions", "refund"},
		{"subscriptions", "revoke"},
	}
	for _, prefix := range tests {
		prefix := prefix
		t.Run(strings.Join(prefix, "-"), func(t *testing.T) {
			t.Parallel()
			openCalls := 0
			dependencies := testDependencies(io.Discard, Services{}, nil, &openCalls)
			args := append(
				append([]string(nil), prefix...),
				"--package-name", "com.example.app",
				"--purchase-id", "purchase-1",
			)
			err := execute(NewCommand(dependencies), args...)
			if !errors.Is(err, shared.ErrConfirmationRequired) {
				t.Fatalf("error = %v, want confirmation error", err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
		})
	}
}

func TestEveryMutationDryRunPrintsPlanWithoutSession(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"purchases", "consume"},
		{"purchases", "acknowledge"},
		{"subscriptions", "cancel", "--caller", "user"},
		{"subscriptions", "refund"},
		{"subscriptions", "revoke"},
	}
	for _, prefix := range tests {
		prefix := prefix
		t.Run(strings.Join(prefix, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			openCalls := 0
			dependencies := testDependencies(&stdout, Services{}, nil, &openCalls)
			args := append(
				append([]string(nil), prefix...),
				"--package-name", "com.example.app",
				"--purchase-id", "purchase-1",
				"--dry-run",
				"--output", "json",
			)
			if err := execute(NewCommand(dependencies), args...); err != nil {
				t.Fatalf("dry-run: %v", err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
			for _, expected := range []string{
				`"dryRun":true`,
				`"requiresConfirmation":true`,
				`"mutationsPerformed":false`,
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("plan missing %s: %s", expected, stdout.String())
				}
			}
		})
	}
}

func TestOrdersListIsReadOnlyWithoutConfirmation(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	next := "next"
	service := &fakeOrderService{
		listResult: rawOrdersResult(t, `{
			"continuationToken":"next",
			"orderItemList":[{
				"orderId":"order-1",
				"purchaseId":"purchase-1",
				"packageName":"com.example.app",
				"itemId":"coins",
				"status":"2",
				"futureField":true
			}],
			"futureTopLevel":"preserved"
		}`),
	}
	var openedProfile string
	dependencies := testDependencies(
		&stdout,
		Services{Orders: service},
		&openedProfile,
		nil,
	)

	err := execute(
		NewCommand(dependencies),
		"orders", "list",
		"--seller-seq", "000123456789",
		"--package-name", "com.example.app",
		"--request-date", "20260730",
		"--continuation-token", "current",
		"--profile", "production",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("orders list: %v", err)
	}
	if openedProfile != "production" || service.listCalls != 1 {
		t.Fatalf("profile = %q, calls = %d", openedProfile, service.listCalls)
	}
	want := orders.ListOptions{
		SellerSequence:    "000123456789",
		PackageName:       "com.example.app",
		RequestDate:       "20260730",
		ContinuationToken: "current",
	}
	if service.listInput != want {
		t.Fatalf("options = %+v, want %+v", service.listInput, want)
	}
	if service.listResult.ContinuationToken == nil ||
		*service.listResult.ContinuationToken != next {
		t.Fatalf("continuation token = %#v", service.listResult.ContinuationToken)
	}
	if !strings.Contains(stdout.String(), `"futureTopLevel":"preserved"`) ||
		!strings.Contains(stdout.String(), `"futureField":true`) {
		t.Fatalf("lossless output = %s", stdout.String())
	}
}

func TestLocalValidationRunsBeforeSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing package", args: []string{"purchases", "consume", "--purchase-id", "purchase-1", "--confirm"}},
		{name: "invalid package", args: []string{"purchases", "consume", "--package-name", "example", "--purchase-id", "purchase-1", "--confirm"}},
		{name: "missing purchase", args: []string{"purchases", "consume", "--package-name", "com.example.app", "--confirm"}},
		{name: "invalid batch ID", args: []string{"purchases", "consume", "--package-name", "com.example.app", "--purchase-id", "purchase-1", "--purchased-id", "bad/id", "--confirm"}},
		{name: "invalid output", args: []string{"purchases", "consume", "--package-name", "com.example.app", "--purchase-id", "purchase-1", "--output", "yaml", "--confirm"}},
		{name: "invalid caller", args: []string{"subscriptions", "cancel", "--package-name", "com.example.app", "--purchase-id", "purchase-1", "--caller", "operator", "--confirm"}},
		{name: "status positional", args: []string{"subscriptions", "status", "--package-name", "com.example.app", "--purchase-id", "purchase-1", "extra"}},
		{name: "invalid seller", args: []string{"orders", "list", "--seller-seq", "123"}},
		{name: "invalid date", args: []string{"orders", "list", "--seller-seq", "000123456789", "--request-date", "20260230"}},
		{name: "invalid token", args: []string{"orders", "list", "--seller-seq", "000123456789", "--continuation-token", " token"}},
		{name: "invalid order package", args: []string{"orders", "list", "--seller-seq", "000123456789", "--package-name", "example"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			openCalls := 0
			dependencies := testDependencies(io.Discard, Services{}, nil, &openCalls)
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want *shared.UsageError", err, err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
		})
	}
}

func TestOrdersRejectsMutationFlagsInsteadOfRequiringThem(t *testing.T) {
	t.Parallel()

	for _, flagName := range []string{"--confirm", "--dry-run"} {
		openCalls := 0
		dependencies := testDependencies(io.Discard, Services{}, nil, &openCalls)
		err := execute(
			NewCommand(dependencies),
			"orders", "list",
			"--seller-seq", "000123456789",
			flagName,
		)
		if err == nil {
			t.Fatalf("%s unexpectedly accepted", flagName)
		}
		if openCalls != 0 {
			t.Fatalf("%s made %d open calls", flagName, openCalls)
		}
	}
}

func TestServiceAndPrinterErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("samsung unavailable")
	purchaseService := &fakePurchaseService{consumeErr: sentinel}
	dependencies := testDependencies(
		io.Discard,
		Services{Purchases: purchaseService},
		nil,
		nil,
	)
	err := execute(
		NewCommand(dependencies),
		"purchases", "consume",
		"--package-name", "com.example.app",
		"--purchase-id", "purchase-1",
		"--confirm",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("service error = %v, want sentinel", err)
	}

	dependencies = testDependencies(
		io.Discard,
		Services{Orders: &fakeOrderService{
			listResult: &orders.Result{OrderItemList: []orders.Order{}},
		}},
		nil,
		nil,
	)
	dependencies.Printer = errorPrinter{err: sentinel}
	err = execute(
		NewCommand(dependencies),
		"orders", "list",
		"--seller-seq", "000123456789",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("printer error = %v, want sentinel", err)
	}
}

func testDependencies(
	stdout io.Writer,
	services Services,
	openedProfile *string,
	openCalls *int,
) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		OpenServices: func(profile string) (Services, error) {
			if openedProfile != nil {
				*openedProfile = profile
			}
			if openCalls != nil {
				*openCalls++
			}
			return services, nil
		},
	}
}

type errorPrinter struct {
	err error
}

func (printer errorPrinter) Print(output.Format, any) error {
	return printer.err
}

func rawPurchaseResult(t *testing.T, raw string) *purchases.Result {
	t.Helper()
	var result purchases.Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode purchase result: %v", err)
	}
	return &result
}

func rawSubscriptionStatus(t *testing.T, raw string) *subscriptions.Status {
	t.Helper()
	var result subscriptions.Status
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode subscription status: %v", err)
	}
	return &result
}

func rawSubscriptionAction(t *testing.T, raw string) *subscriptions.ActionResult {
	t.Helper()
	var result subscriptions.ActionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode subscription action: %v", err)
	}
	return &result
}

func rawOrdersResult(t *testing.T, raw string) *orders.Result {
	t.Helper()
	var result orders.Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode orders result: %v", err)
	}
	return &result
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}

func subcommandNames(command *ffcli.Command) []string {
	names := make([]string, len(command.Subcommands))
	for index, subcommand := range command.Subcommands {
		names[index] = subcommand.Name
	}
	return names
}
