// Package authcmd implements the gsc authentication commands.
package authcmd

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungauth "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

// Client is the Samsung token API used by authentication commands.
type Client interface {
	Exchange(context.Context, string) (*samsungauth.AccessTokenResponse, error)
	Check(context.Context, string, string) (*samsungauth.TokenStatusResponse, error)
	Revoke(context.Context, string, string) (*samsungauth.TokenStatusResponse, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps authentication commands deterministic and testable.
type Dependencies struct {
	Stderr  io.Writer
	Printer Printer
	Client  Client

	LoadConfig func() (*config.Config, error)
	SaveConfig func(*config.Config) error

	OpenTokenStore     func() (credentials.TokenStore, error)
	ResolveCredentials func(credentials.Options, *config.Config, credentials.TokenStore) (credentials.Credentials, error)
	LoadPrivateKey     func(string) (*rsa.PrivateKey, error)
	SignJWT            func(samsungauth.JWTConfig) (string, error)
	Now                func() time.Time
}

// DefaultDependencies creates production dependencies without opening the OS
// credential manager until a command actually needs it.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
) (Dependencies, error) {
	client, err := samsungauth.NewClient(samsungauth.ClientOptions{})
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		Stderr:             stderr,
		Printer:            output.NewPrinter(stdout, isTerminal),
		Client:             client,
		LoadConfig:         config.Load,
		SaveConfig:         config.Save,
		OpenTokenStore:     credentials.OpenTokenStore,
		ResolveCredentials: credentials.ResolveFromConfigWithStore,
		LoadPrivateKey:     credentials.LoadPrivateKey,
		SignJWT:            samsungauth.SignJWT,
		Now:                time.Now,
	}, nil
}

// NewCommand creates the non-interactive auth command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	login := newLoginCommand(dependencies, stderr)
	status := newStatusCommand(dependencies, stderr)
	revoke := newRevokeCommand(dependencies, stderr)

	command := &ffcli.Command{
		Name:        "auth",
		ShortUsage:  "gsc auth <command> [flags]",
		ShortHelp:   "Manage Galaxy Store service-account authentication.",
		Subcommands: []*ffcli.Command{login, status, revoke},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("auth requires a command: login, status, or revoke")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newLoginCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("auth login", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var serviceAccountID string
	var privateKeyPath string
	var scopes stringList
	var outputValue string
	var setDefault bool
	var dryRun bool
	flags.StringVar(&profile, "profile", "", "Required profile name for secure token storage")
	flags.StringVar(&serviceAccountID, "service-account-id", "", "Required Samsung service account ID")
	flags.StringVar(&privateKeyPath, "private-key", "", "Required path to the Samsung RSA private key")
	flags.Var(&scopes, "scope", "Required service-account scope; repeat for publishing and gss")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&setDefault, "set-default", false, "Make this the default profile")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate inputs and show planned operations without changing state")

	command := &ffcli.Command{
		Name:       "login",
		ShortUsage: "gsc auth login --profile NAME --service-account-id ID --private-key PATH --scope SCOPE [flags]",
		ShortHelp:  "Exchange a service-account JWT and store the resulting token securely.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("auth login does not accept positional arguments")
			}
			return runLogin(ctx, dependencies, loginOptions{
				Profile:          profile,
				ServiceAccountID: serviceAccountID,
				PrivateKeyPath:   privateKeyPath,
				Scopes:           append([]string(nil), scopes...),
				Output:           outputValue,
				SetDefault:       setDefault,
				DryRun:           dryRun,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newStatusCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("auth status", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to the configured or environment profile)")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "status",
		ShortUsage: "gsc auth status [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Validate the resolved stored access token with Samsung.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("auth status does not accept positional arguments")
			}
			return runStatus(ctx, dependencies, statusOptions{
				Profile: profile,
				Output:  outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRevokeCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("auth revoke", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var outputValue string
	var confirm bool
	var dryRun bool
	flags.StringVar(&profile, "profile", "", "Required credential profile whose token will be revoked")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&confirm, "confirm", false, "Confirm permanent remote token revocation")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and show the revocation plan without changing state")

	command := &ffcli.Command{
		Name:       "revoke",
		ShortUsage: "gsc auth revoke --profile NAME [--dry-run | --confirm] [--output FORMAT]",
		ShortHelp:  "Revoke a token remotely, then remove it from secure local storage.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("auth revoke does not accept positional arguments")
			}
			return runRevoke(ctx, dependencies, revokeOptions{
				Profile: profile,
				Output:  outputValue,
				Mode: shared.MutationMode{
					DryRun:  dryRun,
					Confirm: confirm,
				},
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("scope must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

func validateCommonDependencies(dependencies Dependencies) error {
	switch {
	case dependencies.Printer == nil:
		return errors.New("auth command output printer is not configured")
	case dependencies.Client == nil:
		return errors.New("auth command Samsung client is not configured")
	default:
		return nil
	}
}

func validateLoginDependencies(dependencies Dependencies) error {
	if err := validateCommonDependencies(dependencies); err != nil {
		return err
	}
	switch {
	case dependencies.LoadConfig == nil:
		return errors.New("auth command config loader is not configured")
	case dependencies.SaveConfig == nil:
		return errors.New("auth command config saver is not configured")
	case dependencies.OpenTokenStore == nil:
		return errors.New("auth command token store is not configured")
	case dependencies.ResolveCredentials == nil:
		return errors.New("auth command credential resolver is not configured")
	case dependencies.LoadPrivateKey == nil:
		return errors.New("auth command private-key loader is not configured")
	case dependencies.SignJWT == nil:
		return errors.New("auth command JWT signer is not configured")
	default:
		return nil
	}
}

func validateCredentialDependencies(dependencies Dependencies) error {
	if err := validateCommonDependencies(dependencies); err != nil {
		return err
	}
	switch {
	case dependencies.LoadConfig == nil:
		return errors.New("auth command config loader is not configured")
	case dependencies.OpenTokenStore == nil:
		return errors.New("auth command token store is not configured")
	case dependencies.ResolveCredentials == nil:
		return errors.New("auth command credential resolver is not configured")
	default:
		return nil
	}
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func normalizeScopes(values []string) ([]samsungauth.Scope, []string, error) {
	if len(values) == 0 {
		return nil, nil, shared.UsageErrorf("--scope is required")
	}
	authScopes := make([]samsungauth.Scope, 0, len(values))
	configScopes := make([]string, 0, len(values))
	seen := make(map[samsungauth.Scope]struct{}, len(values))
	for _, value := range values {
		scope := samsungauth.Scope(strings.ToLower(strings.TrimSpace(value)))
		switch scope {
		case samsungauth.ScopePublishing, samsungauth.ScopeGSS:
		default:
			return nil, nil, shared.UsageErrorf(
				"invalid scope %q: must be publishing or gss",
				value,
			)
		}
		if _, exists := seen[scope]; exists {
			return nil, nil, shared.UsageErrorf("scope %q must not be repeated", scope)
		}
		seen[scope] = struct{}{}
		authScopes = append(authScopes, scope)
		configScopes = append(configScopes, string(scope))
	}
	return authScopes, configScopes, nil
}

func absolutePath(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve private key path: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func encodePrivateKey(privateKey *rsa.PrivateKey) ([]byte, error) {
	if privateKey == nil {
		return nil, errors.New("loaded private key is nil")
	}
	if err := privateKey.Validate(); err != nil {
		return nil, errors.New("loaded private key is invalid")
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}), nil
}
