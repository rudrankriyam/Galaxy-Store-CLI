package appscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

type fakeService struct {
	listResult *apps.ListResult
	listErr    error
	listCalls  int
	listInput  apps.ListOptions

	viewResult []apps.App
	viewErr    error
	viewCalls  int
	viewID     string
}

func (service *fakeService) List(
	_ context.Context,
	options apps.ListOptions,
) (*apps.ListResult, error) {
	service.listCalls++
	service.listInput = options
	return service.listResult, service.listErr
}

func (service *fakeService) View(_ context.Context, contentID string) ([]apps.App, error) {
	service.viewCalls++
	service.viewID = contentID
	return service.viewResult, service.viewErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "apps" || len(command.Subcommands) != 2 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "list" || command.Subcommands[1].Name != "view" {
		t.Fatalf("subcommands = %q, %q", command.Subcommands[0].Name, command.Subcommands[1].Name)
	}
}

func TestListPassesProfileAndPaginationAndPreservesLosslessJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{
		listResult: &apps.ListResult{
			Apps: []apps.App{
				rawApp(t, `{
					"contentId":"000001234567",
					"contentName":"Galaxy App",
					"contentStatus":"FOR_SALE",
					"modifyDate":"2026-07-30 10:00:00.0"
				}`),
			},
			Pagination: apps.Pagination{
				Offset:     3,
				Limit:      1,
				Total:      9,
				HasMore:    true,
				NextOffset: 4,
			},
		},
	}
	var openedProfile string
	dependencies := testDependencies(&stdout, service, &openedProfile)

	err := execute(
		NewCommand(dependencies),
		"list",
		"--profile", "production",
		"--offset", "3",
		"--limit", "1",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if openedProfile != "production" {
		t.Fatalf("profile = %q", openedProfile)
	}
	if service.listCalls != 1 || service.listInput != (apps.ListOptions{Offset: 3, Limit: 1}) {
		t.Fatalf("list calls = %d, input = %+v", service.listCalls, service.listInput)
	}
	if !strings.Contains(stdout.String(), `"modifyDate":"2026-07-30 10:00:00.0"`) {
		t.Fatalf("lossless JSON omitted unknown field: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"nextOffset":4`) {
		t.Fatalf("JSON omitted pagination: %s", stdout.String())
	}
}

func TestListTableHasUsefulColumns(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{listResult: &apps.ListResult{
		Apps: []apps.App{{
			ContentID:     "000001234567",
			Title:         "Galaxy App",
			PackageName:   "com.example.app",
			AppStatus:     "SALE",
			ContentStatus: "FOR_SALE",
		}},
	}}
	dependencies := testDependencies(&stdout, service, nil)
	if err := execute(NewCommand(dependencies), "list", "--output", "table"); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, expected := range []string{
		"CONTENT ID",
		"TITLE",
		"PACKAGE",
		"APP STATUS",
		"CONTENT STATUS",
		"com.example.app",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("table missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestViewUsesContentIDAndPreservesBothRecordsAndUnknownFields(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{viewResult: []apps.App{
		rawApp(t, `{
			"contentId":"000007654321",
			"appTitle":"Published",
			"appStatus":"SALE",
			"contentStatus":"FOR_SALE",
			"binaryList":[{"packageName":"com.example.app"}]
		}`),
		rawApp(t, `{
			"contentId":"000007654321",
			"appTitle":"Update",
			"appStatus":"REGISTRATION",
			"contentStatus":"REGISTERING",
			"futureField":{"kept":true}
		}`),
	}}
	dependencies := testDependencies(&stdout, service, nil)

	if err := execute(
		NewCommand(dependencies),
		"view",
		"--content-id", "000007654321",
		"--output", "json",
	); err != nil {
		t.Fatalf("view: %v", err)
	}
	if service.viewCalls != 1 || service.viewID != "000007654321" {
		t.Fatalf("view calls = %d, ID = %q", service.viewCalls, service.viewID)
	}
	for _, expected := range []string{
		`"appStatus":"SALE"`,
		`"appStatus":"REGISTRATION"`,
		`"binaryList"`,
		`"futureField":{"kept":true}`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("view JSON missing %s: %s", expected, stdout.String())
		}
	}
}

func TestViewMarkdownHasOneRowPerAppStatus(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{viewResult: []apps.App{
		{ContentID: "000007654321", Title: "Published", AppStatus: "SALE"},
		{ContentID: "000007654321", Title: "Update", AppStatus: "REGISTRATION"},
	}}
	dependencies := testDependencies(&stdout, service, nil)
	if err := execute(
		NewCommand(dependencies),
		"view",
		"--content-id", "000007654321",
		"--output", "markdown",
	); err != nil {
		t.Fatalf("view: %v", err)
	}
	if !strings.Contains(stdout.String(), "| CONTENT ID | TITLE | PACKAGE | APP STATUS | CONTENT STATUS |") ||
		!strings.Contains(stdout.String(), "| 000007654321 | Published |  | SALE |  |") ||
		!strings.Contains(stdout.String(), "| 000007654321 | Update |  | REGISTRATION |  |") {
		t.Fatalf("markdown output:\n%s", stdout.String())
	}
}

func TestValidationRunsBeforeCredentialOrServiceSideEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "list positional", args: []string{"list", "extra"}},
		{name: "list bad output", args: []string{"list", "--output", "yaml"}},
		{name: "list negative offset", args: []string{"list", "--offset=-1"}},
		{name: "list negative limit", args: []string{"list", "--limit=-1"}},
		{name: "view positional", args: []string{"view", "--content-id", "000007654321", "extra"}},
		{name: "view missing ID", args: []string{"view"}},
		{name: "view invalid ID", args: []string{"view", "--content-id", "abc"}},
		{name: "view padded ID", args: []string{"view", "--content-id", " 000007654321 "}},
		{name: "view bad output", args: []string{"view", "--content-id", "000007654321", "--output", "yaml"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				OpenService: func(string) (Service, error) {
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
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
		})
	}
}

func TestSessionFailureGuidesAuthLogin(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		OpenService: func(string) (Service, error) {
			return nil, errors.New(`no token; run "gsc auth login"`)
		},
	}
	err := execute(NewCommand(dependencies), "list")
	if err == nil || !strings.Contains(err.Error(), "gsc auth login") {
		t.Fatalf("error = %v, want login guidance", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestServiceAndPrinterErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("Samsung unavailable")
	service := &fakeService{listErr: sentinel}
	dependencies := testDependencies(io.Discard, service, nil)
	if err := execute(NewCommand(dependencies), "list"); !errors.Is(err, sentinel) {
		t.Fatalf("list error = %v, want sentinel", err)
	}

	service = &fakeService{viewResult: []apps.App{{ContentID: "000007654321"}}}
	dependencies = testDependencies(io.Discard, service, nil)
	dependencies.Printer = errorPrinter{err: sentinel}
	if err := execute(
		NewCommand(dependencies),
		"view",
		"--content-id", "000007654321",
	); !errors.Is(err, sentinel) {
		t.Fatalf("view error = %v, want sentinel", err)
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
		Printer: output.NewPrinter(stdout, nil),
		OpenService: func(profile string) (Service, error) {
			if openedProfile != nil {
				*openedProfile = profile
			}
			return service, nil
		},
	}
}

func rawApp(t *testing.T, raw string) apps.App {
	t.Helper()
	var app apps.App
	if err := json.Unmarshal([]byte(raw), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	return app
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
