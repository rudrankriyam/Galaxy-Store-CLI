package betacmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/beta"
)

type fakeService struct {
	getInput  beta.ListOptions
	getResult *beta.Test
	getErr    error
	getCalls  int

	updateInput  beta.UpdateInput
	updateResult *beta.UpdateResult
	updateErr    error
	updateCalls  int
}

func (service *fakeService) Get(
	_ context.Context,
	input beta.ListOptions,
) (*beta.Test, error) {
	service.getCalls++
	service.getInput = input
	return service.getResult, service.getErr
}

func (service *fakeService) Update(
	_ context.Context,
	input beta.UpdateInput,
) (*beta.UpdateResult, error) {
	service.updateCalls++
	service.updateInput = input
	return service.updateResult, service.updateErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "beta" || len(command.Subcommands) != 1 {
		t.Fatalf("command = %#v", command)
	}
	testers := command.Subcommands[0]
	if testers.Name != "testers" || len(testers.Subcommands) != 2 ||
		testers.Subcommands[0].Name != "list" || testers.Subcommands[1].Name != "update" {
		t.Fatalf("testers command = %#v", testers)
	}
}

func TestListPassesProfileAndExactOptions(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var profile string
	service := &fakeService{getResult: &beta.Test{
		TotalNumberOfBetaTesters: 2,
		BetaTesters:              []string{"one@example.com", "two@example.com"},
		FeedbackChannel:          "feedback@example.com",
	}}
	dependencies := testDependencies(&stdout, service, &profile, nil)
	err := execute(
		NewCommand(dependencies),
		"testers", "list",
		"--profile", "production",
		"--content-id", "000007654321",
		"--app-status", "registration",
		"--offset", "10",
		"--limit", "100",
		"--output", "table",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if profile != "production" {
		t.Fatalf("profile = %q", profile)
	}
	want := beta.ListOptions{
		ContentID: "000007654321", AppStatus: "REGISTRATION", Offset: 10, Limit: 100,
	}
	if service.getCalls != 1 || !reflect.DeepEqual(service.getInput, want) {
		t.Fatalf("Get calls/input = %d/%+v, want 1/%+v", service.getCalls, service.getInput, want)
	}
	for _, text := range []string{"TESTER", "one@example.com", "feedback@example.com", "2"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("table missing %q:\n%s", text, stdout.String())
		}
	}
}

