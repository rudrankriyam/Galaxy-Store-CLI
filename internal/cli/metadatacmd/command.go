// Package metadatacmd implements the safe Galaxy Store metadata workflow.
package metadatacmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// Service is the exact read/write API surface required by metadata commands.
type Service interface {
	View(context.Context, string) ([]apps.App, error)
	Update(context.Context, string, json.RawMessage) (*samsungcontent.Result, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps local validation, session creation, and filesystem writes
// independently testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenService func(profile string) (Service, error)
	Now         func() time.Time
	ReadBundle  func(string) (*metadata.Bundle, error)
	WriteBundle func(string, metadata.Bundle, metadata.WriteOptions) error
}

type liveService struct {
	apps    *apps.Service
	content *samsungcontent.Service
}

func (service liveService) View(
	ctx context.Context,
	contentID string,
) ([]apps.App, error) {
	return service.apps.View(ctx, contentID)
}

func (service liveService) Update(
	ctx context.Context,
	contentID string,
	payload json.RawMessage,
) (*samsungcontent.Result, error) {
	return service.content.Update(ctx, contentID, payload)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until a network command has validated all local inputs.
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
		Stderr:      stderr,
		Printer:     output.NewPrinter(stdout, isTerminal),
		Now:         time.Now,
		ReadBundle:  metadata.ReadBundle,
		WriteBundle: metadata.WriteBundle,
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			appService, appErr := apps.New(active.Client)
			if appErr != nil {
				return nil, appErr
			}
			contentService, contentErr := samsungcontent.New(active.Client)
			if contentErr != nil {
				return nil, contentErr
			}
			return liveService{apps: appService, content: contentService}, nil
		},
	}, nil
}

type networkOptions struct {
	Profile   string
	ContentID string
	AppStatus string
	Directory string
	Output    string
}

type pullOptions struct {
	Network networkOptions
	Force   bool
}

type validateOptions struct {
	Directory string
	Output    string
}

type applyOptions struct {
	Network networkOptions
	Mode    shared.MutationMode
}

// NewCommand creates the top-level gsc metadata command.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	command := &ffcli.Command{
		Name:       "metadata",
		ShortUsage: "gsc metadata <command> [flags]",
		ShortHelp:  "Pull, validate, diff, and safely apply Galaxy Store metadata.",
		Subcommands: []*ffcli.Command{
			newPullCommand(dependencies, stderr),
			newValidateCommand(dependencies, stderr),
			newDiffCommand(dependencies, stderr),
			newApplyCommand(dependencies, stderr),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf(
				"metadata requires a command: pull, validate, diff, or apply",
			)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newPullCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("metadata pull", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options pullOptions
	bindNetworkFlags(flags, &options.Network)
	flags.BoolVar(
		&options.Force,
		"force",
		false,
		"Replace an existing complete three-file metadata bundle",
	)
	command := &ffcli.Command{
		Name:       "pull",
		ShortUsage: "gsc metadata pull --content-id ID --app-status SALE|REGISTRATION [flags]",
		ShortHelp:  "Pull one exact contentInfo record into a safe three-file bundle.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("metadata pull does not accept positional arguments")
			}
			return runPull(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newValidateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("metadata validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := validateOptions{Directory: "metadata", Output: "auto"}
	flags.StringVar(&options.Directory, "dir", options.Directory, "Metadata bundle directory")
	flags.StringVar(
		&options.Output,
		"output",
		options.Output,
		"Output format: auto, json, table, or markdown",
	)
	command := &ffcli.Command{
		Name:       "validate",
		ShortUsage: "gsc metadata validate [--dir PATH] [--output FORMAT]",
		ShortHelp:  "Validate a metadata bundle entirely offline.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("metadata validate does not accept positional arguments")
			}
			return runValidate(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newDiffCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("metadata diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options networkOptions
	bindNetworkFlags(flags, &options)
	command := &ffcli.Command{
		Name:       "diff",
		ShortUsage: "gsc metadata diff --content-id ID --app-status SALE|REGISTRATION [flags]",
		ShortHelp:  "Compare desired metadata with one exact live contentInfo record.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("metadata diff does not accept positional arguments")
			}
			return runDiff(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newApplyCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("metadata apply", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options applyOptions
	bindNetworkFlags(flags, &options.Network)
	flags.BoolVar(
		&options.Mode.DryRun,
		"dry-run",
		false,
		"Fetch live metadata and print the semantic plan without updating it",
	)
	flags.BoolVar(
		&options.Mode.Confirm,
		"confirm",
		false,
		"Confirm the planned contentUpdate mutation",
	)
	command := &ffcli.Command{
		Name:       "apply",
		ShortUsage: "gsc metadata apply --content-id ID --app-status SALE|REGISTRATION [--dry-run | --confirm] [flags]",
		ShortHelp:  "Apply a drift-checked metadata bundle and verify the readback.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("metadata apply does not accept positional arguments")
			}
			return runApply(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func bindNetworkFlags(flags *flag.FlagSet, options *networkOptions) {
	options.Directory = "metadata"
	options.Output = "auto"
	flags.StringVar(&options.Profile, "profile", "", "Credential profile")
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(
		&options.AppStatus,
		"app-status",
		"",
		"Required exact variant: SALE or REGISTRATION",
	)
	flags.StringVar(&options.Directory, "dir", options.Directory, "Metadata bundle directory")
	flags.StringVar(
		&options.Output,
		"output",
		options.Output,
		"Output format: auto, json, table, or markdown",
	)
}

func commandUsage(command *ffcli.Command) string {
	var builder strings.Builder
	if command.ShortUsage != "" {
		fmt.Fprintf(&builder, "USAGE\n  %s\n", command.ShortUsage)
	}
	if command.ShortHelp != "" {
		fmt.Fprintf(&builder, "\n%s\n", command.ShortHelp)
	}
	if command.FlagSet != nil {
		fmt.Fprintln(&builder, "\nFLAGS")
		previous := command.FlagSet.Output()
		command.FlagSet.SetOutput(&builder)
		command.FlagSet.PrintDefaults()
		command.FlagSet.SetOutput(previous)
	}
	return builder.String()
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func validateIdentity(contentID string, appStatus string) (metadata.AppStatus, error) {
	if err := shared.RequireValue("--content-id", contentID); err != nil {
		return "", err
	}
	if err := shared.ValidateContentID(contentID); err != nil {
		return "", err
	}
	if err := shared.RequireValue("--app-status", appStatus); err != nil {
		return "", err
	}
	status, err := shared.NormalizeAppStatus(appStatus)
	if err != nil {
		return "", err
	}
	return metadata.AppStatus(status), nil
}

func validateBundleIdentity(
	bundle *metadata.Bundle,
	contentID string,
	appStatus metadata.AppStatus,
) error {
	if bundle == nil {
		return errors.New("metadata bundle is nil")
	}
	if bundle.Manifest.ContentID != contentID {
		return shared.UsageErrorf(
			"--content-id does not match metadata manifest contentId",
		)
	}
	if bundle.Manifest.AppStatus != appStatus {
		return shared.UsageErrorf(
			"--app-status does not match metadata manifest appStatus",
		)
	}
	return nil
}
