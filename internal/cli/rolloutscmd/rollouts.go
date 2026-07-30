// Package rolloutscmd implements staged rollout commands.
package rolloutscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/rollout"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

// Service is the Samsung staged-rollout API used by commands.
type Service interface {
	ViewRate(context.Context, string, string) (*rollout.Rate, error)
	SetRate(context.Context, rollout.SetRateInput) (*rollout.MutationResult, error)
	Complete(context.Context, string, string) (*rollout.MutationResult, error)
	ViewBinaries(context.Context, string, string) (*rollout.BinaryList, error)
	AddBinary(context.Context, string, string) (*rollout.MutationResult, error)
	RemoveBinary(context.Context, string, string) (*rollout.MutationResult, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps rollout commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	ReadFile    func(string) ([]byte, error)
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until local input and mutation confirmation have been validated.
func DefaultDependencies(
	stdout io.Writer,
	stderr io.Writer,
	isTerminal output.TerminalDetector,
) (Dependencies, error) {
	factory, err := session.DefaultFactory()
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		Stderr:   stderr,
		Printer:  output.NewPrinter(stdout, isTerminal),
		ReadFile: os.ReadFile,
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			return rollout.New(active.Client)
		},
	}, nil
}

// NewCommand creates the gsc rollouts command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	rate := newRateCommand(dependencies, stderr)
	binaries := newBinariesCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "rollouts",
		ShortUsage:  "gsc rollouts <command> [flags]",
		ShortHelp:   "Manage Galaxy Store staged rollout rates and binaries.",
		Subcommands: []*ffcli.Command{rate, binaries},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("rollouts requires a command: rate or binaries")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	view := newRateViewCommand(dependencies, stderr)
	update := newRateUpdateCommand(dependencies, stderr)
	complete := newRateCompleteCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "rate",
		ShortUsage:  "gsc rollouts rate <command> [flags]",
		ShortHelp:   "View, advance, or complete staged rollout rates.",
		Subcommands: []*ffcli.Command{view, update, complete},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("rollouts rate requires a command: view, update, or complete")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinariesCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	list := newBinariesListCommand(dependencies, stderr)
	update := newBinariesUpdateCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "binaries",
		ShortUsage:  "gsc rollouts binaries <command> [flags]",
		ShortHelp:   "List or update binaries participating in a staged rollout.",
		Subcommands: []*ffcli.Command{list, update},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("rollouts binaries requires a command: list or update")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type commonFlags struct {
	Profile, ContentID, AppStatus, Output string
}

