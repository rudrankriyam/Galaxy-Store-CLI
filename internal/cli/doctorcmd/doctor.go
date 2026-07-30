// Package doctorcmd implements deterministic Galaxy Store CLI diagnostics.
package doctorcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungauth "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

// ErrUnhealthy indicates that doctor printed a report containing a failed
// check. The report remains available on stdout.
var ErrUnhealthy = errors.New("doctor found failing checks")

// Printer renders the diagnostic report.
type Printer interface {
	Print(output.Format, any) error
}

// RuntimeInfo is the non-sensitive command runtime identity.
type RuntimeInfo struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Dependencies keeps diagnostics deterministic and makes all local and remote
// activity explicit in tests.
type Dependencies struct {
	Stderr  io.Writer
	Printer Printer
	Version string

	LoadConfig         func() (*config.Config, error)
	ResolveCredentials func(credentials.Options) (credentials.Credentials, error)
	RuntimeInfo        func() RuntimeInfo
	CheckToken         func(context.Context, string, string) error
}

// DefaultDependencies creates production diagnostics. It constructs the
// read-only authentication client but performs no network request until
// --remote is explicitly passed.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
	version string,
) (Dependencies, error) {
	authClient, err := samsungauth.NewClient(samsungauth.ClientOptions{})
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		Stderr:             stderr,
		Printer:            output.NewPrinter(stdout, isTerminal),
		Version:            version,
		LoadConfig:         config.Load,
		ResolveCredentials: credentials.Resolve,
		RuntimeInfo: func() RuntimeInfo {
			return RuntimeInfo{
				GoVersion: runtime.Version(),
				GOOS:      runtime.GOOS,
				GOARCH:    runtime.GOARCH,
			}
		},
		CheckToken: func(ctx context.Context, serviceAccountID, accessToken string) error {
			response, checkErr := authClient.Check(ctx, serviceAccountID, accessToken)
			if checkErr != nil {
				return checkErr
			}
			if response == nil || !response.OK {
				return errors.New("access token was not confirmed")
			}
			return nil
		},
	}, nil
}

