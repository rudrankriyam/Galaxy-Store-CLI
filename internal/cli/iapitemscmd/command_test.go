package iapitemscmd

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
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/items"
)

type fakeService struct {
	listPackage string
	listOptions items.ListOptions
	listResult  *items.ListResult
	listErr     error
	listCalls   int

	viewPackage string
	viewID      string
	viewResult  *items.Item
	viewErr     error
	viewCalls   int

	createPackage string
	createRequest items.FullRequest
	createResult  *items.Item
	createErr     error
	createCalls   int

	replacePackage string
	replaceRequest items.FullRequest
	replaceResult  *items.Item
	replaceErr     error
	replaceCalls   int

	updatePackage string
	updateRequest items.UpdateRequest
	updateResult  *items.Item
	updateErr     error
	updateCalls   int

	deletePackage string
	deleteID      string
	deleteResult  *items.Item
	deleteErr     error
	deleteCalls   int
}

func (service *fakeService) List(
	_ context.Context,
	packageName string,
	options items.ListOptions,
) (*items.ListResult, error) {
	service.listCalls++
	service.listPackage = packageName
	service.listOptions = options
	return service.listResult, service.listErr
}

func (service *fakeService) View(
	_ context.Context,
	packageName string,
	itemID string,
) (*items.Item, error) {
	service.viewCalls++
	service.viewPackage = packageName
	service.viewID = itemID
	return service.viewResult, service.viewErr
}

func (service *fakeService) Create(
	_ context.Context,
	packageName string,
	request items.FullRequest,
) (*items.Item, error) {
	service.createCalls++
	service.createPackage = packageName
	service.createRequest = request
	return service.createResult, service.createErr
}

func (service *fakeService) Replace(
	_ context.Context,
	packageName string,
	request items.FullRequest,
) (*items.Item, error) {
	service.replaceCalls++
	service.replacePackage = packageName
	service.replaceRequest = request
	return service.replaceResult, service.replaceErr
}

func (service *fakeService) Update(
	_ context.Context,
	packageName string,
	request items.UpdateRequest,
) (*items.Item, error) {
	service.updateCalls++
	service.updatePackage = packageName
	service.updateRequest = request
	return service.updateResult, service.updateErr
}

func (service *fakeService) Delete(
	_ context.Context,
	packageName string,
	itemID string,
) (*items.Item, error) {
	service.deleteCalls++
	service.deletePackage = packageName
	service.deleteID = itemID
	return service.deleteResult, service.deleteErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "items" {
		t.Fatalf("name = %q", command.Name)
	}
	names := make([]string, len(command.Subcommands))
	for index, subcommand := range command.Subcommands {
		names[index] = subcommand.Name
	}
	want := []string{"list", "view", "create", "replace", "update", "delete"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("subcommands = %v, want %v", names, want)
	}
}

func TestListPassesPaginationProfileAndPreservesRawJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{listResult: &items.ListResult{
		Items: []items.Item{
			rawItem(t, `{"id":"fuel","title":"Fuel","type":"ITEM","status":"PUBLISHED","future":true}`),
		},
		TotalCount: 9,
	}}
	var profile string
	dependencies := testDependencies(&stdout, service, &profile)
	err := execute(
		NewCommand(dependencies),
		"list",
		"--package-name", "com.example.game",
		"--page", "3",
		"--size", "50",
		"--profile", "production",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if service.listCalls != 1 ||
		service.listPackage != "com.example.game" ||
		service.listOptions != (items.ListOptions{Page: 3, Size: 50}) {
		t.Fatalf(
			"list calls=%d package=%q options=%+v",
			service.listCalls,
			service.listPackage,
			service.listOptions,
		)
	}
	if profile != "production" {
		t.Fatalf("profile = %q", profile)
	}
	for _, expected := range []string{`"itemList"`, `"totalCount":9`, `"future":true`} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("JSON missing %q: %s", expected, stdout.String())
		}
	}
}

