package statuswaitcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

const testContentID = "000007654321"

func TestWaitPollsImmediatelyAndPrintsOneFinalExactVariant(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	service := &fakeService{responses: [][]apps.App{{
		{
			ContentID:     testContentID,
			AppStatus:     "SALE",
			ContentStatus: "FOR_SALE",
			Title:         "Published",
		},
		{
			ContentID:     testContentID,
			AppStatus:     "REGISTRATION",
			ContentStatus: "READY_FOR_SALE",
			Title:         "Pending update",
			PackageName:   "com.example.app",
		},
	}}}
	dependencies, clock, openCalls := testDependencies(&stdout, &stderr, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "registration",
		"--until", "READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if *openCalls != 1 || service.calls != 1 || clock.waitCalls != 0 {
		t.Fatalf(
			"open=%d view=%d waits=%d, want 1,1,0",
			*openCalls,
			service.calls,
			clock.waitCalls,
		)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no progress after immediate match", stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("stdout emitted more than one record: %q", stdout.String())
	}
	for _, expected := range []string{
		`"contentId":"000007654321"`,
		`"appStatus":"REGISTRATION"`,
		`"contentStatus":"READY_FOR_SALE"`,
		`"title":"Pending update"`,
		`"outcome":"reached"`,
		`"attempts":1`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), expected)
		}
	}
	if strings.Contains(stdout.String(), "Published") ||
		strings.Contains(stdout.String(), `"contentStatus":"FOR_SALE"`) {
		t.Fatalf("stdout included unselected SALE variant: %q", stdout.String())
	}
}

func TestWaitWritesIntermediateProgressOnlyToStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	service := &fakeService{responses: [][]apps.App{
		{app("REGISTRATION", "UPDATING")},
		{app("REGISTRATION", "UNDER_CONTENT_REVIEW")},
		{app("REGISTRATION", "READY_FOR_CHANGE")},
	}}
	dependencies, clock, _ := testDependencies(&stdout, &stderr, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "READY_FOR_CHANGE",
		"--interval", "1s",
		"--timeout", "10s",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if service.calls != 3 || clock.waitCalls != 2 {
		t.Fatalf("view calls = %d, wait calls = %d", service.calls, clock.waitCalls)
	}
	if strings.Contains(stdout.String(), "UPDATING") ||
		strings.Contains(stdout.String(), "UNDER_CONTENT_REVIEW") {
		t.Fatalf("stdout leaked intermediate states: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"contentStatus":"READY_FOR_CHANGE"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, progress := range []string{
		"current=UPDATING",
		"current=UNDER_CONTENT_REVIEW",
		"target=READY_FOR_CHANGE",
		"attempt=1",
		"attempt=2",
	} {
		if !strings.Contains(stderr.String(), progress) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), progress)
		}
	}
}

