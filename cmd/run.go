package cmd

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
)

// Run executes gsc and returns the intended process exit code.
func Run(args []string, version string) int {
	return run(args, version, os.Stdout, os.Stderr)
}

func run(args []string, version string, stdout io.Writer, stderr io.Writer) int {
	root := RootCommand(version, stdout, stderr)
	if err := root.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		printError(stderr, err)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := root.Run(ctx); err != nil {
		exitCode := exitCodeForError(err)
		printError(stderr, err)
		return exitCode
	}
	return ExitSuccess
}
