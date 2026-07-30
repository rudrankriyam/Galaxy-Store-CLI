package rolloutscmd

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
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/rollout"
)

type fakeService struct {
	viewRateID, viewRateStatus string
	viewRateResult             *rollout.Rate
	viewRateErr                error
	viewRateCalls              int

	setRateInput  rollout.SetRateInput
	setRateResult *rollout.MutationResult
	setRateErr    error
	setRateCalls  int

	completeID, completeStatus string
	completeResult             *rollout.MutationResult
	completeErr                error
	completeCalls              int

	binariesID, binariesStatus string
	binariesResult             *rollout.BinaryList
	binariesErr                error
	binariesCalls              int

	addID, addSequence       string
	removeID, removeSequence string
	binaryResult             *rollout.MutationResult
	binaryErr                error
	addCalls, removeCalls    int
}

func (service *fakeService) ViewRate(
	_ context.Context,
	contentID,
	status string,
) (*rollout.Rate, error) {
	service.viewRateCalls++
	service.viewRateID, service.viewRateStatus = contentID, status
	return service.viewRateResult, service.viewRateErr
}

func (service *fakeService) SetRate(
	_ context.Context,
	input rollout.SetRateInput,
) (*rollout.MutationResult, error) {
	service.setRateCalls++
	service.setRateInput = input
	return service.setRateResult, service.setRateErr
}

func (service *fakeService) Complete(
	_ context.Context,
	contentID,
	status string,
) (*rollout.MutationResult, error) {
	service.completeCalls++
	service.completeID, service.completeStatus = contentID, status
	return service.completeResult, service.completeErr
}

func (service *fakeService) ViewBinaries(
	_ context.Context,
	contentID,
	status string,
) (*rollout.BinaryList, error) {
	service.binariesCalls++
	service.binariesID, service.binariesStatus = contentID, status
	return service.binariesResult, service.binariesErr
}

func (service *fakeService) AddBinary(
	_ context.Context,
	contentID,
	sequence string,
) (*rollout.MutationResult, error) {
	service.addCalls++
	service.addID, service.addSequence = contentID, sequence
	return service.binaryResult, service.binaryErr
}

func (service *fakeService) RemoveBinary(
	_ context.Context,
	contentID,
	sequence string,
) (*rollout.MutationResult, error) {
	service.removeCalls++
	service.removeID, service.removeSequence = contentID, sequence
	return service.binaryResult, service.binaryErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "rollouts" || len(command.Subcommands) != 2 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "rate" ||
		!reflect.DeepEqual(subcommandNames(command.Subcommands[0]), []string{"view", "update", "complete"}) {
		t.Fatalf("rate command = %#v", command.Subcommands[0])
	}
	if command.Subcommands[1].Name != "binaries" ||
		!reflect.DeepEqual(subcommandNames(command.Subcommands[1]), []string{"list", "update"}) {
		t.Fatalf("binaries command = %#v", command.Subcommands[1])
	}
}

func TestRateViewPassesProfileIdentityAndPrintsTable(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var profile string
	service := &fakeService{viewRateResult: &rollout.Rate{
		RolloutRate: 30,
		Countries: []rollout.CountryRate{
			{CountryCode: "USA", RolloutRate: 35},
		},
	}}
	dependencies := testDependencies(&stdout, service, &profile, nil)
	err := execute(
		NewCommand(dependencies),
		"rate", "view",
		"--profile", "production",
		"--content-id", "000007654321",
		"--app-status", "sale",
		"--output", "table",
	)
	if err != nil {
		t.Fatalf("rate view: %v", err)
	}
	if profile != "production" || service.viewRateCalls != 1 ||
		service.viewRateID != "000007654321" || service.viewRateStatus != "SALE" {
		t.Fatalf("profile/call = %q/%d/%q/%q", profile, service.viewRateCalls, service.viewRateID, service.viewRateStatus)
	}
	for _, text := range []string{"COUNTRY", "DEFAULT", "30", "USA", "35"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("table missing %q:\n%s", text, stdout.String())
		}
	}
}