func TestWaitCanObserveVariantThatAppearsLater(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	service := &fakeService{responses: [][]apps.App{
		{app("SALE", "FOR_SALE")},
		{app("SALE", "FOR_SALE"), app("REGISTRATION", "REGISTERING")},
	}}
	dependencies, _, _ := testDependencies(&stdout, &stderr, service)
	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "REGISTERING",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if !strings.Contains(stderr.String(), "current=variant not found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"contentStatus":"REGISTERING"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRequestedTerminalStatusIsSuccessful(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{responses: [][]apps.App{{app("REGISTRATION", "CONTENT_REVIEW_REJECTED")}}}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "CONTENT_REVIEW_REJECTED,READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"outcome":"reached"`) ||
		strings.Contains(stdout.String(), `"terminalCategory"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUnrequestedTerminalStatesReturnTypedErrorsAndFinalResults(t *testing.T) {
	tests := []struct {
		status   string
		category TerminalCategory
	}{
		{status: "PRE_REVIEWS_REJECTED", category: TerminalRejected},
		{status: "CONTENT_REVIEW_REJECTED", category: TerminalRejected},
		{status: "DEVICE_TEST_REJECTED", category: TerminalRejected},
		{status: "TEST_CONFIRMATION_REJECTED", category: TerminalRejected},
		{status: "BETA_PRE_REVIEW_REJECTED", category: TerminalRejected},
		{status: "PRE_REVIEWS_CANCELED", category: TerminalCanceled},
		{status: "CONTENT_REVIEW_CANCELED", category: TerminalCanceled},
		{status: "DEVICE_TEST_CANCELED", category: TerminalCanceled},
		{status: "TEST_CONFIRMATION_CANCELED", category: TerminalCanceled},
		{status: "CANCELED", category: TerminalCanceled},
		{status: "TERMINATED", category: TerminalTerminated},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			var stdout bytes.Buffer
			service := &fakeService{responses: [][]apps.App{{app(appStatusFor(test.status), test.status)}}}
			dependencies, clock, _ := testDependencies(&stdout, &bytes.Buffer{}, service)
			err := execute(
				NewCommand(dependencies),
				"--content-id", testContentID,
				"--app-status", appStatusFor(test.status),
				"--until", targetFor(test.status),
				"--interval", "1s",
				"--timeout", "10s",
				"--output", "json",
			)
			var terminalError *TerminalStatusError
			if !errors.As(err, &terminalError) {
				t.Fatalf("error = %v, want *TerminalStatusError", err)
			}
			if terminalError.Category != test.category ||
				terminalError.ContentStatus != test.status {
				t.Fatalf("terminal error = %#v", terminalError)
			}
			if clock.waitCalls != 0 {
				t.Fatalf("wait calls = %d, want 0", clock.waitCalls)
			}
			for _, expected := range []string{
				`"outcome":"terminal"`,
				`"terminalCategory":"` + string(test.category) + `"`,
				`"contentStatus":"` + test.status + `"`,
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("stdout = %q, missing %q", stdout.String(), expected)
				}
			}
		})
	}
}

func TestTimeoutIsTypedPreciseAndEmitsLastStatusOnce(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	service := &fakeService{defaultResponse: []apps.App{app("REGISTRATION", "UPDATING")}}
	dependencies, clock, _ := testDependencies(&stdout, &stderr, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "3s",
		"--output", "json",
	)
	var timeoutError *TimeoutError
	if !errors.As(err, &timeoutError) {
		t.Fatalf("error = %v, want *TimeoutError", err)
	}
	if timeoutError.Timeout != 3*time.Second ||
		timeoutError.Attempts != 3 ||
		timeoutError.ContentStatus != "UPDATING" {
		t.Fatalf("timeout error = %#v", timeoutError)
	}
	if service.calls != 3 || clock.waitCalls != 3 {
		t.Fatalf("view calls = %d, waits = %d", service.calls, clock.waitCalls)
	}
	if strings.Count(stdout.String(), "\n") != 1 ||
		!strings.Contains(stdout.String(), `"outcome":"timeout"`) ||
		!strings.Contains(stdout.String(), `"contentStatus":"UPDATING"`) ||
		!strings.Contains(stdout.String(), `"attempts":3`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(err.Error(), "timed out after 3s") ||
		!strings.Contains(err.Error(), "last status: UPDATING") {
		t.Fatalf("error = %v", err)
	}
}

func TestTimeoutPreciselyReportsMissingVariant(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{defaultResponse: []apps.App{app("SALE", "FOR_SALE")}}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "1s",
	)
	var timeoutError *TimeoutError
	if !errors.As(err, &timeoutError) {
		t.Fatalf("error = %v, want *TimeoutError", err)
	}
	if timeoutError.ContentStatus != "" ||
		!strings.Contains(err.Error(), "last status: variant not found") {
		t.Fatalf("timeout error = %#v (%v)", timeoutError, err)
	}
	if strings.Contains(stdout.String(), `"contentStatus"`) {
		t.Fatalf("stdout invented a content status: %q", stdout.String())
	}
}

