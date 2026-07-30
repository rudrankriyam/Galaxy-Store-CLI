// Package betacmd implements closed-beta tester commands.
package betacmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/beta"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// Service is the Samsung closed-beta API used by commands.
type Service interface {
	Get(context.Context, beta.ListOptions) (*beta.Test, error)
	Update(context.Context, beta.UpdateInput) (*beta.UpdateResult, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps closed-beta commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	ReadFile    func(string) ([]byte, error)
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until local arguments and mutation input have been validated.
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
		Stderr:   stderr,
		Printer:  output.NewPrinter(stdout, isTerminal),
		ReadFile: os.ReadFile,
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			return beta.New(active.Client)
		},
	}, nil
}

// NewCommand creates the gsc beta command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	testers := newTestersCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "beta",
		ShortUsage:  "gsc beta <command> [flags]",
		ShortHelp:   "Manage Galaxy Store closed beta testers.",
		Subcommands: []*ffcli.Command{testers},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("beta requires a command: testers")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newTestersCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	list := newListCommand(dependencies, stderr)
	update := newUpdateCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "testers",
		ShortUsage:  "gsc beta testers <command> [flags]",
		ShortHelp:   "List or update Samsung accounts in a closed beta.",
		Subcommands: []*ffcli.Command{list, update},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("beta testers requires a command: list or update")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newListCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("beta testers list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var profile, contentID, appStatus, outputValue string
	var offset, limit int
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&appStatus, "app-status", "", "Required app variant: SALE or REGISTRATION")
	flags.IntVar(&offset, "offset", 0, "Zero-based beta tester offset")
	flags.IntVar(&limit, "limit", 1000, "Maximum beta testers to return (1-1000)")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc beta testers list --content-id ID --app-status STATUS [flags]",
		ShortHelp:  "List one page of closed beta testers.",
		LongHelp: `List one page of closed beta testers.

--app-status is always explicit because SALE and REGISTRATION records can exist
simultaneously for the same content ID.

Example:
  gsc beta testers list --content-id 000007654321 --app-status REGISTRATION --output table`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("beta testers list does not accept positional arguments")
			}
			return runList(ctx, dependencies, listOptions{
				Profile: profile, ContentID: contentID, AppStatus: appStatus,
				Offset: offset, Limit: limit, Output: outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newUpdateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("beta testers update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var profile, contentID, file, outputValue string
	var dryRun, confirm bool
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&file, "file", "", "Required JSON update file")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and print the mutation plan without opening a session")
	flags.BoolVar(&confirm, "confirm", false, "Confirm the remote tester update")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc beta testers update --content-id ID --file update.json [--dry-run | --confirm] [flags]",
		ShortHelp:  "Add or remove closed beta testers from a JSON update file.",
		LongHelp: `Add or remove closed beta testers from a JSON update file.

The file accepts betaTestersToBeAdded, betaTestersToBeDeleted, and an optional
feedbackChannel. Samsung accepts at most 1,000 additions and 1,000 deletions per
request and can report individual invalid Samsung account IDs.

Examples:
  gsc beta testers update --content-id 000007654321 --file testers.json --dry-run
  gsc beta testers update --content-id 000007654321 --file testers.json --confirm`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("beta testers update does not accept positional arguments")
			}
			return runUpdate(ctx, dependencies, updateOptions{
				Profile: profile, ContentID: contentID, File: file,
				Output: outputValue,
				Mode:   shared.MutationMode{DryRun: dryRun, Confirm: confirm},
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type listOptions struct {
	Profile   string
	ContentID string
	AppStatus string
	Offset    int
	Limit     int
	Output    string
}

type updateOptions struct {
	Profile   string
	ContentID string
	File      string
	Output    string
	Mode      shared.MutationMode
}

type updateFile struct {
	AddTesters      []string `json:"betaTestersToBeAdded"`
	DeleteTesters   []string `json:"betaTestersToBeDeleted"`
	FeedbackChannel *string  `json:"feedbackChannel"`
}

func runList(ctx context.Context, dependencies Dependencies, options listOptions) error {
	contentID, status, format, err := validateList(options)
	if err != nil {
		return err
	}
	if err := validateReadDependencies(dependencies); err != nil {
		return err
	}
	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store session: beta service is nil")
	}
	result, err := service.Get(ctx, beta.ListOptions{
		ContentID: contentID, AppStatus: status, Offset: options.Offset, Limit: options.Limit,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid closed beta response")
	}
	return dependencies.Printer.Print(format, listResult{Test: result})
}

func runUpdate(ctx context.Context, dependencies Dependencies, options updateOptions) error {
	contentID, format, input, err := validateUpdate(dependencies, options)
	if err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation("update closed beta testers"); err != nil {
		return err
	}
	plan := updatePlan(contentID, input)
	if options.Mode.DryRun {
		if dependencies.Printer == nil {
			return errors.New("beta command output printer is not configured")
		}
		return dependencies.Printer.Print(format, plan)
	}
	if err := validateReadDependencies(dependencies); err != nil {
		return err
	}
	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store session: beta service is nil")
	}
	result, updateErr := service.Update(ctx, beta.UpdateInput{
		ContentID: contentID, AddTesters: input.AddTesters,
		DeleteTesters: input.DeleteTesters, FeedbackChannel: input.FeedbackChannel,
	})
	if result != nil {
		if printErr := dependencies.Printer.Print(format, updateResult{UpdateResult: result}); printErr != nil {
			return printErr
		}
	}
	if updateErr != nil {
		return updateErr
	}
	if result == nil {
		return errors.New("samsung returned an invalid closed beta update response")
	}
	return nil
}

func validateList(options listOptions) (string, string, output.Format, error) {
	if err := shared.RequireValue("--content-id", options.ContentID); err != nil {
		return "", "", "", err
	}
	if err := shared.ValidateContentID(options.ContentID); err != nil {
		return "", "", "", err
	}
	status, err := shared.NormalizeAppStatus(options.AppStatus)
	if err != nil {
		return "", "", "", err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return "", "", "", err
	}
	if options.Offset < 0 {
		return "", "", "", shared.UsageErrorf("--offset must be zero or greater")
	}
	if err := shared.ValidateLimit(options.Limit, 1000); err != nil {
		return "", "", "", err
	}
	return options.ContentID, status, format, nil
}

func validateUpdate(
	dependencies Dependencies,
	options updateOptions,
) (string, output.Format, updateFile, error) {
	if err := shared.RequireValue("--content-id", options.ContentID); err != nil {
		return "", "", updateFile{}, err
	}
	if err := shared.ValidateContentID(options.ContentID); err != nil {
		return "", "", updateFile{}, err
	}
	if err := shared.RequireValue("--file", options.File); err != nil {
		return "", "", updateFile{}, err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return "", "", updateFile{}, err
	}
	if dependencies.ReadFile == nil {
		return "", "", updateFile{}, errors.New("beta command file reader is not configured")
	}
	data, err := dependencies.ReadFile(options.File)
	if err != nil {
		return "", "", updateFile{}, shared.UsageErrorf("read --file: %v", err)
	}
	var input updateFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", "", updateFile{}, shared.UsageErrorf("decode --file: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", "", updateFile{}, shared.UsageErrorf("decode --file: multiple JSON values are not allowed")
		}
		return "", "", updateFile{}, shared.UsageErrorf("decode --file: %v", err)
	}
	if err := validateUpdateFile(input); err != nil {
		return "", "", updateFile{}, err
	}
	return options.ContentID, format, input, nil
}

