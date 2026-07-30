package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v3/ffcli"
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
	root.Subcommands = []*ffcli.Command{versionCommand}

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