func TestContextCanceledBeforeOpenHasNoSessionOrOutputSideEffects(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{}
	dependencies, _, openCalls := testDependencies(&stdout, &bytes.Buffer{}, service)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := executeContext(
		ctx,
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--until", "FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if *openCalls != 0 || service.calls != 0 || stdout.Len() != 0 {
		t.Fatalf("open=%d view=%d stdout=%q", *openCalls, service.calls, stdout.String())
	}
}

func TestContextCancellationDuringWaitStopsWithoutFinalOutput(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{defaultResponse: []apps.App{app("REGISTRATION", "UPDATING")}}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)
	ctx, cancel := context.WithCancel(context.Background())
	dependencies.Wait = func(waitContext context.Context, _ time.Duration) error {
		cancel()
		<-waitContext.Done()
		return waitContext.Err()
	}

	err := executeContext(
		ctx,
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if service.calls != 1 || stdout.Len() != 0 {
		t.Fatalf("view calls = %d, stdout = %q", service.calls, stdout.String())
	}
}

func TestValidationHappensBeforeOpeningSession(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "invalid content ID",
			args: []string{
				"--content-id", "123",
				"--app-status", "SALE",
				"--until", "FOR_SALE",
			},
		},
		{
			name: "missing app status",
			args: []string{
				"--content-id", testContentID,
				"--until", "FOR_SALE",
			},
		},
		{
			name: "invalid app status",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "UPDATING",
				"--until", "FOR_SALE",
			},
		},
		{
			name: "missing until",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
			},
		},
		{
			name: "unknown until",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "IN_REVIEW",
			},
		},
		{
			name: "empty until item",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE,",
			},
		},
		{
			name: "duplicate until",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE,for_sale",
			},
		},
		{
			name: "short interval",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--interval", "999ms",
			},
		},
		{
			name: "long interval",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--interval", "5m1s",
			},
		},
		{
			name: "short timeout",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--timeout", "999ms",
			},
		},
		{
			name: "long timeout",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--timeout", "24h1s",
			},
		},
		{
			name: "invalid output",
			args: []string{
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--output", "xml",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			dependencies, _, openCalls := testDependencies(&bytes.Buffer{}, &bytes.Buffer{}, service)
			err := execute(NewCommand(dependencies), test.args...)
			if err == nil {
				t.Fatal("error = nil")
			}
			if *openCalls != 0 || service.calls != 0 {
				t.Fatalf("open calls = %d, view calls = %d", *openCalls, service.calls)
			}
		})
	}
}

func TestEveryAcceptedUntilStatusComesFromSamsungMapping(t *testing.T) {
	for _, status := range contentStatuses {
		targets, err := parseTargets(status)
		if err != nil {
			t.Fatalf("parseTargets(%q) error = %v", status, err)
		}
		if _, ok := targets[status]; !ok {
			t.Fatalf("parseTargets(%q) = %v", status, targets)
		}
	}
	for _, invented := range []string{
		"IN_REVIEW",
		"APPROVED",
		"PUBLISHED",
		"FAILED",
		"REJECTED",
	} {
		if _, err := parseTargets(invented); err == nil {
			t.Fatalf("parseTargets(%q) error = nil", invented)
		}
	}
}

func TestWaitRejectsDuplicateExactVariantsAndMismatchedContentIDs(t *testing.T) {
	tests := []struct {
		name      string
		responses []apps.App
		contains  string
	}{
		{
			name: "duplicate",
			responses: []apps.App{
				app("SALE", "FOR_SALE"),
				app("SALE", "SUSPENDED"),
			},
			contains: "multiple SALE records",
		},
		{
			name: "mismatched content ID",
			responses: []apps.App{{
				ContentID:     "000000000001",
				AppStatus:     "SALE",
				ContentStatus: "FOR_SALE",
			}},
			contains: "returned content ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			service := &fakeService{responses: [][]apps.App{test.responses}}
			dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)
			err := execute(
				NewCommand(dependencies),
				"--content-id", testContentID,
				"--app-status", "SALE",
				"--until", "FOR_SALE",
				"--interval", "1s",
				"--timeout", "10s",
			)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestUndocumentedCurrentStatusFailsInsteadOfGuessing(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{responses: [][]apps.App{{app("REGISTRATION", "SAMSUNG_NEW_STATE")}}}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "REGISTRATION",
		"--until", "READY_FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if err == nil || !strings.Contains(err.Error(), "undocumented contentStatus") {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestServiceErrorsPassThroughWithoutFinalOutput(t *testing.T) {
	sentinel := errors.New("Samsung unavailable")
	var stdout bytes.Buffer
	service := &fakeService{err: sentinel}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--until", "FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestProgressWriterFailureStopsBeforeAnotherPoll(t *testing.T) {
	service := &fakeService{defaultResponse: []apps.App{app("SALE", "SUSPENDED")}}
	dependencies, clock, _ := testDependencies(&bytes.Buffer{}, &bytes.Buffer{}, service)
	dependencies.Stderr = failingWriter{}

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--until", "FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
	)
	if err == nil || !strings.Contains(err.Error(), "write status progress") {
		t.Fatalf("error = %v", err)
	}
	if service.calls != 1 || clock.waitCalls != 0 {
		t.Fatalf("view calls = %d, waits = %d", service.calls, clock.waitCalls)
	}
}

func TestTableOutputContainsFinalState(t *testing.T) {
	var stdout bytes.Buffer
	service := &fakeService{responses: [][]apps.App{{app("SALE", "FOR_SALE")}}}
	dependencies, _, _ := testDependencies(&stdout, &bytes.Buffer{}, service)

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--until", "FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
		"--output", "table",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	for _, expected := range []string{
		"CONTENT ID",
		"APP STATUS",
		"CONTENT STATUS",
		"OUTCOME",
		testContentID,
		"FOR_SALE",
		"reached",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), expected)
		}
	}
}

