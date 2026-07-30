package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/peterbourgon/ff/v3/ffcli"
	"golang.org/x/term"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/apicmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/appscmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/authcmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/betacmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/contentcmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/discovery"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/doctorcmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/iapcmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/iapitemscmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/receiptscmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/reviewscmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/rolloutscmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/statscmd"
)

var errUsage = errors.New("invalid command usage")

// RootCommand builds the gsc command tree.
func RootCommand(version string, stdout io.Writer, stderr io.Writer) *ffcli.Command {
	rootFlags := flag.NewFlagSet("gsc", flag.ContinueOnError)
	rootFlags.SetOutput(stderr)

	var versionRequested bool
	rootFlags.BoolVar(&versionRequested, "version", false, "Print version information and exit")

	root := &ffcli.Command{
		Name:       "gsc",
		ShortUsage: "gsc <command> [flags]",
		ShortHelp:  "Ship Android apps to Galaxy Store from the command line.",
		FlagSet:    rootFlags,
	}

	versionFlags := flag.NewFlagSet("version", flag.ContinueOnError)
	versionFlags.SetOutput(stderr)
	versionCommand := &ffcli.Command{
		Name:       "version",
		ShortUsage: "gsc version",
		ShortHelp:  "Print version information.",
		FlagSet:    versionFlags,
		Exec: func(context.Context, []string) error {
			_, err := fmt.Fprintln(stdout, version)
			return err
		},
	}
	authCommand := unavailableCommand("auth", "Manage Galaxy Store service-account authentication.")
	if dependencies, err := authcmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		authCommand = authcmd.NewCommand(dependencies)
	} else {
		authCommand.Exec = func(context.Context, []string) error {
			return fmt.Errorf("initialize authentication: %w", err)
		}
	}
	appsCommand := unavailableCommand("apps", "View apps registered in Galaxy Store Seller Portal.")
	if dependencies, err := appscmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		appsCommand = appscmd.NewCommand(dependencies)
	} else {
		appsCommand.Exec = func(context.Context, []string) error {
			return fmt.Errorf("initialize app commands: %w", err)
		}
	}
	binariesCommand := unavailableCommand(
		"binaries",
		"Manage app binaries through Samsung's current v2 API.",
	)
	uploadsCommand := unavailableCommand("uploads", "Create upload sessions and upload app binaries.")
	if dependencies, err := contentcmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		appsCommand.Subcommands = append(
			appsCommand.Subcommands,
			contentcmd.NewAppsSubcommands(dependencies)...,
		)
		appsCommand.ShortHelp = "View, update, and submit apps registered in Galaxy Store Seller Portal."
		appsCommand.Exec = func(context.Context, []string) error {
			return shared.UsageErrorf(
				"apps requires a command: list, view, update, submit, or status",
			)
		}
		binariesCommand = contentcmd.NewBinariesCommand(dependencies)
		uploadsCommand = contentcmd.NewUploadsCommand(dependencies)
	} else {
		binariesCommand.Exec = initializationError("binary commands", err)
		uploadsCommand.Exec = initializationError("upload commands", err)
	}

	betaCommand := unavailableCommand("beta", "Manage Galaxy Store closed beta testers.")
	if dependencies, err := betacmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		betaCommand = betacmd.NewCommand(dependencies)
	} else {
		betaCommand.Exec = initializationError("beta commands", err)
	}

	rolloutsCommand := unavailableCommand(
		"rollouts",
		"Manage Galaxy Store staged rollout rates and binaries.",
	)
	if dependencies, err := rolloutscmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		rolloutsCommand = rolloutscmd.NewCommand(dependencies)
	} else {
		rolloutsCommand.Exec = initializationError("rollout commands", err)
	}

	reviewsCommand := unavailableCommand(
		"reviews",
		"List Galaxy Store buyer comments and manage seller replies.",
	)
	if dependencies, err := reviewscmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		reviewsCommand = reviewscmd.NewCommand(dependencies)
	} else {
		reviewsCommand.Exec = initializationError("review commands", err)
	}

	iapCommand := unavailableCommand(
		"iap",
		"Manage Galaxy Store in-app products, transactions, subscriptions, and receipts.",
	)
	if dependencies, err := iapcmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		iapCommand = iapcmd.NewCommand(dependencies)
		if itemDependencies, itemErr := iapitemscmd.DefaultDependencies(
			stdout,
			stderr,
			isTerminal,
		); itemErr == nil {
			iapCommand.Subcommands = append(
				[]*ffcli.Command{iapitemscmd.NewCommand(itemDependencies)},
				iapCommand.Subcommands...,
			)
		} else {
			itemsCommand := unavailableCommand("items", "Manage one-time Samsung IAP items.")
			itemsCommand.Exec = initializationError("IAP item commands", itemErr)
			iapCommand.Subcommands = append([]*ffcli.Command{itemsCommand}, iapCommand.Subcommands...)
		}
		iapCommand.Subcommands = append(
			iapCommand.Subcommands,
			receiptscmd.NewCommand(receiptscmd.DefaultDependencies(stdout, stderr, isTerminal)),
		)
		iapCommand.ShortHelp = "Manage Galaxy Store in-app products, transactions, subscriptions, and receipts."
		iapCommand.Exec = func(context.Context, []string) error {
			return shared.UsageErrorf(
				"iap requires a command: items, purchases, subscriptions, orders, or receipts",
			)
		}
	} else {
		iapCommand.Exec = initializationError("IAP commands", err)
	}

	statsCommand := unavailableCommand("stats", "Query Galaxy Store seller and app statistics.")
	if dependencies, err := statscmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		statsCommand = statscmd.NewCommand(dependencies)
	} else {
		statsCommand.Exec = initializationError("statistics commands", err)
	}

	apiCommand := unavailableCommand(
		"api",
		"Call a documented Galaxy Store Developer API path safely.",
	)
	if dependencies, err := apicmd.DefaultDependencies(stdout, stderr, isTerminal); err == nil {
		apiCommand = apicmd.NewCommand(dependencies)
	} else {
		apiCommand.Exec = initializationError("raw API commands", err)
	}

	doctorCommand := unavailableCommand(
		"doctor",
		"Diagnose local configuration and existing Galaxy Store credentials.",
	)
	if dependencies, err := doctorcmd.DefaultDependencies(
		stdout,
		stderr,
		isTerminal,
		version,
	); err == nil {
		doctorCommand = doctorcmd.NewCommand(dependencies)
	} else {
		doctorCommand.Exec = initializationError("diagnostic command", err)
	}

	root.Subcommands = []*ffcli.Command{
		authCommand,
		appsCommand,
		binariesCommand,
		uploadsCommand,
		betaCommand,
		rolloutsCommand,
		reviewsCommand,
		iapCommand,
		statsCommand,
		apiCommand,
		doctorCommand,
		discovery.CapabilitiesCommand(stdout, isTerminal),
		discovery.SchemaCommand(stdout, isTerminal),
		discovery.SearchCommand(stdout, isTerminal),
		versionCommand,
	}

	root.Exec = func(_ context.Context, args []string) error {
		if versionRequested {
			_, err := fmt.Fprintln(stdout, version)
			return err
		}
		if len(args) > 0 {
			return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
		}
		_, err := fmt.Fprint(stdout, rootUsage(root))
		return err
	}
	root.UsageFunc = rootUsage
	versionCommand.UsageFunc = commandUsage

	return root
}

func initializationError(component string, err error) func(context.Context, []string) error {
	return func(context.Context, []string) error {
		return fmt.Errorf("initialize %s: %w", component, err)
	}
}

func unavailableCommand(name, help string) *ffcli.Command {
	return &ffcli.Command{
		Name:       name,
		ShortUsage: "gsc " + name,
		ShortHelp:  help,
	}
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func rootUsage(command *ffcli.Command) string {
	usage := fmt.Sprintf("Usage:\n  %s\n\n%s\n\nCommands:\n", command.ShortUsage, command.ShortHelp)
	for _, subcommand := range command.Subcommands {
		usage += fmt.Sprintf("  %-14s %s\n", subcommand.Name, subcommand.ShortHelp)
	}
	return usage + "\nRun \"gsc <command> --help\" for command help.\n"
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}