func TestListTableProjection(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{listResult: &items.ListResult{
		Items: []items.Item{{
			ID:       "fuel",
			Title:    "Fuel",
			Type:     items.TypeItem,
			Status:   items.StatusPublished,
			USDPrice: "0.99",
			Prices: []items.Price{
				{CountryID: "USA", LocalPrice: "0.99"},
				{CountryID: "KOR", LocalPrice: "1000"},
			},
		}},
	}}
	err := execute(
		NewCommand(testDependencies(&stdout, service, nil)),
		"list",
		"--package-name", "com.example.game",
		"--output", "table",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, expected := range []string{
		"ITEM ID", "TITLE", "TYPE", "STATUS", "USD PRICE", "TERRITORIES", "USA,KOR",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("table missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestViewPassesIdentifiersAndPrintsRawItem(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{
		viewResult: ptr(rawItem(t, `{"id":"fuel_pack","status":"UNPUBLISHED","futureField":"kept"}`)),
	}
	err := execute(
		NewCommand(testDependencies(&stdout, service, nil)),
		"view",
		"--package-name", "com.example.game",
		"--item-id", "fuel_pack",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if service.viewCalls != 1 ||
		service.viewPackage != "com.example.game" ||
		service.viewID != "fuel_pack" {
		t.Fatalf("view calls=%d package=%q id=%q", service.viewCalls, service.viewPackage, service.viewID)
	}
	if got := strings.TrimSpace(stdout.String()); got !=
		`{"id":"fuel_pack","status":"UNPUBLISHED","futureField":"kept"}` {
		t.Fatalf("JSON = %s", got)
	}
}

func TestFileMutationsDecodeExactRequestAndCallService(t *testing.T) {
	t.Parallel()

	fullJSON := `{
		"id":"fuel",
		"title":"Fuel",
		"description":"One tank",
		"type":"ITEM",
		"status":"PUBLISHED",
		"itemPaymentMethod":{"phoneBillStatus":false},
		"usdPrice":0.99,
		"prices":[{"countryId":"USA","currency":"USD","localPrice":"0.99"}]
	}`
	updateJSON := `{
		"id":"fuel",
		"title":"More fuel",
		"prices":[{"countryId":"USA","localPrice":"1.99"}]
	}`
	tests := []struct {
		action string
		input  string
		assert func(*testing.T, *fakeService)
	}{
		{
			action: "create",
			input:  fullJSON,
			assert: func(t *testing.T, service *fakeService) {
				if service.createCalls != 1 || service.createRequest.ID != "fuel" {
					t.Fatalf("create calls=%d request=%+v", service.createCalls, service.createRequest)
				}
			},
		},
		{
			action: "replace",
			input:  fullJSON,
			assert: func(t *testing.T, service *fakeService) {
				if service.replaceCalls != 1 || service.replaceRequest.USDPrice.String() != "0.99" {
					t.Fatalf("replace calls=%d request=%+v", service.replaceCalls, service.replaceRequest)
				}
			},
		},
		{
			action: "update",
			input:  updateJSON,
			assert: func(t *testing.T, service *fakeService) {
				if service.updateCalls != 1 ||
					service.updateRequest.Title == nil ||
					*service.updateRequest.Title != "More fuel" {
					t.Fatalf("update calls=%d request=%+v", service.updateCalls, service.updateRequest)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.action, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			response := &items.Item{ID: "fuel", Type: items.TypeItem, Status: items.StatusPublished}
			service := &fakeService{
				createResult:  response,
				replaceResult: response,
				updateResult:  response,
			}
			readCalls := 0
			var profile string
			dependencies := testDependencies(&stdout, service, &profile)
			dependencies.ReadFile = func(path string) ([]byte, error) {
				readCalls++
				if path != "request.json" {
					t.Fatalf("read path = %q", path)
				}
				return []byte(test.input), nil
			}
			err := execute(
				NewCommand(dependencies),
				test.action,
				"--package-name", "com.example.game",
				"--file", "request.json",
				"--profile", "production",
				"--confirm",
				"--output", "json",
			)
			if err != nil {
				t.Fatalf("%s: %v", test.action, err)
			}
			if readCalls != 1 || profile != "production" {
				t.Fatalf("read calls=%d profile=%q", readCalls, profile)
			}
			test.assert(t, service)
			if got := strings.TrimSpace(stdout.String()); !strings.Contains(got, `"id":"fuel"`) {
				t.Fatalf("output = %s", got)
			}
		})
	}
}

func TestDeletePassesIdentifiersAfterConfirmation(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{deleteResult: &items.Item{ID: "fuel"}}
	err := execute(
		NewCommand(testDependencies(&stdout, service, nil)),
		"delete",
		"--package-name", "com.example.game",
		"--item-id", "fuel",
		"--confirm",
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if service.deleteCalls != 1 ||
		service.deletePackage != "com.example.game" ||
		service.deleteID != "fuel" {
		t.Fatalf(
			"delete calls=%d package=%q id=%q",
			service.deleteCalls,
			service.deletePackage,
			service.deleteID,
		)
	}
}

func TestDryRunValidatesFileAndNeverOpensSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	readCalls := 0
	openCalls := 0
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			readCalls++
			return []byte(validFullJSON()), nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return nil, errors.New("must not open")
		},
	}
	err := execute(
		NewCommand(dependencies),
		"create",
		"--package-name", "com.example.game",
		"--file", "request.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if readCalls != 1 || openCalls != 0 {
		t.Fatalf("read calls=%d open calls=%d", readCalls, openCalls)
	}
	for _, expected := range []string{
		`"action":"create"`,
		`"itemId":"fuel"`,
		`"requiresConfirmation":true`,
		`"mutationsPerformed":false`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("plan missing %q: %s", expected, stdout.String())
		}
	}
}

func TestDeleteDryRunNeverReadsFileOrOpensSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	readCalls := 0
	openCalls := 0
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			readCalls++
			return nil, nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return nil, errors.New("must not open")
		},
	}
	err := execute(
		NewCommand(dependencies),
		"delete",
		"--package-name", "com.example.game",
		"--item-id", "fuel",
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if readCalls != 0 || openCalls != 0 {
		t.Fatalf("read calls=%d open calls=%d", readCalls, openCalls)
	}
}

