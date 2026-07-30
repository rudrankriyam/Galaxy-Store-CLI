// Command generate-command-docs writes or checks the committed command
// reference using the in-process gsc command tree.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rudrankriyam/Galaxy-Store-CLI/cmd"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/commanddocs"
)

func main() {
	var check bool
	var output string
	flag.BoolVar(&check, "check", false, "Fail if the committed command reference is stale")
	flag.StringVar(&output, "output", "docs/COMMANDS.md", "Command-reference output path")
	flag.Parse()

	reference := []byte(commanddocs.Render(cmd.RootCommand("documentation", io.Discard, io.Discard)))
	if check {
		if err := checkReference(output, reference); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := writeReference(output, reference); err != nil {
		fmt.Fprintf(os.Stderr, "write command reference: %v\n", err)
		os.Exit(1)
	}
}

func checkReference(path string, expected []byte) error {
	actual, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is missing; run make docs", path)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("%s is stale; run make docs and commit the result", path)
	}
	return nil
}

func writeReference(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".commands-*.md")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