func validateUpdateFile(input updateFile) error {
	if len(input.AddTesters) == 0 && len(input.DeleteTesters) == 0 && input.FeedbackChannel == nil {
		return shared.UsageErrorf("update file must add testers, delete testers, or set feedbackChannel")
	}
	for _, entry := range []struct {
		label   string
		testers []string
	}{
		{label: "betaTestersToBeAdded", testers: input.AddTesters},
		{label: "betaTestersToBeDeleted", testers: input.DeleteTesters},
	} {
		label, testers := entry.label, entry.testers
		if len(testers) > 1000 {
			return shared.UsageErrorf("%s cannot exceed 1000 accounts", label)
		}
		for _, tester := range testers {
			if tester == "" || tester != strings.TrimSpace(tester) {
				return shared.UsageErrorf("%s must not contain blank or padded account IDs", label)
			}
		}
	}
	return nil
}

func updatePlan(contentID string, input updateFile) shared.Plan {
	details := fmt.Sprintf(
		"add=%d delete=%d feedbackChannel=%t",
		len(input.AddTesters),
		len(input.DeleteTesters),
		input.FeedbackChannel != nil,
	)
	return shared.Plan{
		Operations: []shared.Operation{{
			Action: "update", Resource: "closed-beta-testers/" + contentID, Details: details,
		}},
		Warnings: []string{
			"Samsung may reject individual account IDs while accepting the overall update.",
			"Deletion wins when the same account appears in both add and delete lists.",
		},
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}
}

type listResult struct {
	*beta.Test
}

func (result listResult) OutputHeaders() []string {
	return []string{"TESTER", "FEEDBACK CHANNEL", "TOTAL"}
}

func (result listResult) OutputRows() [][]string {
	if result.Test == nil {
		return nil
	}
	rows := make([][]string, len(result.BetaTesters))
	for index, tester := range result.BetaTesters {
		rows[index] = []string{
			tester, result.FeedbackChannel, strconv.Itoa(result.TotalNumberOfBetaTesters),
		}
	}
	return rows
}

type updateResult struct {
	*beta.UpdateResult
}

func (result updateResult) OutputHeaders() []string {
	return []string{"OPERATION", "FAILED TESTER"}
}

func (result updateResult) OutputRows() [][]string {
	if result.UpdateResult == nil {
		return nil
	}
	rows := make([][]string, 0, len(result.AdditionFailedTesters)+len(result.DeletionFailedTesters))
	for _, tester := range result.AdditionFailedTesters {
		rows = append(rows, []string{"add", tester})
	}
	for _, tester := range result.DeletionFailedTesters {
		rows = append(rows, []string{"delete", tester})
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

func validateReadDependencies(dependencies Dependencies) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("beta command output printer is not configured")
	case dependencies.OpenService == nil:
		return errors.New("beta command session factory is not configured")
	default:
		return nil
	}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var (
	_ output.RowSource = listResult{}
	_ output.RowSource = updateResult{}
)