func TestConfirmationIsRequiredBeforeFileOrSession(t *testing.T) {
	t.Parallel()

	readCalls := 0
	openCalls := 0
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		ReadFile: func(string) ([]byte, error) {
			readCalls++
			return []byte(validFullJSON()), nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"create",
		"--package-name", "com.example.game",
		"--file", "request.json",
	)
	if !errors.Is(err, shared.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want confirmation required", err)
	}
	if readCalls != 0 || openCalls != 0 {
		t.Fatalf("read calls=%d open calls=%d", readCalls, openCalls)
	}
}

func TestLocalFlagValidationPrecedesFileAndSession(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"create", "--file", "request.json", "--confirm"},
		{"create", "--package-name", "invalid", "--file", "request.json", "--confirm"},
		{"create", "--package-name", "com.example.app", "--confirm"},
		{"create", "--package-name", "com.example.app", "--file", "request.json", "--output", "yaml", "--confirm"},
		{"delete", "--package-name", "com.example.app", "--item-id", "../fuel", "--confirm"},
		{"list", "--package-name", "com.example.app", "--page", "0"},
	}
	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			readCalls := 0
			openCalls := 0
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				ReadFile: func(string) ([]byte, error) {
					readCalls++
					return []byte(validFullJSON()), nil
				},
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(NewCommand(dependencies), args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want UsageError", err, err)
			}
			if readCalls != 0 || openCalls != 0 {
				t.Fatalf("read calls=%d open calls=%d", readCalls, openCalls)
			}
		})
	}
}

func TestInvalidJSONAndRequestNeverOpenSession(t *testing.T) {
	t.Parallel()

	tests := []string{
		`not-json`,
		`{"id":"fuel","unknown":true}`,
		`{"id":"fuel"}`,
		`{
			"id":"fuel",
			"title":"Fuel",
			"description":"One tank",
			"type":"ITEM",
			"status":"PUBLISHED",
			"usdPrice":0.99,
			"prices":[{"countryId":"USA","currency":"USD","localPrice":"0.99"}]
		}`,
		`{
			"id":"fuel",
			"title":"Fuel",
			"description":"One tank",
			"type":"ITEM",
			"status":"PUBLISHED",
			"itemPaymentMethod":{},
			"usdPrice":0.99,
			"prices":[{"countryId":"USA","currency":"USD","localPrice":"0.99"}]
		}`,
		validFullJSON() + `{}`,
	}
	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			openCalls := 0
			dependencies := Dependencies{
				Printer:  output.NewPrinter(io.Discard, nil),
				ReadFile: func(string) ([]byte, error) { return []byte(input), nil },
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(
				NewCommand(dependencies),
				"create",
				"--package-name", "com.example.app",
				"--file", "request.json",
				"--confirm",
			)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want UsageError", err, err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d", openCalls)
			}
		})
	}
}

func TestServiceAndPrinterErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("samsung unavailable")
	service := &fakeService{listErr: sentinel}
	dependencies := testDependencies(io.Discard, service, nil)
	err := execute(
		NewCommand(dependencies),
		"list",
		"--package-name", "com.example.app",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("list error = %v, want sentinel", err)
	}

	service = &fakeService{viewResult: &items.Item{ID: "fuel"}}
	dependencies = testDependencies(io.Discard, service, nil)
	dependencies.Printer = errorPrinter{err: sentinel}
	err = execute(
		NewCommand(dependencies),
		"view",
		"--package-name", "com.example.app",
		"--item-id", "fuel",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("printer error = %v, want sentinel", err)
	}
}

type errorPrinter struct {
	err error
}

func (printer errorPrinter) Print(output.Format, any) error {
	return printer.err
}

func testDependencies(
	stdout io.Writer,
	service Service,
	openedProfile *string,
) Dependencies {
	return Dependencies{
		Printer:  output.NewPrinter(stdout, nil),
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("unexpected read") },
		OpenService: func(profile string) (Service, error) {
			if openedProfile != nil {
				*openedProfile = profile
			}
			return service, nil
		},
	}
}

func rawItem(t *testing.T, raw string) items.Item {
	t.Helper()
	var item items.Item
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return item
}

func ptr[T any](value T) *T {
	return &value
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}

func validFullJSON() string {
	return `{
		"id":"fuel",
		"title":"Fuel",
		"description":"One tank",
		"type":"ITEM",
		"status":"PUBLISHED",
		"itemPaymentMethod":{"phoneBillStatus":false},
		"usdPrice":0.99,
		"prices":[{"countryId":"USA","currency":"USD","localPrice":"0.99"}]
	}`
}
