package doctorcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

const (
	secretToken      = "secret-access-token"
	secretPrivateKey = "/private/keys/seller.pem"
	secretAccountID  = "private-service-account"
)

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "doctor" ||
		!strings.Contains(command.ShortUsage, "--remote") ||
		command.LongHelp == "" {
		t.Fatalf("command = %#v", command)
	}
}

func TestDoctorIsLocalByDefaultAndReportsSafeMetadata(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var remoteCalls int
	dependencies := healthyDependencies(&stdout)
	dependencies.CheckToken = func(context.Context, string, string) error {
		remoteCalls++
		return errors.New("must not call")
	}

	if err := execute(
		NewCommand(dependencies),
		"--profile", "production",
		"--output", "json",
	); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if remoteCalls != 0 {
		t.Fatalf("remote calls = %d, want 0", remoteCalls)
	}
	assertNoSecrets(t, stdout.String())

	var report result
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !report.Healthy || report.RemoteRequested {
		t.Fatalf("report = %#v", report)
	}
	if !report.Config.Valid || report.Config.ProfileCount != 1 {
		t.Fatalf("config = %#v", report.Config)
	}
	if report.Credentials.Kind != credentials.KindAccessToken ||
		!report.Credentials.TokenPresent ||
		!report.Credentials.ServiceAccountIDPresent ||
		report.Credentials.PrivateKeyConfigured {
		t.Fatalf("credentials = %#v", report.Credentials)
	}
	if got := statusFor(report.Checks, "remote"); got != statusSkip {
		t.Fatalf("remote status = %q, want skip", got)
	}
}

func TestDoctorRemoteFlagValidatesExistingTokenOnce(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	var calls int
	dependencies.CheckToken = func(_ context.Context, serviceAccountID, token string) error {
		calls++
		if serviceAccountID != secretAccountID || token != secretToken {
			t.Fatalf("remote credentials = %q/%q", serviceAccountID, token)
		}
		return nil
	}

	if err := execute(NewCommand(dependencies), "--remote", "--output", "json"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if calls != 1 {
		t.Fatalf("remote calls = %d, want 1", calls)
	}
	assertNoSecrets(t, stdout.String())

	var report result
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.RemoteRequested || statusFor(report.Checks, "remote") != statusPass {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorMissingConfigCanUseEnvironmentAccessToken(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	dependencies.LoadConfig = func() (*config.Config, error) {
		return nil, config.ErrNotFound
	}
	if err := execute(NewCommand(dependencies), "--output", "json"); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	var report result
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.Healthy || statusFor(report.Checks, "config") != statusWarn {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorPrintsRedactedFailureReportForCredentialErrors(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	dependencies.ResolveCredentials = func(credentials.Options) (credentials.Credentials, error) {
		return credentials.Credentials{}, errors.New(
			"failed using " + secretToken + " " + secretPrivateKey + " " + secretAccountID,
		)
	}

	err := execute(NewCommand(dependencies), "--output", "json")
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("doctor error = %v, want ErrUnhealthy", err)
	}
	assertNoSecrets(t, stdout.String(), err.Error())

	var report result
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode report: %v", decodeErr)
	}
	if report.Healthy || statusFor(report.Checks, "credentials") != statusFail {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorRejectsServiceAccountOnlyProfileWithoutPrintingPath(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	dependencies.ResolveCredentials = func(credentials.Options) (credentials.Credentials, error) {
		return credentials.Credentials{
			Kind:             credentials.KindServiceAccount,
			Profile:          "production",
			ServiceAccountID: secretAccountID,
			PrivateKeyPath:   secretPrivateKey,
		}, nil
	}
	var remoteCalls int
	dependencies.CheckToken = func(context.Context, string, string) error {
		remoteCalls++
		return nil
	}

	err := execute(NewCommand(dependencies), "--remote", "--output", "json")
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("doctor error = %v", err)
	}
	if remoteCalls != 0 {
		t.Fatalf("remote calls = %d, want 0", remoteCalls)
	}
	assertNoSecrets(t, stdout.String(), err.Error())

	var report result
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode report: %v", decodeErr)
	}
	if !report.Credentials.PrivateKeyConfigured ||
		report.Credentials.TokenPresent ||
		statusFor(report.Checks, "remote") != statusFail {
		t.Fatalf("credentials/report = %#v / %#v", report.Credentials, report.Checks)
	}
}

func TestDoctorRemoteFailureIsRedacted(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	dependencies.CheckToken = func(context.Context, string, string) error {
		return errors.New("samsung echoed " + secretToken + " and " + secretAccountID)
	}
	err := execute(NewCommand(dependencies), "--remote", "--output", "json")
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("doctor error = %v", err)
	}
	assertNoSecrets(t, stdout.String(), err.Error())
}

func TestDoctorInvalidConfigIsAReportedFailure(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	dependencies := healthyDependencies(&stdout)
	dependencies.LoadConfig = func() (*config.Config, error) {
		return &config.Config{
			DefaultProfile: "missing",
			Profiles:       map[string]config.Profile{},
		}, nil
	}
	err := execute(NewCommand(dependencies), "--output", "json")
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("doctor error = %v", err)
	}

	var report result
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode report: %v", decodeErr)
	}
	if report.Config.Valid || statusFor(report.Checks, "config") != statusFail {
		t.Fatalf("config/report = %#v / %#v", report.Config, report.Checks)
	}
}

