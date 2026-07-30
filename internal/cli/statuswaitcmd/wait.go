// Package statuswaitcmd implements read-only Galaxy Store app status waiting.
package statuswaitcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

const (
	defaultInterval = 15 * time.Second
	minimumInterval = time.Second
	maximumInterval = 5 * time.Minute
	defaultTimeout  = 30 * time.Minute
	minimumTimeout  = time.Second
	maximumTimeout  = 24 * time.Hour
)

// Service is the apps.Service.View-compatible read surface used by the waiter.
type Service interface {
	View(context.Context, string) ([]apps.App, error)
}

// Printer renders only the waiter's final structured result.
type Printer interface {
	Print(output.Format, any) error
}

// WaitFunc pauses until the next poll or context cancellation.
type WaitFunc func(context.Context, time.Duration) error

// Dependencies keeps polling, time, output, and session resolution injectable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenService func(profile string) (Service, error)
	Now         func() time.Time
	Wait        WaitFunc
}

// TerminalCategory classifies Samsung terminal failure states.
type TerminalCategory string

const (
	TerminalRejected   TerminalCategory = "rejected"
	TerminalCanceled   TerminalCategory = "canceled"
	TerminalTerminated TerminalCategory = "terminated"
)

// TerminalStatusError reports a terminal state that was not requested.
type TerminalStatusError struct {
	ContentID     string
	AppStatus     string
	ContentStatus string
	Category      TerminalCategory
}

func (err *TerminalStatusError) Error() string {
	return fmt.Sprintf(
		"app %s %s reached terminal %s status %s before a requested status",
		err.ContentID,
		err.AppStatus,
		err.Category,
		err.ContentStatus,
	)
}

// TimeoutError reports that the configured wait duration elapsed.
type TimeoutError struct {
	ContentID     string
	AppStatus     string
	ContentStatus string
	Timeout       time.Duration
	Attempts      int
}

func (err *TimeoutError) Error() string {
	current := err.ContentStatus
	if current == "" {
		current = "variant not found"
	}
	return fmt.Sprintf(
		"timed out after %s waiting for app %s %s; last status: %s (%d attempts)",
		err.Timeout,
		err.ContentID,
		err.AppStatus,
		current,
		err.Attempts,
	)
}

// Result is the single structured record emitted when waiting finishes.
type Result struct {
	ContentID        string           `json:"contentId"`
	AppStatus        string           `json:"appStatus"`
	ContentStatus    string           `json:"contentStatus,omitempty"`
	Title            string           `json:"title,omitempty"`
	PackageName      string           `json:"packageName,omitempty"`
	Outcome          string           `json:"outcome"`
	TerminalCategory TerminalCategory `json:"terminalCategory,omitempty"`
	Attempts         int              `json:"attempts"`
	Elapsed          string           `json:"elapsed"`
}

// OutputHeaders implements output.RowSource.
func (result Result) OutputHeaders() []string {
	return []string{
		"CONTENT ID",
		"APP STATUS",
		"CONTENT STATUS",
		"OUTCOME",
		"ATTEMPTS",
		"ELAPSED",
	}
}

// OutputRows implements output.RowSource.
func (result Result) OutputRows() [][]string {
	return [][]string{{
		result.ContentID,
		result.AppStatus,
		result.ContentStatus,
		result.Outcome,
		fmt.Sprint(result.Attempts),
		result.Elapsed,
	}}
}

type options struct {
	ContentID string
	AppStatus string
	Until     string
	Interval  time.Duration
	Timeout   time.Duration
	Profile   string
	Output    string
}

// DefaultDependencies creates production dependencies that only open an
// existing access-token session. It never signs a JWT or mints a token.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
) (Dependencies, error) {
	factory, err := session.DefaultFactory()
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		Stderr:  stderr,
		Printer: output.NewPrinter(stdout, isTerminal),
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			if active == nil || active.Client == nil {
				return nil, errors.New("open Galaxy Store session: client is nil")
			}
			return apps.New(active.Client)
		},
		Now:  time.Now,
		Wait: waitWithContext,
	}, nil
}

