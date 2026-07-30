package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutArgumentsPrintsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(nil, "dev-test", &stdout, &stderr)

	if exitCode != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitSuccess, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Ship Android apps to Galaxy Store") {
		t.Fatalf("stdout = %q, want root help", stdout.String())
	}
}

func TestRunVersionFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--version"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitSuccess, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "dev-test" {
		t.Fatalf("stdout = %q, want %q", got, "dev-test")
	}
}

func TestRunVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"version"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitSuccess, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "dev-test" {
		t.Fatalf("stdout = %q, want %q", got, "dev-test")
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"missing"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "Error:") {
		t.Fatalf("stderr = %q, want error", stderr.String())
	}
}