func bindCommon(flags *flag.FlagSet, values *commonFlags, includeStatus bool) {
	flags.StringVar(&values.Profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&values.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	if includeStatus {
		flags.StringVar(&values.AppStatus, "app-status", "", "Required app variant: SALE or REGISTRATION")
	}
	flags.StringVar(&values.Output, "output", "auto", "Output format: auto, json, table, or markdown")
}

func newRateViewCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("rollouts rate view", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var values commonFlags
	bindCommon(flags, &values, true)
	command := &ffcli.Command{
		Name:       "view",
		ShortUsage: "gsc rollouts rate view --content-id ID --app-status STATUS [flags]",
		ShortHelp:  "View default and country-specific rollout rates.",
		LongHelp: `View default and country-specific rollout rates.

Example:
  gsc rollouts rate view --content-id 000007654321 --app-status SALE --output table`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("rollouts rate view does not accept positional arguments")
			}
			return runRateView(ctx, dependencies, values)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRateUpdateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("rollouts rate update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var values commonFlags
	var file string
	var dryRun, confirm bool
	bindCommon(flags, &values, true)
	flags.StringVar(&file, "file", "", "Required JSON rollout-rate file")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and print the mutation plan without opening a session")
	flags.BoolVar(&confirm, "confirm", false, "Confirm the rollout-rate mutation")
	command := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc rollouts rate update --content-id ID --app-status STATUS --file rates.json [--dry-run | --confirm]",
		ShortHelp:  "Enable or monotonically advance rollout rates from JSON.",
		LongHelp: `Enable or monotonically advance rollout rates from JSON.

The file contains rolloutRate and an optional countries array. Before writing,
gsc reads Samsung's current rate and refuses default or country decreases.

Examples:
  gsc rollouts rate update --content-id 000007654321 --app-status SALE --file rates.json --dry-run
  gsc rollouts rate update --content-id 000007654321 --app-status SALE --file rates.json --confirm`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("rollouts rate update does not accept positional arguments")
			}
			return runRateUpdate(ctx, dependencies, values, file, shared.MutationMode{
				DryRun: dryRun, Confirm: confirm,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRateCompleteCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("rollouts rate complete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var values commonFlags
	var dryRun, confirm bool
	bindCommon(flags, &values, true)
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and print the mutation plan without opening a session")
	flags.BoolVar(&confirm, "confirm", false, "Confirm global completion of the rollout")
	command := &ffcli.Command{
		Name:       "complete",
		ShortUsage: "gsc rollouts rate complete --content-id ID --app-status STATUS [--dry-run | --confirm]",
		ShortHelp:  "Complete a staged rollout by deploying to all users globally.",
		LongHelp: `Complete a staged rollout by deploying to all users globally.

Samsung calls this DISABLE_ROLLOUT, but it does not stop distribution: it makes
the release available to 100% of users. gsc names the operation complete to
preserve that safety-critical meaning.

Example:
  gsc rollouts rate complete --content-id 000007654321 --app-status SALE --confirm`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("rollouts rate complete does not accept positional arguments")
			}
			return runRateComplete(ctx, dependencies, values, shared.MutationMode{
				DryRun: dryRun, Confirm: confirm,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinariesListCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("rollouts binaries list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var values commonFlags
	bindCommon(flags, &values, true)
	command := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc rollouts binaries list --content-id ID --app-status STATUS [flags]",
		ShortHelp:  "List binary rollout status for an explicit app variant.",
		LongHelp: `List binary rollout status for an explicit app variant.

Example:
  gsc rollouts binaries list --content-id 000007654321 --app-status REGISTRATION`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("rollouts binaries list does not accept positional arguments")
			}
			return runBinariesList(ctx, dependencies, values)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinariesUpdateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("rollouts binaries update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var values commonFlags
	var file string
	var dryRun, confirm bool
	bindCommon(flags, &values, false)
	flags.StringVar(&file, "file", "", "Required JSON binary update file")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and print the mutation plan without opening a session")
	flags.BoolVar(&confirm, "confirm", false, "Confirm the binary rollout mutation")
	command := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc rollouts binaries update --content-id ID --file binary.json [--dry-run | --confirm]",
		ShortHelp:  "Add or remove one binary from a staged rollout.",
		LongHelp: `Add or remove one binary from a staged rollout.

The JSON file contains function (ADD or REMOVE) and binarySeq.

Examples:
  gsc rollouts binaries update --content-id 000007654321 --file binary.json --dry-run
  gsc rollouts binaries update --content-id 000007654321 --file binary.json --confirm`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("rollouts binaries update does not accept positional arguments")
			}
			return runBinariesUpdate(ctx, dependencies, values, file, shared.MutationMode{
				DryRun: dryRun, Confirm: confirm,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type rateFile struct {
	RolloutRate int                   `json:"rolloutRate"`
	Countries   []rollout.CountryRate `json:"countries"`
}

type binaryFile struct {
	Function  string `json:"function"`
	BinarySeq string `json:"binarySeq"`
}

func runRateView(ctx context.Context, dependencies Dependencies, values commonFlags) error {
	contentID, status, format, err := validateCommon(values, true)
	if err != nil {
		return err
	}
	service, err := openService(dependencies, values.Profile)
	if err != nil {
		return err
	}
	result, err := service.ViewRate(ctx, contentID, status)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid rollout-rate response")
	}
	return dependencies.Printer.Print(format, rateResult{Rate: result})
}

func runRateUpdate(
	ctx context.Context,
	dependencies Dependencies,
	values commonFlags,
	file string,
	mode shared.MutationMode,
) error {
	contentID, status, format, err := validateCommon(values, true)
	if err != nil {
		return err
	}
	var input rateFile
	if err := readStrictFile(dependencies, file, &input); err != nil {
		return err
	}
	if err := validateRateFile(input); err != nil {
		return err
	}
	if err := mode.RequireConfirmation("advance the staged rollout"); err != nil {
		return err
	}
	if mode.DryRun {
		if dependencies.Printer == nil {
			return errors.New("rollouts command output printer is not configured")
		}
		return dependencies.Printer.Print(format, rateUpdatePlan(contentID, status, input))
	}
	service, err := openService(dependencies, values.Profile)
	if err != nil {
		return err
	}
	result, err := service.SetRate(ctx, rollout.SetRateInput{
		ContentID: contentID, AppStatus: status, RolloutRate: input.RolloutRate,
		Countries: append([]rollout.CountryRate(nil), input.Countries...),
	})
	if err != nil {
		return err
	}
	return printMutation(dependencies, format, result)
}

func runRateComplete(
	ctx context.Context,
	dependencies Dependencies,
	values commonFlags,
	mode shared.MutationMode,
) error {
	contentID, status, format, err := validateCommon(values, true)
	if err != nil {
		return err
	}
	if err := mode.RequireConfirmation("complete the staged rollout for all users globally"); err != nil {
		return err
	}
	if mode.DryRun {
		if dependencies.Printer == nil {
			return errors.New("rollouts command output printer is not configured")
		}
		return dependencies.Printer.Print(format, shared.Plan{
			Operations: []shared.Operation{{
				Action:   "complete",
				Resource: "staged-rollout/" + contentID + "/" + status,
				Details:  "deploy the release to all users globally",
			}},
			Warnings: []string{
				"Samsung names this DISABLE_ROLLOUT; it completes distribution rather than stopping it.",
				"Execution first checks that no country rate exceeds the default rate.",
			},
			RequiresConfirmation: true,
			MutationsPerformed:   false,
		})
	}
	service, err := openService(dependencies, values.Profile)
	if err != nil {
		return err
	}
	result, err := service.Complete(ctx, contentID, status)
	if err != nil {
		return err
	}
	return printMutation(dependencies, format, result)
}

func runBinariesList(ctx context.Context, dependencies Dependencies, values commonFlags) error {
	contentID, status, format, err := validateCommon(values, true)
	if err != nil {
		return err
	}
	service, err := openService(dependencies, values.Profile)
	if err != nil {
		return err
	}
	result, err := service.ViewBinaries(ctx, contentID, status)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid rollout-binary response")
	}
	return dependencies.Printer.Print(format, binariesResult{BinaryList: result})
}

func runBinariesUpdate(
	ctx context.Context,
	dependencies Dependencies,
	values commonFlags,
	file string,
	mode shared.MutationMode,
) error {
	contentID, _, format, err := validateCommon(values, false)
	if err != nil {
		return err
	}
	var input binaryFile
	if err := readStrictFile(dependencies, file, &input); err != nil {
		return err
	}
	if err := validateBinaryFile(input); err != nil {
		return err
	}
	if err := mode.RequireConfirmation(strings.ToLower(input.Function) + " the staged rollout binary"); err != nil {
		return err
	}
	if mode.DryRun {
		if dependencies.Printer == nil {
			return errors.New("rollouts command output printer is not configured")
		}
		return dependencies.Printer.Print(format, shared.Plan{
			Operations: []shared.Operation{{
				Action:   strings.ToLower(input.Function),
				Resource: "staged-rollout-binary/" + contentID + "/" + input.BinarySeq,
			}},
			Warnings:             []string{},
			RequiresConfirmation: true,
			MutationsPerformed:   false,
		})
	}
	service, err := openService(dependencies, values.Profile)
	if err != nil {
		return err
	}
	var result *rollout.MutationResult
	switch input.Function {
	case "ADD":
		result, err = service.AddBinary(ctx, contentID, input.BinarySeq)
	case "REMOVE":
		result, err = service.RemoveBinary(ctx, contentID, input.BinarySeq)
	}
	if err != nil {
		return err
	}
	return printMutation(dependencies, format, result)
}

func validateCommon(
	values commonFlags,
	requireStatus bool,
) (string, string, output.Format, error) {
	if err := shared.RequireValue("--content-id", values.ContentID); err != nil {
		return "", "", "", err
	}
	if err := shared.ValidateContentID(values.ContentID); err != nil {
		return "", "", "", err
	}
	status := ""
	var err error
	if requireStatus {
		status, err = shared.NormalizeAppStatus(values.AppStatus)
		if err != nil {
			return "", "", "", err
		}
	}
	format, err := output.ParseFormat(values.Output)
	if err != nil {
		return "", "", "", shared.UsageErrorf("%v", err)
	}
	return values.ContentID, status, format, nil
}

func readStrictFile(dependencies Dependencies, path string, destination any) error {
	if err := shared.RequireValue("--file", path); err != nil {
		return err
	}
	if dependencies.ReadFile == nil {
		return errors.New("rollouts command file reader is not configured")
	}
	data, err := dependencies.ReadFile(path)
	if err != nil {
		return shared.UsageErrorf("read --file: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return shared.UsageErrorf("decode --file: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return shared.UsageErrorf("decode --file: multiple JSON values are not allowed")
		}
		return shared.UsageErrorf("decode --file: %v", err)
	}
	return nil
}

func validateRateFile(input rateFile) error {
	if input.RolloutRate < 1 || input.RolloutRate > 100 {
		return shared.UsageErrorf("rolloutRate must be between 1 and 100")
	}
	seen := make(map[string]bool, len(input.Countries))
	for _, country := range input.Countries {
		if len(country.CountryCode) != 3 || country.CountryCode != strings.ToUpper(country.CountryCode) {
			return shared.UsageErrorf("countryCode %q must be three uppercase ASCII letters", country.CountryCode)
		}
		for _, character := range country.CountryCode {
			if character < 'A' || character > 'Z' {
				return shared.UsageErrorf("countryCode %q must be three uppercase ASCII letters", country.CountryCode)
			}
		}
		if seen[country.CountryCode] {
			return shared.UsageErrorf("countryCode %q is duplicated", country.CountryCode)
		}
		seen[country.CountryCode] = true
		if country.RolloutRate < 1 || country.RolloutRate > 100 {
			return shared.UsageErrorf("rolloutRate for %s must be between 1 and 100", country.CountryCode)
		}
	}
	return nil
}

func validateBinaryFile(input binaryFile) error {
	switch input.Function {
	case "ADD", "REMOVE":
	default:
		return shared.UsageErrorf("function must be exactly ADD or REMOVE")
	}
	if input.BinarySeq == "" || input.BinarySeq != strings.TrimSpace(input.BinarySeq) {
		return shared.UsageErrorf("binarySeq must be a positive integer")
	}
	value, err := strconv.Atoi(input.BinarySeq)
	if err != nil || value <= 0 {
		return shared.UsageErrorf("binarySeq must be a positive integer")
	}
	return nil
}

func openService(dependencies Dependencies, profile string) (Service, error) {
	switch {
	case dependencies.Printer == nil:
		return nil, errors.New("rollouts command output printer is not configured")
	case dependencies.OpenService == nil:
		return nil, errors.New("rollouts command session factory is not configured")
	}
	service, err := dependencies.OpenService(strings.TrimSpace(profile))
	if err != nil {
		return nil, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return nil, errors.New("open Galaxy Store session: rollout service is nil")
	}
	return service, nil
}

func printMutation(
	dependencies Dependencies,
	format output.Format,
	result *rollout.MutationResult,
) error {
	if result == nil {
		return errors.New("samsung returned an invalid rollout mutation response")
	}
	return dependencies.Printer.Print(format, mutationResult{MutationResult: result})
}

func rateUpdatePlan(contentID, status string, input rateFile) shared.Plan {
	return shared.Plan{
		Operations: []shared.Operation{{
			Action:   "advance",
			Resource: "staged-rollout/" + contentID + "/" + status,
			Details:  fmt.Sprintf("default=%d countries=%d", input.RolloutRate, len(input.Countries)),
		}},
		Warnings: []string{
			"Execution reads the current rollout first and refuses any default or country decrease.",
			"A country rate above the default prevents later completion until the default catches up.",
		},
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}
}

type rateResult struct {
	*rollout.Rate
}

func (result rateResult) OutputHeaders() []string {
	return []string{"COUNTRY", "ROLLOUT RATE", "DEFAULT"}
}

func (result rateResult) OutputRows() [][]string {
	if result.Rate == nil {
		return nil
	}
	rows := [][]string{{"DEFAULT", strconv.Itoa(result.RolloutRate), "true"}}
	for _, country := range result.Countries {
		rows = append(rows, []string{country.CountryCode, strconv.Itoa(country.RolloutRate), "false"})
	}
	return rows
}

type binariesResult struct {
	*rollout.BinaryList
}

func (result binariesResult) OutputHeaders() []string {
	return []string{"SEQUENCE", "FILE", "VERSION", "ROLLOUT STATUS", "APP STATUS"}
}

func (result binariesResult) OutputRows() [][]string {
	if result.BinaryList == nil {
		return nil
	}
	rows := make([][]string, len(result.Binaries))
	for index, binary := range result.Binaries {
		version := binary.VersionName
		if version == "" {
			version = binary.VersionCode
		}
		rows[index] = []string{
			strconv.Itoa(binary.Sequence), binary.FileName, version,
			binary.RolloutStatus, binary.AppStatus,
		}
	}
	return rows
}

type mutationResult struct {
	*rollout.MutationResult
}

func (result mutationResult) OutputHeaders() []string {
	return []string{"RESULT CODE", "MESSAGE", "FUNCTION", "COMPLETED"}
}

func (result mutationResult) OutputRows() [][]string {
	if result.MutationResult == nil {
		return nil
	}
	return [][]string{{
		result.ResultCode, result.ResultMessage, result.Function,
		strconv.FormatBool(result.Completed),
	}}
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var (
	_ output.RowSource = rateResult{}
	_ output.RowSource = binariesResult{}
	_ output.RowSource = mutationResult{}
)
