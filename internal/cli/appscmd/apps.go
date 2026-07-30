// Package appscmd implements read-only Galaxy Store app commands.
package appscmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// Service is the read-only Samsung app API used by commands.
type Service interface {
	List(context.Context, apps.ListOptions) (*apps.ListResult, error)
	View(context.Context, string) ([]apps.App, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps app commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until a command has validated all local arguments.
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
			service, serviceErr := apps.New(active.Client)
			if serviceErr != nil {
				return nil, serviceErr
			}
			return service, nil
		},
	}, nil
}

// NewCommand creates the gsc apps command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	list := newListCommand(dependencies, stderr)
	view := newViewCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "apps",
		ShortUsage:  "gsc apps <command> [flags]",
		ShortHelp:   "View apps registered in Galaxy Store Seller Portal.",
		Subcommands: []*ffcli.Command{list, view},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("apps requires a command: list or view")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newListCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("apps list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var offset int
	var limit int
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.IntVar(&offset, "offset", 0, "Zero-based local result offset")
	flags.IntVar(&limit, "limit", 0, "Maximum results to return; zero returns all remaining apps")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc apps list [--profile NAME] [--offset N] [--limit N] [--output FORMAT]",
		ShortHelp:  "List apps registered by the resolved Galaxy Store seller.",
		LongHelp: `List apps registered by the resolved Galaxy Store seller.

Samsung returns the complete contentList array without server pagination.
--offset and --limit select a local page without inventing API query parameters.

Examples:
  gsc apps list
  gsc apps list --profile production --output table
  gsc apps list --offset 50 --limit 50 --output json`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps list does not accept positional arguments")
			}
			return runList(ctx, dependencies, listOptions{
				Profile: profile,
				Offset:  offset,
				Limit:   limit,
				Output:  outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newViewCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("apps view", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var contentID string
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "view",
		ShortUsage: "gsc apps view --content-id ID [--profile NAME] [--output FORMAT]",
		ShortHelp:  "View every SALE and REGISTRATION record for one app.",
		LongHelp: `View every SALE and REGISTRATION record for one app.

Samsung can return a published SALE record and an in-progress REGISTRATION
record for the same content ID. This command preserves both and retains unknown
response fields in JSON output.

Examples:
  gsc apps view --content-id 000007654321
  gsc apps view --content-id 000007654321 --output markdown`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps view does not accept positional arguments")
			}
			return runView(ctx, dependencies, viewOptions{
				Profile:   profile,
				ContentID: contentID,
				Output:    outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type listOptions struct {
	Profile string
	Offset  int
	Limit   int
	Output  string
}

type viewOptions struct {
	Profile   string
	ContentID string
	Output    string
}

func runList(ctx context.Context, dependencies Dependencies, options listOptions) error {
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if options.Offset < 0 {
		return shared.UsageErrorf("--offset must be zero or greater")
	}
	if options.Limit < 0 {
		return shared.UsageErrorf("--limit must be zero or greater")
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}

	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store session: app service is nil")
	}
	result, err := service.List(ctx, apps.ListOptions{
		Offset: options.Offset,
		Limit:  options.Limit,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid app list response")
	}
	return dependencies.Printer.Print(format, listResult{ListResult: result})
}

func runView(ctx context.Context, dependencies Dependencies, options viewOptions) error {
	contentID := options.ContentID
	if err := shared.RequireValue("--content-id", contentID); err != nil {
		return err
	}
	if err := shared.ValidateContentID(contentID); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}

	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store session: app service is nil")
	}
	records, err := service.View(ctx, contentID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return errors.New("samsung returned an invalid empty app response")
	}
	return dependencies.Printer.Print(format, viewResult(records))
}

type listResult struct {
	*apps.ListResult
}

func (result listResult) OutputHeaders() []string {
	return appHeaders()
}

func (result listResult) OutputRows() [][]string {
	if result.ListResult == nil {
		return nil
	}
	return appRows(result.Apps)
}

type viewResult []apps.App

func (result viewResult) OutputHeaders() []string {
	return appHeaders()
}

func (result viewResult) OutputRows() [][]string {
	return appRows(result)
}

func appHeaders() []string {
	return []string{"CONTENT ID", "TITLE", "PACKAGE", "APP STATUS", "CONTENT STATUS"}
}

func appRows(records []apps.App) [][]string {
	rows := make([][]string, len(records))
	for index, app := range records {
		rows[index] = []string{
			app.ContentID,
			app.Title,
			app.PackageName,
			app.AppStatus,
			app.ContentStatus,
		}
	}
	return rows
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
		return errors.New("apps command output printer is not configured")
	case dependencies.OpenService == nil:
		return errors.New("apps command session factory is not configured")
	default:
		return nil
	}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var (
	_ output.RowSource = listResult{}
	_ output.RowSource = viewResult{}
)