// NewCommand creates a wait command intended for the gsc apps status group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	flags := flag.NewFlagSet("apps status wait", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var values options
	values.Interval = defaultInterval
	values.Timeout = defaultTimeout
	flags.StringVar(&values.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&values.AppStatus, "app-status", "", "Required app variant: SALE or REGISTRATION")
	flags.StringVar(&values.Until, "until", "", "Required comma-separated Samsung contentStatus values")
	flags.DurationVar(&values.Interval, "interval", defaultInterval, "Polling interval from 1s through 5m")
	flags.DurationVar(&values.Timeout, "timeout", defaultTimeout, "Maximum wait from 1s through 24h")
	flags.StringVar(&values.Profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&values.Output, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "wait",
		ShortUsage: "gsc apps status wait --content-id ID --app-status SALE|REGISTRATION --until STATUS[,STATUS] [flags]",
		ShortHelp:  "Wait for one exact Galaxy Store app variant to reach a content status.",
		LongHelp: `Wait for one exact Galaxy Store app variant to reach a content status.

The first status read happens immediately. SALE and REGISTRATION are never
inferred because both records can exist simultaneously. Progress is written to
stderr; stdout receives one final structured result.

Examples:
  gsc apps status wait --content-id 000007654321 --app-status REGISTRATION --until READY_FOR_SALE
  gsc apps status wait --content-id 000007654321 --app-status SALE --until FOR_SALE,SUSPENDED --interval 10s --timeout 20m`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps status wait does not accept positional arguments")
			}
			return run(ctx, dependencies, values)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func run(ctx context.Context, dependencies Dependencies, values options) error {
	contentID := strings.TrimSpace(values.ContentID)
	if contentID != values.ContentID {
		return shared.UsageErrorf("--content-id must not have surrounding whitespace")
	}
	if err := shared.ValidateContentID(contentID); err != nil {
		return err
	}
	appStatus, err := shared.NormalizeAppStatus(values.AppStatus)
	if err != nil {
		return err
	}
	targets, err := parseTargets(values.Until)
	if err != nil {
		return err
	}
	if err := validateDurations(values.Interval, values.Timeout); err != nil {
		return err
	}
	format, err := parseOutput(values.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	service, err := dependencies.OpenService(strings.TrimSpace(values.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store session: apps service is nil")
	}

	started := dependencies.Now()
	deadline := started.Add(values.Timeout)
	waitContext, cancel := context.WithTimeout(ctx, values.Timeout)
	defer cancel()

	var attempts int
	var last *apps.App
	for {
		if err := waitContext.Err(); err != nil {
			return contextOrTimeout(ctx, values, appStatus, last, attempts, started, dependencies, format)
		}

		attempts++
		records, viewErr := service.View(waitContext, contentID)
		if viewErr != nil {
			if waitContext.Err() != nil {
				return contextOrTimeout(ctx, values, appStatus, last, attempts, started, dependencies, format)
			}
			return viewErr
		}
		selected, selectErr := selectVariant(records, contentID, appStatus)
		if selectErr != nil {
			return selectErr
		}
		last = selected

		if selected != nil {
			status := strings.TrimSpace(selected.ContentStatus)
			if !knownContentStatus(status) {
				return fmt.Errorf(
					"Samsung returned undocumented contentStatus %q for app %s %s",
					status,
					contentID,
					appStatus,
				)
			}
			if _, reached := targets[status]; reached {
				return printFinal(dependencies.Printer, format, newResult(
					contentID,
					appStatus,
					selected,
					"reached",
					"",
					attempts,
					dependencies.Now().Sub(started),
				), nil)
			}
			if category, terminal := terminalCategory(status); terminal {
				terminalError := &TerminalStatusError{
					ContentID:     contentID,
					AppStatus:     appStatus,
					ContentStatus: status,
					Category:      category,
				}
				return printFinal(dependencies.Printer, format, newResult(
					contentID,
					appStatus,
					selected,
					"terminal",
					category,
					attempts,
					dependencies.Now().Sub(started),
				), terminalError)
			}
		}

		if err := writeProgress(dependencies.Stderr, contentID, appStatus, selected, attempts, targets); err != nil {
			return err
		}
		now := dependencies.Now()
		remaining := deadline.Sub(now)
		if remaining <= 0 {
			return finishTimeout(values, appStatus, last, attempts, started, dependencies, format)
		}
		delay := min(values.Interval, remaining)
		if err := dependencies.Wait(waitContext, delay); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if waitContext.Err() != nil {
				return finishTimeout(values, appStatus, last, attempts, started, dependencies, format)
			}
			return fmt.Errorf("wait for next status poll: %w", err)
		}
		if !dependencies.Now().Before(deadline) {
			return finishTimeout(values, appStatus, last, attempts, started, dependencies, format)
		}
	}
}

func contextOrTimeout(
	parent context.Context,
	values options,
	appStatus string,
	last *apps.App,
	attempts int,
	started time.Time,
	dependencies Dependencies,
	format output.Format,
) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	return finishTimeout(values, appStatus, last, attempts, started, dependencies, format)
}