func TestDoctorTableAndMarkdownAreUsefulAndSecretFree(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"table", "markdown"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			if err := execute(
				NewCommand(healthyDependencies(&stdout)),
				"--output", format,
			); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			for _, expected := range []string{"CHECK", "STATUS", "DETAIL", "command", "runtime", "config", "credentials", "remote"} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("%s output missing %q:\n%s", format, expected, stdout.String())
				}
			}
			assertNoSecrets(t, stdout.String())
		})
	}
}

func TestDoctorValidatesInvocationBeforeLocalOrRemoteChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "positional", args: []string{"extra"}},
		{name: "invalid output", args: []string{"--output", "yaml"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var loadCalls int
			var resolveCalls int
			var remoteCalls int
			dependencies := healthyDependencies(io.Discard)
			dependencies.LoadConfig = func() (*config.Config, error) {
				loadCalls++
				return nil, errors.New("must not load")
			}
			dependencies.ResolveCredentials = func(credentials.Options) (credentials.Credentials, error) {
				resolveCalls++
				return credentials.Credentials{}, errors.New("must not resolve")
			}
			dependencies.CheckToken = func(context.Context, string, string) error {
				remoteCalls++
				return errors.New("must not check")
			}
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want UsageError", err, err)
			}
			if loadCalls != 0 || resolveCalls != 0 || remoteCalls != 0 {
				t.Fatalf("calls = load:%d resolve:%d remote:%d", loadCalls, resolveCalls, remoteCalls)
			}
		})
	}
}

func healthyDependencies(stdout io.Writer) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		Version: "dev-test",
		LoadConfig: func() (*config.Config, error) {
			return &config.Config{
				DefaultProfile: "production",
				Profiles: map[string]config.Profile{
					"production": {
						ServiceAccountID: secretAccountID,
						PrivateKeyPath:   secretPrivateKey,
						Scopes:           []string{"publishing"},
					},
				},
			}, nil
		},
		ResolveCredentials: func(credentials.Options) (credentials.Credentials, error) {
			return credentials.Credentials{
				Kind:             credentials.KindAccessToken,
				Profile:          "production",
				AccessToken:      secretToken,
				ServiceAccountID: secretAccountID,
				Scopes:           []string{"publishing"},
			}, nil
		},
		RuntimeInfo: func() RuntimeInfo {
			return RuntimeInfo{
				GoVersion: "go1.25.12",
				GOOS:      "testos",
				GOARCH:    "testarch",
			}
		},
		CheckToken: func(context.Context, string, string) error {
			return nil
		},
	}
}

func statusFor(checks []check, name string) checkStatus {
	for _, item := range checks {
		if item.Name == name {
			return item.Status
		}
	}
	return ""
}

func assertNoSecrets(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		for _, secret := range []string{secretToken, secretPrivateKey, secretAccountID} {
			if strings.Contains(value, secret) {
				t.Fatalf("value leaked %q: %s", secret, value)
			}
		}
	}
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
