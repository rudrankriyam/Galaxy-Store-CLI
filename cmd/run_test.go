package cmd

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"
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
	for _, command := range []string{
		"api",
		"apps",
		"auth",
		"beta",
		"binaries",
		"capabilities",
		"doctor",
		"iap",
		"reviews",
		"rollouts",
		"schema",
		"search",
		"stats",
		"uploads",
		"version",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("stdout = %q, want %q command", stdout.String(), command)
		}
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

func TestRunAuthWithoutSubcommandReturnsUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"auth"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "auth requires a command") {
		t.Fatalf("stderr = %q, want auth usage diagnostic", stderr.String())
	}
}

func TestRunAppsWithoutSubcommandReturnsUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"apps"}, "dev-test", &stdout, &stderr)

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if !strings.Contains(
		stderr.String(),
		"apps requires a command: list, view, update, submit, or status",
	) {
		t.Fatalf("stderr = %q, want apps usage diagnostic", stderr.String())
	}
}

func TestRootCommandRegistersCompleteCommandTree(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	root := RootCommand("dev-test", &stdout, &stderr)
	got := make([]string, 0, len(root.Subcommands))
	for _, command := range root.Subcommands {
		got = append(got, command.Name)
	}
	want := []string{
		"auth",
		"apps",
		"binaries",
		"uploads",
		"beta",
		"rollouts",
		"reviews",
		"iap",
		"stats",
		"api",
		"doctor",
		"capabilities",
		"schema",
		"search",
		"version",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("root commands = %v, want %v", got, want)
	}

	apps := findSubcommand(t, root, "apps")
	if got, want := subcommandNames(apps), []string{"list", "view", "update", "submit", "status"}; !slices.Equal(got, want) {
		t.Fatalf("apps commands = %v, want %v", got, want)
	}

	iap := findSubcommand(t, root, "iap")
	if got, want := subcommandNames(iap), []string{"items", "purchases", "subscriptions", "orders", "receipts"}; !slices.Equal(got, want) {
		t.Fatalf("iap commands = %v, want %v", got, want)
	}
}

func TestRunCommandGroupsWithoutSubcommandsReturnsUsage(t *testing.T) {
	testCases := []struct {
		command string
		want    string
	}{
		{command: "binaries", want: "binaries requires a command"},
		{command: "uploads", want: "uploads requires a command"},
		{command: "beta", want: "beta requires a command"},
		{command: "rollouts", want: "rollouts requires a command"},
		{command: "reviews", want: "reviews requires a command"},
		{
			command: "iap",
			want:    "iap requires a command: items, purchases, subscriptions, orders, or receipts",
		},
		{command: "stats", want: "stats requires a command"},
		{command: "api", want: "api requires the request command"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.command, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := run([]string{testCase.command}, "dev-test", &stdout, &stderr)

			if exitCode != ExitUsage {
				t.Fatalf(
					"exit code = %d, want %d; stdout=%q stderr=%q",
					exitCode,
					ExitUsage,
					stdout.String(),
					stderr.String(),
				)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want no data", stdout.String())
			}
			if !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.want)
			}
		})
	}
}

func findSubcommand(t *testing.T, command *ffcli.Command, name string) *ffcli.Command {
	t.Helper()
	for _, subcommand := range command.Subcommands {
		if subcommand.Name == name {
			return subcommand
		}
	}
	t.Fatalf("%s command not found", name)
	return nil
}

func subcommandNames(command *ffcli.Command) []string {
	names := make([]string, 0, len(command.Subcommands))
	for _, subcommand := range command.Subcommands {
		names = append(names, subcommand.Name)
	}
	return names
}
