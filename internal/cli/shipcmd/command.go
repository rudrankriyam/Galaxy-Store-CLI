// Package shipcmd implements the typed, resumable Galaxy Store shipping
// command. Shipping stops at review submission and never changes distribution
// status to FOR_SALE.
package shipcmd

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
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Lock owns the exclusive sibling lock for one checkpoint.
type Lock interface {
	Release() error
}

// Dependencies keeps authentication, durable state, locking, and output
// independently testable. OpenRemote must not resolve credentials until it is
// called.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenRemote  func(profile string) (ship.Remote, error)
	NewStore    func(path string) (ship.CheckpointStore, error)
	AcquireLock func(checkpointPath string) (Lock, error)
}

type planOptions struct {
	ContentID                   string
	BinaryPath                  string
	MetadataDirectory           string
	GMS                         string
	CopyDeviceConfigurationFrom string
	Output                      string
}

type runOptions struct {
	Plan           planOptions
	CheckpointPath string
	Profile        string
	DryRun         bool
	Confirm        bool
}

// NewCommand creates the top-level gsc ship command.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	command := &ffcli.Command{
		Name:       "ship",
		ShortUsage: "gsc ship <plan|run> [flags]",
		ShortHelp:  "Plan and run a typed, resumable Galaxy Store review submission.",
		LongHelp: `Plan and run the fixed Galaxy Store shipping pipeline.

The pipeline validates one exact REGISTRATION target, uploads and registers one
binary through the current v2 API, applies a drift-checked metadata bundle,
verifies the readback, and submits the app for review. It never changes
distribution status to FOR_SALE.`,
		Subcommands: []*ffcli.Command{
			newPlanCommand(dependencies, stderr),
			newRunCommand(dependencies, stderr),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("ship requires a command: plan or run")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newPlanCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("ship plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := planOptions{}
	bindPlanFlags(flags, &options)
	command := &ffcli.Command{
		Name:       "plan",
		ShortUsage: "gsc ship plan --content-id ID --binary PATH --metadata-dir PATH --gms Y|N [flags]",
		ShortHelp:  "Build and print a deterministic shipping plan entirely offline.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("ship plan does not accept positional arguments")
			}
			return runPlan(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRunCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("ship run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := runOptions{}
	bindPlanFlags(flags, &options.Plan)
	flags.StringVar(
		&options.CheckpointPath,
		"checkpoint",
		"",
		"Private checkpoint path (default .gsc/ship-CONTENT_ID.json)",
	)
	flags.StringVar(
		&options.Profile,
		"profile",
		"",
		"Credential profile (defaults to environment or configured profile)",
	)
	flags.BoolVar(
		&options.DryRun,
		"dry-run",
		false,
		"Print the offline plan without opening credentials or changing local or remote state",
	)
	flags.BoolVar(
		&options.Confirm,
		"confirm",
		false,
		"Confirm upload, binary registration, metadata apply, and review submission",
	)
	command := &ffcli.Command{
		Name:       "run",
		ShortUsage: "gsc ship run --content-id ID --binary PATH --metadata-dir PATH --gms Y|N [--dry-run | --confirm] [flags]",
		ShortHelp:  "Run or resume the typed pipeline through review submission.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("ship run does not accept positional arguments")
			}
			return runShip(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func bindPlanFlags(flags *flag.FlagSet, options *planOptions) {
	options.MetadataDirectory = "metadata"
	options.Output = "auto"
	flags.StringVar(
		&options.ContentID,
		"content-id",
		"",
		"Required 12-digit Galaxy Store content ID",
	)
	flags.StringVar(
		&options.BinaryPath,
		"binary",
		"",
		"Required APK or AAB path",
	)
	flags.StringVar(
		&options.MetadataDirectory,
		"metadata-dir",
		options.MetadataDirectory,
		"Complete metadata bundle directory",
	)
	flags.StringVar(
		&options.GMS,
		"gms",
		"",
		"Required Google Mobile Services declaration: Y or N",
	)
	flags.StringVar(
		&options.CopyDeviceConfigurationFrom,
		"copy-device-config-from",
		"",
		"Existing binary sequence whose device configuration should be copied",
	)
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

func requirePrinter(dependencies Dependencies) error {
	if dependencies.Printer == nil {
		return errors.New("shipping output printer is not configured")
	}
	return nil
}