// NewCommand creates gsc doctor.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var outputValue string
	var remote bool
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&remote, "remote", false, "Validate the existing access token with Samsung")

	command := &ffcli.Command{
		Name:       "doctor",
		ShortUsage: "gsc doctor [--profile NAME] [--output FORMAT] [--remote]",
		ShortHelp:  "Diagnose local configuration and existing Galaxy Store credentials.",
		LongHelp: `Diagnose local configuration and existing Galaxy Store credentials.

By default, every check is local and no token is minted or sent over the
network. Pass --remote to validate an existing access token with Samsung.
Private keys, private-key paths, access tokens, and authorization headers are
never included in the report.

Examples:
  gsc doctor
  gsc doctor --profile production --output table
  gsc doctor --profile production --remote --output json`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("doctor does not accept positional arguments")
			}
			return run(ctx, dependencies, options{
				Profile: profile,
				Output:  outputValue,
				Remote:  remote,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type options struct {
	Profile string
	Output  string
	Remote  bool
}

type checkStatus string

const (
	statusPass checkStatus = "pass"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
	statusSkip checkStatus = "skip"
)

type check struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
}

type configSummary struct {
	Configured               bool `json:"configured"`
	Readable                 bool `json:"readable"`
	Valid                    bool `json:"valid"`
	ProfileCount             int  `json:"profileCount"`
	DefaultProfileConfigured bool `json:"defaultProfileConfigured"`
}

type credentialSummary struct {
	Kind                    credentials.Kind `json:"kind,omitempty"`
	Profile                 string           `json:"profile,omitempty"`
	TokenPresent            bool             `json:"tokenPresent"`
	ServiceAccountIDPresent bool             `json:"serviceAccountIdPresent"`
	PrivateKeyConfigured    bool             `json:"privateKeyConfigured"`
}

type result struct {
	Healthy         bool              `json:"healthy"`
	RemoteRequested bool              `json:"remoteRequested"`
	CommandVersion  string            `json:"commandVersion"`
	Runtime         RuntimeInfo       `json:"runtime"`
	Config          configSummary     `json:"config"`
	Credentials     credentialSummary `json:"credentials"`
	Checks          []check           `json:"checks"`
}

func (value result) OutputHeaders() []string {
	return []string{"CHECK", "STATUS", "DETAIL"}
}

func (value result) OutputRows() [][]string {
	rows := make([][]string, len(value.Checks))
	for index, item := range value.Checks {
		rows[index] = []string{
			item.Name,
			strings.ToUpper(string(item.Status)),
			item.Detail,
		}
	}
	return rows
}

func run(ctx context.Context, dependencies Dependencies, options options) error {
	format, err := output.ParseFormat(options.Output)
	if err != nil {
		return shared.UsageErrorf("%v", err)
	}
	if err := validateDependencies(dependencies, options.Remote); err != nil {
		return err
	}

	commandVersion := strings.TrimSpace(dependencies.Version)
	if commandVersion == "" {
		commandVersion = "dev"
	}
	runtimeInfo := dependencies.RuntimeInfo()
	report := result{
		Healthy:         true,
		RemoteRequested: options.Remote,
		CommandVersion:  commandVersion,
		Runtime:         runtimeInfo,
		Checks: []check{
			{
				Name:   "command",
				Status: statusPass,
				Detail: "gsc " + commandVersion,
			},
			{
				Name:   "runtime",
				Status: statusPass,
				Detail: runtimeDetail(runtimeInfo),
			},
		},
	}

	cfg, configErr := dependencies.LoadConfig()
	switch {
	case configErr == nil && cfg != nil:
		if err := cfg.Validate(); err != nil {
			report.Config = configSummary{Configured: true, Readable: true}
			report.addCheck("config", statusFail, "Configuration is readable but invalid.")
		} else {
			report.Config = configSummary{
				Configured:               true,
				Readable:                 true,
				Valid:                    true,
				ProfileCount:             len(cfg.Profiles),
				DefaultProfileConfigured: strings.TrimSpace(cfg.DefaultProfile) != "",
			}
			report.addCheck("config", statusPass, configDetail(report.Config))
		}
	case errors.Is(configErr, config.ErrNotFound):
		report.addCheck(
			"config",
			statusWarn,
			"No config file is present; a complete environment access-token pair can still be used.",
		)
	default:
		report.Config.Configured = true
		report.addCheck("config", statusFail, "Configuration could not be read or validated.")
	}

	resolved, credentialErr := dependencies.ResolveCredentials(credentials.Options{
		Profile: strings.TrimSpace(options.Profile),
	})
	usableToken := credentialErr == nil &&
		resolved.Kind == credentials.KindAccessToken &&
		strings.TrimSpace(resolved.AccessToken) != "" &&
		strings.TrimSpace(resolved.ServiceAccountID) != ""
	if credentialErr != nil {
		report.addCheck(
			"credentials",
			statusFail,
			`No usable access token was resolved; run "gsc auth login".`,
		)
	} else {
		report.Credentials = credentialSummary{
			Kind:                    resolved.Kind,
			Profile:                 resolved.Profile,
			TokenPresent:            strings.TrimSpace(resolved.AccessToken) != "",
			ServiceAccountIDPresent: strings.TrimSpace(resolved.ServiceAccountID) != "",
			PrivateKeyConfigured:    strings.TrimSpace(resolved.PrivateKeyPath) != "",
		}
		if usableToken {
			report.addCheck(
				"credentials",
				statusPass,
				"An existing access token and service account ID are available.",
			)
		} else {
			report.addCheck(
				"credentials",
				statusFail,
				`A service-account profile exists but has no stored access token; run "gsc auth login".`,
			)
		}
	}

	if !options.Remote {
		report.addCheck("remote", statusSkip, "Not requested; no network call was made.")
	} else if !usableToken {
		report.addCheck(
			"remote",
			statusFail,
			`Remote validation requires an existing access token; run "gsc auth login".`,
		)
	} else if err := dependencies.CheckToken(
		ctx,
		resolved.ServiceAccountID,
		resolved.AccessToken,
	); err != nil {
		report.addCheck(
			"remote",
			statusFail,
			"Samsung did not validate the existing access token.",
		)
	} else {
		report.addCheck("remote", statusPass, "Samsung validated the existing access token.")
	}

	if err := dependencies.Printer.Print(format, report); err != nil {
		return err
	}
	if !report.Healthy {
		return ErrUnhealthy
	}
	return nil
}

func (value *result) addCheck(name string, status checkStatus, detail string) {
	value.Checks = append(value.Checks, check{
		Name:   name,
		Status: status,
		Detail: detail,
	})
	if status == statusFail {
		value.Healthy = false
	}
}

func runtimeDetail(info RuntimeInfo) string {
	goVersion := strings.TrimSpace(info.GoVersion)
	if goVersion == "" {
		goVersion = "unknown Go"
	}
	goos := strings.TrimSpace(info.GOOS)
	goarch := strings.TrimSpace(info.GOARCH)
	if goos == "" || goarch == "" {
		return goVersion
	}
	return fmt.Sprintf("%s %s/%s", goVersion, goos, goarch)
}

func configDetail(summary configSummary) string {
	defaultState := "no default profile"
	if summary.DefaultProfileConfigured {
		defaultState = "default profile configured"
	}
	return fmt.Sprintf("%d profile(s); %s", summary.ProfileCount, defaultState)
}

func validateDependencies(dependencies Dependencies, remote bool) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("doctor output printer is not configured")
	case dependencies.LoadConfig == nil:
		return errors.New("doctor config loader is not configured")
	case dependencies.ResolveCredentials == nil:
		return errors.New("doctor credential resolver is not configured")
	case dependencies.RuntimeInfo == nil:
		return errors.New("doctor runtime inspector is not configured")
	case remote && dependencies.CheckToken == nil:
		return errors.New("doctor remote token checker is not configured")
	default:
		return nil
	}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var _ output.RowSource = result{}