func TestRateUpdateDryRunDoesNotOpenSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var opens int
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"rolloutRate":40,"countries":[{"countryCode":"USA","rolloutRate":45}]}`), nil
		},
		OpenService: func(string) (Service, error) {
			opens++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"rate", "update",
		"--content-id", "000007654321",
		"--app-status", "SALE",
		"--file", "rates.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("rate update dry-run: %v", err)
	}
	if opens != 0 {
		t.Fatalf("session opens = %d, want 0", opens)
	}
	for _, text := range []string{
		`"action":"advance"`,
		`"details":"default=40 countries=1"`,
		`reads the current rollout first`,
		`"mutationsPerformed":false`,
	} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("plan missing %q: %s", text, stdout.String())
		}
	}
}

func TestRateUpdateRequiresConfirmationBeforeSession(t *testing.T) {
	t.Parallel()

	var opens int
	dependencies := Dependencies{
		Printer:  output.NewPrinter(io.Discard, nil),
		ReadFile: func(string) ([]byte, error) { return []byte(`{"rolloutRate":40}`), nil },
		OpenService: func(string) (Service, error) {
			opens++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"rate", "update",
		"--content-id", "000007654321",
		"--app-status", "SALE",
		"--file", "rates.json",
	)
	if !errors.Is(err, shared.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want confirmation required", err)
	}
	if opens != 0 {
		t.Fatalf("session opens = %d, want 0", opens)
	}
}

func TestConfirmedRateUpdatePassesExactInput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{setRateResult: &rollout.MutationResult{
		ResultCode: "0000", ResultMessage: "Ok", Function: "ENABLE_ROLLOUT",
	}}
	dependencies := testDependencies(
		&stdout,
		service,
		nil,
		[]byte(`{"rolloutRate":40,"countries":[{"countryCode":"USA","rolloutRate":45}]}`),
	)
	err := execute(
		NewCommand(dependencies),
		"rate", "update",
		"--content-id", "000007654321",
		"--app-status", "REGISTRATION",
		"--file", "rates.json",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("rate update: %v", err)
	}
	want := rollout.SetRateInput{
		ContentID: "000007654321", AppStatus: "REGISTRATION", RolloutRate: 40,
		Countries: []rollout.CountryRate{{CountryCode: "USA", RolloutRate: 45}},
	}
	if service.setRateCalls != 1 || !reflect.DeepEqual(service.setRateInput, want) {
		t.Fatalf("SetRate calls/input = %d/%#v, want 1/%#v", service.setRateCalls, service.setRateInput, want)
	}
	if !strings.Contains(stdout.String(), `"function":"ENABLE_ROLLOUT"`) {
		t.Fatalf("output = %s", stdout.String())
	}
}

func TestCompleteDryRunAndConfirmedCallPreserveGlobalSemantics(t *testing.T) {
	t.Parallel()

	t.Run("dry run", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		var opens int
		dependencies := Dependencies{
			Printer: output.NewPrinter(&stdout, nil),
			OpenService: func(string) (Service, error) {
				opens++
				return &fakeService{}, nil
			},
		}
		err := execute(
			NewCommand(dependencies),
			"rate", "complete",
			"--content-id", "000007654321",
			"--app-status", "SALE",
			"--dry-run",
			"--output", "json",
		)
		if err != nil {
			t.Fatalf("complete dry-run: %v", err)
		}
		if opens != 0 {
			t.Fatalf("session opens = %d, want 0", opens)
		}
		if !strings.Contains(stdout.String(), `deploy the release to all users globally`) ||
			!strings.Contains(stdout.String(), `DISABLE_ROLLOUT`) {
			t.Fatalf("plan obscures global semantics: %s", stdout.String())
		}
	})

	t.Run("confirmed", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		service := &fakeService{completeResult: &rollout.MutationResult{
			ResultCode: "0000", ResultMessage: "Ok",
			Function: "DISABLE_ROLLOUT", Completed: true,
		}}
		dependencies := testDependencies(&stdout, service, nil, nil)
		err := execute(
			NewCommand(dependencies),
			"rate", "complete",
			"--content-id", "000007654321",
			"--app-status", "SALE",
			"--confirm",
			"--output", "json",
		)
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if service.completeCalls != 1 ||
			service.completeID != "000007654321" ||
			service.completeStatus != "SALE" {
			t.Fatalf("complete calls/input = %d/%q/%q", service.completeCalls, service.completeID, service.completeStatus)
		}
		if !strings.Contains(stdout.String(), `"completed":true`) {
			t.Fatalf("output = %s", stdout.String())
		}
	})
}

func TestBinariesListAndUpdateUseExactServiceOperations(t *testing.T) {
	t.Parallel()

	t.Run("list", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		service := &fakeService{binariesResult: &rollout.BinaryList{
			Binaries: []rollout.Binary{{
				Sequence: 1, FileName: "app.aab", VersionName: "2.0",
				RolloutStatus: "ENABLED", AppStatus: "REGISTRATION",
			}},
		}}
		dependencies := testDependencies(&stdout, service, nil, nil)
		err := execute(
			NewCommand(dependencies),
			"binaries", "list",
			"--content-id", "000007654321",
			"--app-status", "REGISTRATION",
			"--output", "table",
		)
		if err != nil {
			t.Fatalf("binaries list: %v", err)
		}
		if service.binariesCalls != 1 ||
			service.binariesID != "000007654321" ||
			service.binariesStatus != "REGISTRATION" {
			t.Fatalf("binaries call = %d/%q/%q", service.binariesCalls, service.binariesID, service.binariesStatus)
		}
		for _, text := range []string{"SEQUENCE", "app.aab", "2.0", "ENABLED"} {
			if !strings.Contains(stdout.String(), text) {
				t.Fatalf("table missing %q:\n%s", text, stdout.String())
			}
		}
	})

	for _, function := range []string{"ADD", "REMOVE"} {
		function := function
		t.Run(strings.ToLower(function), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			service := &fakeService{binaryResult: &rollout.MutationResult{
				ResultCode: "0000", ResultMessage: "Ok", Function: function,
			}}
			dependencies := testDependencies(
				&stdout,
				service,
				nil,
				[]byte(`{"function":"`+function+`","binarySeq":"3"}`),
			)
			err := execute(
				NewCommand(dependencies),
				"binaries", "update",
				"--content-id", "000007654321",
				"--file", "binary.json",
				"--confirm",
				"--output", "json",
			)
			if err != nil {
				t.Fatalf("binaries update: %v", err)
			}
			if function == "ADD" {
				if service.addCalls != 1 || service.addID != "000007654321" || service.addSequence != "3" {
					t.Fatalf("add call = %d/%q/%q", service.addCalls, service.addID, service.addSequence)
				}
			} else if service.removeCalls != 1 || service.removeID != "000007654321" || service.removeSequence != "3" {
				t.Fatalf("remove call = %d/%q/%q", service.removeCalls, service.removeID, service.removeSequence)
			}
			if !strings.Contains(stdout.String(), `"function":"`+function+`"`) {
				t.Fatalf("output = %s", stdout.String())
			}
		})
	}
}

func TestBinariesUpdateDryRunDoesNotOpenSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var opens int
	dependencies := Dependencies{
		Printer:  output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) { return []byte(`{"function":"REMOVE","binarySeq":"3"}`), nil },
		OpenService: func(string) (Service, error) {
			opens++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewCommand(dependencies),
		"binaries", "update",
		"--content-id", "000007654321",
		"--file", "binary.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("binaries dry-run: %v", err)
	}
	if opens != 0 {
		t.Fatalf("session opens = %d, want 0", opens)
	}
	if !strings.Contains(stdout.String(), `"action":"remove"`) ||
		!strings.Contains(stdout.String(), `"staged-rollout-binary/000007654321/3"`) {
		t.Fatalf("plan = %s", stdout.String())
	}
}

func TestValidationHappensBeforeSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		file []byte
	}{
		{name: "view ID", args: []string{"rate", "view", "--content-id", "bad", "--app-status", "SALE"}},
		{name: "view status", args: []string{"rate", "view", "--content-id", "000007654321"}},
		{name: "view output", args: []string{"rate", "view", "--content-id", "000007654321", "--app-status", "SALE", "--output", "yaml"}},
		{
			name: "rate unknown JSON",
			args: []string{"rate", "update", "--content-id", "000007654321", "--app-status", "SALE", "--file", "x", "--confirm"},
			file: []byte(`{"rolloutRate":40,"unknown":true}`),
		},
		{
			name: "rate invalid percentage",
			args: []string{"rate", "update", "--content-id", "000007654321", "--app-status", "SALE", "--file", "x", "--confirm"},
			file: []byte(`{"rolloutRate":0}`),
		},
		{
			name: "rate duplicate country",
			args: []string{"rate", "update", "--content-id", "000007654321", "--app-status", "SALE", "--file", "x", "--confirm"},
			file: []byte(`{"rolloutRate":40,"countries":[{"countryCode":"USA","rolloutRate":45},{"countryCode":"USA","rolloutRate":50}]}`),
		},
		{
			name: "binary function",
			args: []string{"binaries", "update", "--content-id", "000007654321", "--file", "x", "--confirm"},
			file: []byte(`{"function":"DISABLE","binarySeq":"1"}`),
		},
		{
			name: "binary sequence",
			args: []string{"binaries", "update", "--content-id", "000007654321", "--file", "x", "--confirm"},
			file: []byte(`{"function":"ADD","binarySeq":"0"}`),
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

func subcommandNames(command *ffcli.Command) []string {
	names := make([]string, len(command.Subcommands))
	for index, subcommand := range command.Subcommands {
		names[index] = subcommand.Name
	}
	return names
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