func finishTimeout(
	values options,
	appStatus string,
	last *apps.App,
	attempts int,
	started time.Time,
	dependencies Dependencies,
	format output.Format,
) error {
	contentStatus := ""
	if last != nil {
		contentStatus = strings.TrimSpace(last.ContentStatus)
	}
	timeoutError := &TimeoutError{
		ContentID:     values.ContentID,
		AppStatus:     appStatus,
		ContentStatus: contentStatus,
		Timeout:       values.Timeout,
		Attempts:      attempts,
	}
	return printFinal(dependencies.Printer, format, newResult(
		values.ContentID,
		appStatus,
		last,
		"timeout",
		"",
		attempts,
		dependencies.Now().Sub(started),
	), timeoutError)
}

func printFinal(printer Printer, format output.Format, result Result, waitError error) error {
	if err := printer.Print(format, result); err != nil {
		if waitError != nil {
			return errors.Join(waitError, err)
		}
		return err
	}
	return waitError
}

func newResult(
	contentID string,
	appStatus string,
	app *apps.App,
	outcome string,
	category TerminalCategory,
	attempts int,
	elapsed time.Duration,
) Result {
	result := Result{
		ContentID:        contentID,
		AppStatus:        appStatus,
		Outcome:          outcome,
		TerminalCategory: category,
		Attempts:         attempts,
		Elapsed:          max(elapsed, 0).Round(time.Millisecond).String(),
	}
	if app != nil {
		result.ContentStatus = strings.TrimSpace(app.ContentStatus)
		result.Title = app.Title
		result.PackageName = app.PackageName
	}
	return result
}

func selectVariant(records []apps.App, contentID string, appStatus string) (*apps.App, error) {
	var selected *apps.App
	for index := range records {
		record := &records[index]
		if record.ContentID != "" && record.ContentID != contentID {
			return nil, fmt.Errorf(
				"Samsung returned content ID %q while waiting for %s",
				record.ContentID,
				contentID,
			)
		}
		if strings.ToUpper(strings.TrimSpace(record.AppStatus)) != appStatus {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf(
				"Samsung returned multiple %s records for content ID %s",
				appStatus,
				contentID,
			)
		}
		selected = record
	}
	return selected, nil
}

func writeProgress(
	stderr io.Writer,
	contentID string,
	appStatus string,
	current *apps.App,
	attempt int,
	targets map[string]struct{},
) error {
	status := "variant not found"
	if current != nil {
		status = strings.TrimSpace(current.ContentStatus)
	}
	if _, err := fmt.Fprintf(
		stderr,
		"Waiting for %s %s: current=%s target=%s attempt=%d\n",
		contentID,
		appStatus,
		status,
		strings.Join(sortedTargets(targets), ","),
		attempt,
	); err != nil {
		return fmt.Errorf("write status progress: %w", err)
	}
	return nil
}

func parseTargets(value string) (map[string]struct{}, error) {
	if strings.TrimSpace(value) == "" {
		return nil, shared.UsageErrorf("--until is required")
	}
	targets := make(map[string]struct{})
	for _, part := range strings.Split(value, ",") {
		status := strings.ToUpper(strings.TrimSpace(part))
		if status == "" {
			return nil, shared.UsageErrorf("--until contains an empty contentStatus")
		}
		if !knownContentStatus(status) {
			return nil, shared.UsageErrorf(
				"unsupported --until contentStatus %q; use a status from Samsung's status mapping",
				part,
			)
		}
		if _, duplicate := targets[status]; duplicate {
			return nil, shared.UsageErrorf("--until contentStatus %q must not be repeated", status)
		}
		targets[status] = struct{}{}
	}
	return targets, nil
}

