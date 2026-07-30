package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	if result.OperationCount != 39 || result.LimitationCount != 6 {
		t.Fatalf("capability counts = %d/%d, want 39/6", result.OperationCount, result.LimitationCount)
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
		"metadata",
		"ship",
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
		"search",
		"schema",
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

	assertSubcommands(t, findSubcommand(t, root, "auth"), "login", "status", "revoke")
	assertSubcommands(t, findSubcommand(t, root, "metadata"), "pull", "validate", "diff", "apply")
	assertSubcommands(t, findSubcommand(t, root, "ship"), "plan", "run")
	assertSubcommands(t, findSubcommand(t, apps, "status"), "update", "wait")
	assertSubcommands(t, findSubcommand(t, root, "binaries"), "add", "update", "delete")
	uploads := findSubcommand(t, root, "uploads")
	assertSubcommands(t, uploads, "sessions", "file")
	assertSubcommands(t, findSubcommand(t, uploads, "sessions"), "create")
	beta := findSubcommand(t, root, "beta")
	assertSubcommands(t, beta, "testers")
	assertSubcommands(t, findSubcommand(t, beta, "testers"), "list", "update")
	rollouts := findSubcommand(t, root, "rollouts")
	assertSubcommands(t, rollouts, "rate", "binaries")
	assertSubcommands(t, findSubcommand(t, rollouts, "rate"), "view", "update", "complete")
	assertSubcommands(t, findSubcommand(t, rollouts, "binaries"), "list", "update")
	reviews := findSubcommand(t, root, "reviews")
	assertSubcommands(t, reviews, "list", "reply")
	assertSubcommands(t, findSubcommand(t, reviews, "reply"), "delete")
	assertSubcommands(t, findSubcommand(t, iap, "items"), "list", "view", "create", "replace", "update", "delete")
	assertSubcommands(t, findSubcommand(t, iap, "purchases"), "consume", "acknowledge")
	assertSubcommands(t, findSubcommand(t, iap, "subscriptions"), "status", "cancel", "refund", "revoke")
	assertSubcommands(t, findSubcommand(t, iap, "orders"), "list")
	assertSubcommands(t, findSubcommand(t, iap, "receipts"), "verify")
	assertSubcommands(t, findSubcommand(t, root, "stats"), "seller", "content")
	assertSubcommands(t, findSubcommand(t, root, "api"), "request")

	for _, forbidden := range []string{"items", "receipts"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("unexpected top-level %q command in %v", forbidden, got)
		}
	}
}

func TestEveryCommandGroupHelpListsImmediateSubcommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := RootCommand("dev-test", &stdout, &stderr)

	testCases := []struct {
		path    string
		command *ffcli.Command
	}{
		{path: "gsc", command: root},
		{path: "gsc apps", command: findSubcommand(t, root, "apps")},
		{
			path: "gsc uploads sessions",
			command: findSubcommand(
				t,
				findSubcommand(t, root, "uploads"),
				"sessions",
			),
		},
		{
			path: "gsc iap subscriptions",
			command: findSubcommand(
				t,
				findSubcommand(t, root, "iap"),
				"subscriptions",
			),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			usage := testCase.command.UsageFunc(testCase.command)
			if !strings.Contains(
				usage,
				"Usage:\n  "+testCase.path+" <command> [flags]",
			) {
				t.Fatalf("usage = %q, want canonical group usage for %q", usage, testCase.path)
			}
			for _, subcommand := range testCase.command.Subcommands {
				want := fmt.Sprintf("  %-14s %s", subcommand.Name, subcommand.ShortHelp)
				if !strings.Contains(usage, want) {
					t.Errorf("usage = %q, want immediate command row %q", usage, want)
				}
			}
			wantHint := fmt.Sprintf(
				"Run %q for command help.",
				testCase.path+" <command> --help",
			)
			if !strings.Contains(usage, wantHint) {
				t.Errorf("usage = %q, want hint %q", usage, wantHint)
			}
		})
	}
}

func TestLeafCommandHelpIncludesLongHelpAndFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := RootCommand("dev-test", &stdout, &stderr)
	apps := findSubcommand(t, root, "apps")
	list := findSubcommand(t, apps, "list")

	usage := list.UsageFunc(list)
	for _, want := range []string{
		"Samsung returns the complete contentList array",
		"Examples:",
		"Flags:",
		"--limit int",
		"--output string",
		`(default "auto")`,
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage = %q, want %q", usage, want)
		}
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

func assertSubcommands(t *testing.T, command *ffcli.Command, want ...string) {
	t.Helper()
	if got := subcommandNames(command); !slices.Equal(got, want) {
		t.Fatalf("%s commands = %v, want %v", command.Name, got, want)
	}
}