func TestOpenServiceHappensAfterValidationAndExactlyOnce(t *testing.T) {
	service := &fakeService{responses: [][]apps.App{{app("SALE", "FOR_SALE")}}}
	dependencies, _, openCalls := testDependencies(&bytes.Buffer{}, &bytes.Buffer{}, service)
	var openedProfile string
	dependencies.OpenService = func(profile string) (Service, error) {
		*openCalls++
		openedProfile = profile
		return service, nil
	}

	err := execute(
		NewCommand(dependencies),
		"--content-id", testContentID,
		"--app-status", "SALE",
		"--until", "FOR_SALE",
		"--interval", "1s",
		"--timeout", "10s",
		"--profile", " production ",
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if *openCalls != 1 || openedProfile != "production" {
		t.Fatalf("open calls = %d, profile = %q", *openCalls, openedProfile)
	}
}

func app(appStatus string, contentStatus string) apps.App {
	return apps.App{
		ContentID:     testContentID,
		AppStatus:     appStatus,
		ContentStatus: contentStatus,
		Title:         "Example",
		PackageName:   "com.example.app",
	}
}

func appStatusFor(status string) string {
	if status == "TERMINATED" {
		return "SALE"
	}
	return "REGISTRATION"
}

func targetFor(status string) string {
	if appStatusFor(status) == "SALE" {
		return "FOR_SALE"
	}
	return "READY_FOR_SALE"
}

func testDependencies(
	stdout *bytes.Buffer,
	stderr io.Writer,
	service Service,
) (Dependencies, *fakeClock, *int) {
	clock := &fakeClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	openCalls := 0
	return Dependencies{
		Stderr:  stderr,
		Printer: output.NewPrinter(stdout, func(io.Writer) bool { return false }),
		OpenService: func(string) (Service, error) {
			openCalls++
			return service, nil
		},
		Now:  clock.Now,
		Wait: clock.Wait,
	}, clock, &openCalls
}

func execute(command *ffcli.Command, args ...string) error {
	return executeContext(context.Background(), command, args...)
}

func executeContext(ctx context.Context, command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(ctx)
}

type fakeService struct {
	responses       [][]apps.App
	defaultResponse []apps.App
	err             error
	calls           int
}

func (service *fakeService) View(context.Context, string) ([]apps.App, error) {
	service.calls++
	if service.err != nil {
		return nil, service.err
	}
	if service.calls <= len(service.responses) {
		return append([]apps.App(nil), service.responses[service.calls-1]...), nil
	}
	return append([]apps.App(nil), service.defaultResponse...), nil
}

type fakeClock struct {
	now       time.Time
	waitCalls int
}

func (clock *fakeClock) Now() time.Time {
	return clock.now
}

func (clock *fakeClock) Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.waitCalls++
	clock.now = clock.now.Add(duration)
	return nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("writer failed")
}

func ExampleNewCommand() {
	fmt.Println("gsc apps status wait --content-id 000007654321 --app-status REGISTRATION --until READY_FOR_SALE")
	// Output: gsc apps status wait --content-id 000007654321 --app-status REGISTRATION --until READY_FOR_SALE
}