func TestUpdateDryRunValidatesFileAndDoesNotOpenSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var opens int
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{
				"betaTestersToBeAdded":["one@example.com","two@example.com"],
				"betaTestersToBeDeleted":["old@example.com"],
				"feedbackChannel":"feedback@example.com"
			}`), nil
		},
		OpenService: func(string) (Service, error) {
			opens++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"testers", "update",
		"--content-id", "000007654321",
		"--file", "testers.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("update dry-run: %v", err)
	}
	if opens != 0 {
		t.Fatalf("session opens = %d, want 0", opens)
	}
	for _, text := range []string{
		`"action":"update"`,
		`"resource":"closed-beta-testers/000007654321"`,
		`"details":"add=2 delete=1 feedbackChannel=true"`,
		`"mutationsPerformed":false`,
	} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("plan missing %s: %s", text, stdout.String())
		}
	}
}

func TestUpdateRequiresConfirmationBeforeSession(t *testing.T) {
	t.Parallel()

	var opens int
	dependencies := Dependencies{
		Printer:  output.NewPrinter(io.Discard, nil),
		ReadFile: func(string) ([]byte, error) { return []byte(`{"betaTestersToBeAdded":["one@example.com"]}`), nil },
		OpenService: func(string) (Service, error) {
			opens++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"testers", "update",
		"--content-id", "000007654321",
		"--file", "testers.json",
	)
	if !errors.Is(err, shared.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want confirmation required", err)
	}
	if opens != 0 {
		t.Fatalf("session opens = %d, want 0", opens)
	}
}

func TestConfirmedUpdatePassesExactInputAndPrintsResult(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var profile string
	service := &fakeService{updateResult: &beta.UpdateResult{
		AdditionFailedTesters: []string{},
		DeletionFailedTesters: []string{},
	}}
	dependencies := testDependencies(
		&stdout,
		service,
		&profile,
		[]byte(`{
			"betaTestersToBeAdded":["one@example.com"],
			"betaTestersToBeDeleted":["old@example.com"],
			"feedbackChannel":"feedback@example.com"
		}`),
	)
	err := execute(
		NewCommand(dependencies),
		"testers", "update",
		"--profile", "production",
		"--content-id", "000007654321",
		"--file", "testers.json",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if profile != "production" || service.updateCalls != 1 {
		t.Fatalf("profile/calls = %q/%d", profile, service.updateCalls)
	}
	if service.updateInput.ContentID != "000007654321" ||
		!reflect.DeepEqual(service.updateInput.AddTesters, []string{"one@example.com"}) ||
		!reflect.DeepEqual(service.updateInput.DeleteTesters, []string{"old@example.com"}) ||
		service.updateInput.FeedbackChannel == nil ||
		*service.updateInput.FeedbackChannel != "feedback@example.com" {
		t.Fatalf("Update input = %#v", service.updateInput)
	}
	if got := stdout.String(); !strings.Contains(got, `"additionFailedTesters":[]`) ||
		!strings.Contains(got, `"deletionFailedTesters":[]`) {
		t.Fatalf("output = %s", got)
	}
}

func TestPartialTesterFailurePrintsExactResultAndReturnsTypedError(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	partial := &beta.TesterFailuresError{
		AdditionFailed: []string{"invalid@example.com"},
	}
	service := &fakeService{
		updateResult: &beta.UpdateResult{
			AdditionFailedTesters: []string{"invalid@example.com"},
			DeletionFailedTesters: []string{},
		},
		updateErr: partial,
	}
	dependencies := testDependencies(
		&stdout,
		service,
		nil,
		[]byte(`{"betaTestersToBeAdded":["invalid@example.com"]}`),
	)
	err := execute(
		NewCommand(dependencies),
		"testers", "update",
		"--content-id", "000007654321",
		"--file", "testers.json",
		"--confirm",
		"--output", "json",
	)
	if !errors.Is(err, partial) {
		t.Fatalf("error = %v, want partial failure", err)
	}
	if !strings.Contains(stdout.String(), `"additionFailedTesters":["invalid@example.com"]`) {
		t.Fatalf("partial output = %s", stdout.String())
	}
}

func TestValidationHappensBeforeSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		file []byte
	}{
		{name: "list ID", args: []string{"testers", "list", "--content-id", "bad", "--app-status", "SALE"}},
		{name: "list status", args: []string{"testers", "list", "--content-id", "000007654321"}},
		{name: "list limit", args: []string{"testers", "list", "--content-id", "000007654321", "--app-status", "SALE", "--limit", "1001"}},
		{name: "list output", args: []string{"testers", "list", "--content-id", "000007654321", "--app-status", "SALE", "--output", "yaml"}},
		{name: "update file missing", args: []string{"testers", "update", "--content-id", "000007654321", "--confirm"}},
		{
			name: "unknown file field",
			args: []string{"testers", "update", "--content-id", "000007654321", "--file", "x", "--confirm"},
			file: []byte(`{"unknown":true}`),
		},
		{
			name: "blank tester",
			args: []string{"testers", "update", "--content-id", "000007654321", "--file", "x", "--confirm"},
			file: []byte(`{"betaTestersToBeAdded":[" "]}`),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var opens int
			dependencies := Dependencies{
				Printer:  output.NewPrinter(io.Discard, nil),
				ReadFile: func(string) ([]byte, error) { return test.file, nil },
				OpenService: func(string) (Service, error) {
					opens++
					return &fakeService{}, nil
				},
			}
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want *shared.UsageError", err, err)
			}
			if opens != 0 {
				t.Fatalf("session opens = %d, want 0", opens)
			}
		})
	}
}

func testDependencies(
	stdout io.Writer,
	service Service,
	profile *string,
	file []byte,
) Dependencies {
	return Dependencies{
		Printer:  output.NewPrinter(stdout, nil),
		ReadFile: func(string) ([]byte, error) { return file, nil },
		OpenService: func(value string) (Service, error) {
			if profile != nil {
				*profile = value
			}
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
