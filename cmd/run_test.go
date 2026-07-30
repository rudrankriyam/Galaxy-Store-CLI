package cmd

import (
	"bytes"
	"encoding/json"
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

func TestRunCapabilitiesCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"capabilities", "--output", "json"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitSuccess, stderr.String())
	}
	var result struct {
		OperationCount  int `json:"operationCount"`
		LimitationCount int `json:"limitationCount"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v; stdout=%q", err, stdout.String())
	}
	if result.OperationCount != 38 || result.LimitationCount != 6 {
		t.Fatalf("capability counts = %d/%d, want 38/6", result.OperationCount, result.LimitationCount)
	}
}

func TestRunSearchWithoutQueryReturnsUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"search"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no data", stdout.String())
	}
	if !strings.Contains(stderr.String(), "search query is required") {
		t.Fatalf("stderr = %q, want search diagnostic", stderr.String())
	}
}
