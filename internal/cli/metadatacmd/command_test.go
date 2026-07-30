package metadatacmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
)

const testContentID = "000007654321"

type fakeService struct {
	viewResults [][]apps.App
	viewErr     error
	viewCalls   int
	viewID      string

	updateResult  *samsungcontent.Result
	updateErr     error
	updateCalls   int
	updateID      string
	updatePayload json.RawMessage
}

func (service *fakeService) View(
	_ context.Context,
	contentID string,
) ([]apps.App, error) {
	service.viewCalls++
	service.viewID = contentID
	if service.viewErr != nil {
		return nil, service.viewErr
	}
	if len(service.viewResults) == 0 {
		return nil, nil
	}
	index := min(service.viewCalls-1, len(service.viewResults)-1)
	return service.viewResults[index], nil
}

func (service *fakeService) Update(
	_ context.Context,
	contentID string,
	payload json.RawMessage,
) (*samsungcontent.Result, error) {
	service.updateCalls++
	service.updateID = contentID
	service.updatePayload = append(json.RawMessage(nil), payload...)
	return service.updateResult, service.updateErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "metadata" || len(command.Subcommands) != 4 {
		t.Fatalf("command = %#v", command)
	}
	got := make([]string, 0, len(command.Subcommands))
	for _, subcommand := range command.Subcommands {
		got = append(got, subcommand.Name)
	}
	if strings.Join(got, ",") != "pull,validate,diff,apply" {
		t.Fatalf("subcommands = %v", got)
	}
}

func TestNetworkValidationPrecedesBundleAndSessionAccess(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pull", "diff", "apply"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			readCalls := 0
			openCalls := 0
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				ReadBundle: func(string) (*metadata.Bundle, error) {
					readCalls++
					return nil, errors.New("unexpected read")
				},
				OpenService: func(string) (Service, error) {
					openCalls++
					return nil, errors.New("unexpected open")
				},
			}
			err := execute(
				NewCommand(dependencies),
				command,
				"--content-id", testContentID,
			)
			if err == nil || !strings.Contains(err.Error(), "--app-status") {
				t.Fatalf("%s error = %v", command, err)
			}
			if readCalls != 0 || openCalls != 0 {
				t.Fatalf(
					"%s side effects: read=%d open=%d",
					command,
					readCalls,
					openCalls,
				)
			}
		})
	}
}