func sortedTargets(targets map[string]struct{}) []string {
	statuses := make([]string, 0, len(targets))
	for _, status := range contentStatuses {
		if _, exists := targets[status]; exists {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func validateDurations(interval time.Duration, timeout time.Duration) error {
	if interval < minimumInterval || interval > maximumInterval {
		return shared.UsageErrorf(
			"--interval must be between %s and %s",
			minimumInterval,
			maximumInterval,
		)
	}
	if timeout < minimumTimeout || timeout > maximumTimeout {
		return shared.UsageErrorf(
			"--timeout must be between %s and %s",
			minimumTimeout,
			maximumTimeout,
		)
	}
	return nil
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func validateDependencies(dependencies Dependencies) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("status wait output printer is not configured")
	case dependencies.OpenService == nil:
		return errors.New("status wait session factory is not configured")
	case dependencies.Now == nil:
		return errors.New("status wait clock is not configured")
	case dependencies.Wait == nil:
		return errors.New("status wait timer is not configured")
	default:
		return nil
	}
}

func waitWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var contentStatuses = []string{
	"REGISTERING",
	"UPDATING",
	"RE_REGISTERING",
	"READY_FOR_REVIEW",
	"READY_TO_PRE_REVIEWS",
	"UNDER_PRE_REVIEWS",
	"PRE_REVIEWS_SUSPENDED",
	"PRE_REVIEWS_REJECTED",
	"PRE_REVIEWS_DELAYED",
	"PRE_REVIEWS_CANCELED",
	"READY_FOR_CONTENT_REVIEW",
	"UNDER_CONTENT_REVIEW",
	"CONTENT_REVIEW_REJECTED",
	"CONTENT_REVIEW_SUSPENDED",
	"CONTENT_REVIEW_DELAYED",
	"CONTENT_REVIEW_CANCELED",
	"READY_FOR_DEVICE_TEST",
	"UNDER_DEVICE_TEST",
	"DEVICE_TEST_REJECTED",
	"DEVICE_TEST_SUSPENDED",
	"DEVICE_TEST_DELAYED",
	"DEVICE_TEST_CANCELED",
	"READY_FOR_TEST_CONFIRMATION",
	"UNDER_TEST_CONFIRMATION",
	"TEST_CONFIRMATION_REJECTED",
	"TEST_CONFIRMATION_SUSPENDED",
	"TEST_CONFIRMATION_DELAYED",
	"TEST_CONFIRMATION_CANCELED",
	"READY_FOR_SALE",
	"READY_FOR_CHANGE",
	"CANCELED",
	"FOR_SALE",
	"SUSPENDED",
	"TERMINATED",
	"BETA_REGISTERING",
	"READY_FOR_BETA_TESTING",
	"BETA_PRE_REVIEW_REJECTED",
	"BETA_UPDATING",
	"BETA_DEPLOYED",
	"BETA_SUSPENDED",
}

var knownStatuses = func() map[string]struct{} {
	result := make(map[string]struct{}, len(contentStatuses))
	for _, status := range contentStatuses {
		result[status] = struct{}{}
	}
	return result
}()

func knownContentStatus(status string) bool {
	_, exists := knownStatuses[status]
	return exists
}

func terminalCategory(status string) (TerminalCategory, bool) {
	switch status {
	case "PRE_REVIEWS_REJECTED",
		"CONTENT_REVIEW_REJECTED",
		"DEVICE_TEST_REJECTED",
		"TEST_CONFIRMATION_REJECTED",
		"BETA_PRE_REVIEW_REJECTED":
		return TerminalRejected, true
	case "PRE_REVIEWS_CANCELED",
		"CONTENT_REVIEW_CANCELED",
		"DEVICE_TEST_CANCELED",
		"TEST_CONFIRMATION_CANCELED",
		"CANCELED":
		return TerminalCanceled, true
	case "TERMINATED":
		return TerminalTerminated, true
	default:
		return "", false
	}
}

var _ output.RowSource = Result{}
