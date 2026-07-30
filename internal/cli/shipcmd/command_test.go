package shipcmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

const testContentID = "000007654321"

type capturePrinter struct {
	format output.Format
	value  any
	calls  int
}

func (printer *capturePrinter) Print(format output.Format, value any) error {
	printer.calls++
	printer.format = format
	printer.value = value
	return nil
}

func TestCommandShape(t *testing.T) {
	t.Parallel()
	command := NewCommand(Dependencies{})
	if command.Name != "ship" || len(command.Subcommands) != 2 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "plan" ||
		command.Subcommands[1].Name != "run" {
		t.Fatalf("subcommands = %q, %q", command.Subcommands[0].Name, command.Subcommands[1].Name)
	}
}

func TestPlanIsDeterministicAndEntirelyOffline(t *testing.T) {
	t.Parallel()
	fixture := newCommandFixture(t)
	printer := &capturePrinter{}
	openCalls := 0
	storeCalls := 0
	lockCalls := 0
	dependencies := Dependencies{
		Printer: printer,
		OpenRemote: func(string) (ship.Remote, error) {
			openCalls++
			return nil, errors.New("unexpected remote")
		},
		NewStore: func(string) (ship.CheckpointStore, error) {
			storeCalls++
			return nil, errors.New("unexpected store")
		},
		AcquireLock: func(string) (Lock, error) {
			lockCalls++
			return nil, errors.New("unexpected lock")
		},
	}
	args := fixture.arguments("plan")
	if err := execute(NewCommand(dependencies), args...); err != nil {
		t.Fatal(err)
	}
	first, ok := printer.value.(PlanResult)
	if !ok {
		t.Fatalf("output = %T", printer.value)
	}
	if first.AppStatus != ship.Registration ||
		!first.RequiresConfirmation ||
		first.MutationsPerformed {
		t.Fatalf("plan output = %#v", first)
	}
	if openCalls != 0 || storeCalls != 0 || lockCalls != 0 {
		t.Fatalf("offline calls: open=%d store=%d lock=%d", openCalls, storeCalls, lockCalls)
	}

	if err := execute(NewCommand(dependencies), args...); err != nil {
		t.Fatal(err)
	}
	second := printer.value.(PlanResult)
	if first.ID != second.ID {
		t.Fatalf("plan IDs differ: %q != %q", first.ID, second.ID)
	}
}

func TestRunDryRunDoesNotOpenCredentialsCheckpointOrLock(t *testing.T) {
	t.Parallel()
	fixture := newCommandFixture(t)
	printer := &capturePrinter{}
	var calls int
	dependencies := Dependencies{
		Printer: printer,
		OpenRemote: func(string) (ship.Remote, error) {
			calls++
			return nil, errors.New("unexpected remote")
		},
		NewStore: func(string) (ship.CheckpointStore, error) {
			calls++
			return nil, errors.New("unexpected store")
		},
		AcquireLock: func(string) (Lock, error) {
			calls++
			return nil, errors.New("unexpected lock")
		},
	}
	args := append(fixture.arguments("run"), "--dry-run")
	if err := execute(NewCommand(dependencies), args...); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("side-effect dependency calls = %d", calls)
	}
	if _, ok := printer.value.(PlanResult); !ok {
		t.Fatalf("output = %T", printer.value)
	}
}

func TestRunRequiresConfirmationBeforeCheckpointLockOrCredentials(t *testing.T) {
	t.Parallel()
	fixture := newCommandFixture(t)
	var calls int
	dependencies := Dependencies{
		Printer: &capturePrinter{},
		OpenRemote: func(string) (ship.Remote, error) {
			calls++
			return nil, errors.New("unexpected remote")
		},
		NewStore: func(string) (ship.CheckpointStore, error) {
			calls++
			return nil, errors.New("unexpected store")
		},
		AcquireLock: func(string) (Lock, error) {
			calls++
			return nil, errors.New("unexpected lock")
		},
	}
	err := execute(NewCommand(dependencies), fixture.arguments("run")...)
	if err == nil || !strings.Contains(err.Error(), "explicit confirmation required") {
		t.Fatalf("error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("side-effect dependency calls = %d", calls)
	}
}

func TestInvalidLocalInputsPrecedeConfirmationAndDependencies(t *testing.T) {
	t.Parallel()
	fixture := newCommandFixture(t)
	args := fixture.arguments("run")
	args[2] = "bad"
	var calls int
	dependencies := Dependencies{
		Printer: &capturePrinter{},
		OpenRemote: func(string) (ship.Remote, error) {
			calls++
			return nil, nil
		},
	}
	err := execute(NewCommand(dependencies), append(args, "--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "12 digits") {
		t.Fatalf("error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("remote calls = %d", calls)
	}
}

func TestRunResultOmitsPrivateCheckpointFields(t *testing.T) {
	t.Parallel()
	result := newRunResult(ship.Plan{
		ID:        strings.Repeat("a", 64),
		ContentID: testContentID,
	}, ship.Result{
		Complete:           true,
		MutationsPerformed: true,
		Checkpoint: ship.Checkpoint{
			UploadSessionID: "private-session",
			FileKey:         "private-file-key",
			CompletedSteps:  ship.OrderedSteps(),
		},
	})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !result.MutationsPerformed {
		t.Fatalf("output lost mutation evidence: %s", data)
	}
	for _, secret := range []string{"private-session", "private-file-key", "uploadSessionId", "fileKey"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("output leaked %q: %s", secret, data)
		}
	}
}

type commandFixture struct {
	binary   string
	metadata string
}

func newCommandFixture(t *testing.T) commandFixture {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "app.aab")
	if err := os.WriteFile(binary, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"appStatus":"REGISTRATION",
		"contentStatus":"REGISTERING",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"Before",
		"binaryList":[]
	}`)
	bundle, err := metadata.NewBundle(apps.App{
		ContentID:     testContentID,
		AppStatus:     "REGISTRATION",
		ContentStatus: "REGISTERING",
		Raw:           raw,
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	bundle.Metadata = json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"After"
	}`)
	metadataDirectory := filepath.Join(directory, "metadata")
	if err := metadata.WriteBundle(metadataDirectory, *bundle, metadata.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	return commandFixture{binary: binary, metadata: metadataDirectory}
}

func (fixture commandFixture) arguments(command string) []string {
	return []string{
		command,
		"--content-id", testContentID,
		"--binary", fixture.binary,
		"--metadata-dir", fixture.metadata,
		"--gms", "N",
		"--output", "json",
	}
}

func execute(command *ffcli.Command, args ...string) error {
	return command.ParseAndRun(context.Background(), args)
}