func TestValidateIsOfflineAndStructured(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	bundle := bundleFor(t, appSource("SALE", "Original", `null`))
	openCalls := 0
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadBundle: func(path string) (*metadata.Bundle, error) {
			if path != "custom" {
				t.Fatalf("bundle path = %q", path)
			}
			return &bundle, nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return nil, errors.New("unexpected open")
		},
	}
	err := execute(
		NewCommand(dependencies),
		"validate",
		"--dir", "custom",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("session opens = %d, want 0", openCalls)
	}
	for _, want := range []string{
		`"valid":true`,
		`"contentId":"` + testContentID + `"`,
		`"appStatus":"SALE"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestBundleValidationAndIdentityMismatchPrecedeSession(t *testing.T) {
	t.Parallel()

	openCalls := 0
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		ReadBundle: func(string) (*metadata.Bundle, error) {
			return nil, errors.New("metadata envelope paid must be Y or N")
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return nil, errors.New("unexpected open")
		},
	}
	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--confirm",
	)
	if err == nil || !strings.Contains(err.Error(), "paid must be Y or N") {
		t.Fatalf("invalid bundle error = %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("invalid bundle opened %d sessions", openCalls)
	}

	registration := bundleFor(
		t,
		appSource("REGISTRATION", "Draft", `null`),
	)
	dependencies.ReadBundle = func(string) (*metadata.Bundle, error) {
		return &registration, nil
	}
	err = execute(
		NewCommand(dependencies),
		"diff",
		"--content-id", testContentID,
		"--app-status", "SALE",
	)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("identity mismatch opened %d sessions", openCalls)
	}
}

func TestPullSelectsExactVariantAndRequiresForceToOverwrite(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "metadata")
	service := &fakeService{viewResults: [][]apps.App{{
		rawApp(t, appSource("SALE", "Public", `null`)),
		rawApp(t, appSource("REGISTRATION", "Draft", `null`)),
	}}}
	var stdout bytes.Buffer
	dependencies := Dependencies{
		Printer:     output.NewPrinter(&stdout, nil),
		Now:         func() time.Time { return time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC) },
		ReadBundle:  metadata.ReadBundle,
		WriteBundle: metadata.WriteBundle,
		OpenService: func(string) (Service, error) {
			return service, nil
		},
	}
	args := []string{
		"pull",
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--dir", directory,
		"--output", "json",
	}
	if err := execute(NewCommand(dependencies), args...); err != nil {
		t.Fatalf("pull: %v", err)
	}
	pulled, err := metadata.ReadBundle(directory)
	if err != nil {
		t.Fatalf("read pulled bundle: %v", err)
	}
	if pulled.Manifest.AppStatus != metadata.AppStatusRegistration ||
		!strings.Contains(string(pulled.Source), `"Draft"`) {
		t.Fatalf("pulled bundle = %+v, source = %s", pulled.Manifest, pulled.Source)
	}

	err = execute(NewCommand(dependencies), args...)
	if !errors.Is(err, metadata.ErrOverwrite) {
		t.Fatalf("second pull error = %v, want ErrOverwrite", err)
	}
	if err := execute(
		NewCommand(dependencies),
		append(args, "--force")...,
	); err != nil {
		t.Fatalf("forced pull: %v", err)
	}
}

func TestPullRejectsDuplicateExactVariant(t *testing.T) {
	t.Parallel()

	record := rawApp(t, appSource("SALE", "Original", `null`))
	service := &fakeService{viewResults: [][]apps.App{{record, record}}}
	dependencies := basicDependencies(io.Discard, service, bundleFor(t, record.Raw))
	dependencies.Now = time.Now
	dependencies.WriteBundle = func(
		string,
		metadata.Bundle,
		metadata.WriteOptions,
	) error {
		t.Fatal("bundle must not be written for an ambiguous response")
		return nil
	}
	err := execute(
		NewCommand(dependencies),
		"pull",
		"--content-id", testContentID,
		"--app-status", "SALE",
	)
	if err == nil || !strings.Contains(err.Error(), "multiple SALE") {
		t.Fatalf("pull ambiguity error = %v", err)
	}
}

func TestDiffReportsDeterministicPlanWithoutMutation(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `[{"reuseYn":true,"filekey":"one"}]`)
	bundle := bundleFor(t, source)
	bundle.Metadata = json.RawMessage(`{
		"contentId":"000007654321",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"Changed",
		"screenshots":[]
	}`)
	service := &fakeService{viewResults: [][]apps.App{{rawApp(t, source)}}}
	var stdout bytes.Buffer
	dependencies := basicDependencies(&stdout, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"diff",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if service.updateCalls != 0 {
		t.Fatalf("update calls = %d", service.updateCalls)
	}
	outputValue := stdout.String()
	titleIndex := strings.Index(outputValue, `"/appTitle"`)
	screenshotIndex := strings.Index(outputValue, `"/screenshots"`)
	if titleIndex < 0 || screenshotIndex <= titleIndex {
		t.Fatalf("changes are not deterministic: %s", outputValue)
	}
	for _, want := range []string{
		`"kind":"clear"`,
		`"destructive":true`,
		`"mutationsPerformed":false`,
	} {
		if !strings.Contains(outputValue, want) {
			t.Fatalf("diff output %q does not contain %q", outputValue, want)
		}
	}
}

func TestApplyRejectsSourceDriftBeforeMutation(t *testing.T) {
	t.Parallel()

	original := appSource("SALE", "Original", `null`)
	bundle := bundleFor(t, original)
	bundle.Metadata = desiredMetadata("Changed")
	service := &fakeService{
		viewResults: [][]apps.App{{rawApp(
			t,
			appSource("SALE", "Changed remotely", `null`),
		)}},
	}
	dependencies := basicDependencies(io.Discard, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--confirm",
	)
	if !errors.Is(err, metadata.ErrDrift) {
		t.Fatalf("apply drift error = %v", err)
	}
	if service.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", service.updateCalls)
	}
}

func TestApplyNoOpNeedsNoConfirmation(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `null`)
	bundle := bundleFor(t, source)
	service := &fakeService{viewResults: [][]apps.App{{rawApp(t, source)}}}
	var stdout bytes.Buffer
	dependencies := basicDependencies(&stdout, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("no-op apply: %v", err)
	}
	if service.updateCalls != 0 {
		t.Fatalf("update calls = %d", service.updateCalls)
	}
	if !strings.Contains(stdout.String(), `"changes":[]`) {
		t.Fatalf("no-op output = %s", stdout.String())
	}
}

func TestApplyDryRunPlansAfterReadWithoutMutation(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `null`)
	bundle := bundleFor(t, source)
	bundle.Metadata = desiredMetadata("Changed")
	service := &fakeService{viewResults: [][]apps.App{{rawApp(t, source)}}}
	var stdout bytes.Buffer
	dependencies := basicDependencies(&stdout, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if service.viewCalls != 1 || service.updateCalls != 0 {
		t.Fatalf(
			"calls: view=%d update=%d",
			service.viewCalls,
			service.updateCalls,
		)
	}
	if !strings.Contains(stdout.String(), `"/appTitle"`) ||
		!strings.Contains(stdout.String(), `"requiresConfirmation":true`) {
		t.Fatalf("dry-run output = %s", stdout.String())
	}
}

func TestApplyRequiresConfirmationAfterPlanning(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `null`)
	bundle := bundleFor(t, source)
	bundle.Metadata = desiredMetadata("Changed")
	service := &fakeService{viewResults: [][]apps.App{{rawApp(t, source)}}}
	dependencies := basicDependencies(io.Discard, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
	)
	if !errors.Is(err, shared.ErrConfirmationRequired) {
		t.Fatalf("confirmation error = %v", err)
	}
	if service.viewCalls != 1 || service.updateCalls != 0 {
		t.Fatalf(
			"calls: view=%d update=%d",
			service.viewCalls,
			service.updateCalls,
		)
	}
}

func TestApplyPerformsOneUpdateAndVerifiesReadback(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `null`)
	changed := appSource("SALE", "Changed", `null`)
	bundle := bundleFor(t, source)
	bundle.Metadata = desiredMetadata("Changed")
	service := &fakeService{
		viewResults: [][]apps.App{
			{rawApp(t, source)},
			{rawApp(t, changed)},
		},
		updateResult: &samsungcontent.Result{
			ResultCode:    "0000",
			ResultMessage: "Success",
		},
	}
	var stdout bytes.Buffer
	dependencies := basicDependencies(&stdout, service, bundle)

	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if service.viewCalls != 2 || service.updateCalls != 1 {
		t.Fatalf(
			"calls: view=%d update=%d",
			service.viewCalls,
			service.updateCalls,
		)
	}
	if service.updateID != testContentID ||
		!bytes.Equal(service.updatePayload, bundle.Metadata) {
		t.Fatalf("update id=%q payload=%s", service.updateID, service.updatePayload)
	}
	for _, want := range []string{
		`"readbackVerified":true`,
		`"mutationsPerformed":true`,
		`"resultCode":"0000"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("apply output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestApplyFailsWhenReadbackDoesNotMatch(t *testing.T) {
	t.Parallel()

	source := appSource("SALE", "Original", `null`)
	bundle := bundleFor(t, source)
	bundle.Metadata = desiredMetadata("Changed")
	service := &fakeService{
		viewResults: [][]apps.App{
			{rawApp(t, source)},
			{rawApp(t, source)},
		},
		updateResult: &samsungcontent.Result{ResultCode: "0000"},
	}
	dependencies := basicDependencies(io.Discard, service, bundle)
	err := execute(
		NewCommand(dependencies),
		"apply",
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--confirm",
	)
	if err == nil || !strings.Contains(err.Error(), "still differs") {
		t.Fatalf("readback error = %v", err)
	}
	if service.updateCalls != 1 {
		t.Fatalf("update calls = %d, want exactly 1", service.updateCalls)
	}
}

func basicDependencies(
	stdout io.Writer,
	service Service,
	bundle metadata.Bundle,
) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		ReadBundle: func(string) (*metadata.Bundle, error) {
			copy := bundle
			return &copy, nil
		},
		OpenService: func(string) (Service, error) {
			return service, nil
		},
	}
}

func bundleFor(t *testing.T, source json.RawMessage) metadata.Bundle {
	t.Helper()
	bundle, err := metadata.NewBundle(
		rawApp(t, source),
		time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	return *bundle
}

func rawApp(t *testing.T, raw json.RawMessage) apps.App {
	t.Helper()
	var app apps.App
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	return app
}

func appSource(
	status string,
	title string,
	screenshots string,
) json.RawMessage {
	return json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"appStatus":"` + status + `",
		"contentStatus":"REGISTERING",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":` + quoted(title) + `,
		"screenshots":` + screenshots + `,
		"unknownResponseField":"preserved only in source"
	}`)
}

func desiredMetadata(title string) json.RawMessage {
	return json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":` + quoted(title) + `,
		"screenshots":null
	}`)
}

func quoted(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}

func TestUsageErrorsRemainTyped(t *testing.T) {
	t.Parallel()

	err := execute(NewCommand(Dependencies{}), "validate", "unexpected")
	var usageError *shared.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("error = %T %v, want UsageError", err, err)
	}
}
