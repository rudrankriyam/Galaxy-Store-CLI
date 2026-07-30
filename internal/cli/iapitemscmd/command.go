// Package iapitemscmd implements gsc iap items commands.
package iapitemscmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/items"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// Service is the Samsung IAP item API used by commands.
type Service interface {
	List(context.Context, string, items.ListOptions) (*items.ListResult, error)
	View(context.Context, string, string) (*items.Item, error)
	Create(context.Context, string, items.FullRequest) (*items.Item, error)
	Replace(context.Context, string, items.FullRequest) (*items.Item, error)
	Update(context.Context, string, items.UpdateRequest) (*items.Item, error)
	Delete(context.Context, string, string) (*items.Item, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps item commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	ReadFile    func(string) ([]byte, error)
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without opening a
// session until all local input has been validated.
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
			service, serviceErr := items.New(active.Client)
			if serviceErr != nil {
				return nil, serviceErr
			}
			return service, nil
		},
	}, nil
}

// NewCommand creates the gsc iap items command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	command := &ffcli.Command{
		Name:       "items",
		ShortUsage: "gsc iap items <command> [flags]",
		ShortHelp:  "Manage one-time Samsung IAP items.",
		Subcommands: []*ffcli.Command{
			newListCommand(dependencies, stderr),
			newViewCommand(dependencies, stderr),
			newFileMutationCommand("create", dependencies, stderr),
			newFileMutationCommand("replace", dependencies, stderr),
			newFileMutationCommand("update", dependencies, stderr),
			newDeleteCommand(dependencies, stderr),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf(
				"iap items requires a command: list, view, create, replace, update, or delete",
			)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newListCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap items list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options listOptions
	flags.StringVar(&options.PackageName, "package-name", "", "Required Android package name")
	flags.IntVar(&options.Page, "page", 1, "One-based Samsung result page")
	flags.IntVar(&options.Size, "size", 20, "Number of items to return")
	addCommonFlags(flags, &options.Profile, &options.Output)

	command := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc iap items list --package-name NAME [--page N] [--size N] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "List one page of one-time IAP items.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap items list does not accept positional arguments")
			}
			return runList(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newViewCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap items view", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options itemOptions
	flags.StringVar(&options.PackageName, "package-name", "", "Required Android package name")
	flags.StringVar(&options.ItemID, "item-id", "", "Required IAP item ID")
	addCommonFlags(flags, &options.Profile, &options.Output)

	command := &ffcli.Command{
		Name:       "view",
		ShortUsage: "gsc iap items view --package-name NAME --item-id ID [--profile NAME] [--output FORMAT]",
		ShortHelp:  "View one one-time IAP item.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap items view does not accept positional arguments")
			}
			return runView(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newFileMutationCommand(
	action string,
	dependencies Dependencies,
	stderr io.Writer,
) *ffcli.Command {
	flags := flag.NewFlagSet("iap items "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options fileMutationOptions
	flags.StringVar(&options.PackageName, "package-name", "", "Required Android package name")
	flags.StringVar(&options.File, "file", "", "Required JSON request file")
	addCommonFlags(flags, &options.Profile, &options.Output)
	addMutationFlags(flags, &options.Mode)

	command := &ffcli.Command{
		Name: action,
		ShortUsage: fmt.Sprintf(
			"gsc iap items %s --package-name NAME --file PATH [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
			action,
		),
		ShortHelp: fileMutationHelp(action),
		FlagSet:   flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap items %s does not accept positional arguments", action)
			}
			return runFileMutation(ctx, action, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newDeleteCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap items delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options deleteOptions
	flags.StringVar(&options.PackageName, "package-name", "", "Required Android package name")
	flags.StringVar(&options.ItemID, "item-id", "", "Required IAP item ID")
	addCommonFlags(flags, &options.Profile, &options.Output)
	addMutationFlags(flags, &options.Mode)

	command := &ffcli.Command{
		Name:       "delete",
		ShortUsage: "gsc iap items delete --package-name NAME --item-id ID [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Remove one IAP item immediately.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap items delete does not accept positional arguments")
			}
			return runDelete(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func addCommonFlags(flags *flag.FlagSet, profile *string, outputValue *string) {
	flags.StringVar(profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
}

func addMutationFlags(flags *flag.FlagSet, mode *shared.MutationMode) {
	flags.BoolVar(&mode.Confirm, "confirm", false, "Confirm the immediate Galaxy Store mutation")
	flags.BoolVar(&mode.DryRun, "dry-run", false, "Validate input and print a plan without changing Galaxy Store")
}

func fileMutationHelp(action string) string {
	switch action {
	case "create":
		return "Create one IAP item from a complete JSON request."
	case "replace":
		return "Replace all mutable information for one IAP item."
	case "update":
		return "Update an item's title and/or local territory prices."
	default:
		return "Change one IAP item from a JSON request."
	}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

func validateDependencies(dependencies Dependencies, needFile bool, needService bool) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("iap items command output printer is not configured")
	case needFile && dependencies.ReadFile == nil:
		return errors.New("iap items command file reader is not configured")
	case needService && dependencies.OpenService == nil:
		return errors.New("iap items command session factory is not configured")
	default:
		return nil
	}
}
