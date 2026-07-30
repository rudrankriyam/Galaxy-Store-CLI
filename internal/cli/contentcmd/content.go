// Package contentcmd exposes Galaxy Store content mutations as non-interactive
// ffcli commands.
package contentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

const maximumUpdateFileSize = 10 << 20

// Service is the Samsung Content Publish API used by commands.
type Service interface {
	Update(context.Context, string, json.RawMessage) (*samsungcontent.Result, error)
	Submit(context.Context, string) (*samsungcontent.Result, error)
	ChangeStatus(context.Context, string, string) (*samsungcontent.Result, error)
	AddBinary(context.Context, samsungcontent.AddBinaryRequest) (*samsungcontent.AddBinaryResult, error)
	UpdateBinary(context.Context, samsungcontent.UpdateBinaryRequest) (*samsungcontent.Result, error)
	DeleteBinary(context.Context, string, string) (*samsungcontent.Result, error)
	CreateUploadSession(context.Context) (*samsungcontent.UploadSession, error)
	Upload(context.Context, string, string) (*samsungcontent.UploadResult, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps content commands deterministic and ensures credentials
// are not resolved until all local validation has succeeded.
type Dependencies struct {
	Stderr       io.Writer
	Printer      Printer
	OpenService  func(profile string) (Service, error)
	LoadFile     func(path string) ([]byte, error)
	ValidateFile func(path string) error
}

// DefaultDependencies creates production dependencies.
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
		Stderr:       stderr,
		Printer:      output.NewPrinter(stdout, isTerminal),
		LoadFile:     loadRegularFile,
		ValidateFile: validateRegularFile,
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			service, serviceErr := samsungcontent.New(active.Client)
			if serviceErr != nil {
				return nil, serviceErr
			}
			return service, nil
		},
	}, nil
}

// NewCommands creates standalone mutation command groups. Root integration can
// merge NewAppsSubcommands into the read-only apps command to avoid duplicate
// top-level app groups.
func NewCommands(dependencies Dependencies) []*ffcli.Command {
	return []*ffcli.Command{
		NewAppsCommand(dependencies),
		NewBinariesCommand(dependencies),
		NewUploadsCommand(dependencies),
	}
}

// NewAppsCommand creates an apps group containing content mutations.
func NewAppsCommand(dependencies Dependencies) *ffcli.Command {
	command := &ffcli.Command{
		Name:        "apps",
		ShortUsage:  "gsc apps <command> [flags]",
		ShortHelp:   "Update, submit, and change the status of Galaxy Store apps.",
		Subcommands: NewAppsSubcommands(dependencies),
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("apps requires a mutation command: update, submit, or status")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

// NewAppsSubcommands creates commands intended to be merged into the primary
// apps group at root registration time.
func NewAppsSubcommands(dependencies Dependencies) []*ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	return []*ffcli.Command{
		newAppUpdateCommand(dependencies, stderr),
		newAppSubmitCommand(dependencies, stderr),
		newAppStatusCommand(dependencies, stderr),
	}
}

// NewBinariesCommand creates the v2 binary command group.
func NewBinariesCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	command := &ffcli.Command{
		Name:       "binaries",
		ShortUsage: "gsc binaries <command> [flags]",
		ShortHelp:  "Manage app binaries through Samsung's current v2 API.",
		Subcommands: []*ffcli.Command{
			newBinaryAddCommand(dependencies, stderr),
			newBinaryUpdateCommand(dependencies, stderr),
			newBinaryDeleteCommand(dependencies, stderr),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("binaries requires a command: add, update, or delete")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

// NewUploadsCommand creates the upload-session and file command groups.
func NewUploadsCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	createSession := newUploadSessionCreateCommand(dependencies, stderr)
	sessions := &ffcli.Command{
		Name:        "sessions",
		ShortUsage:  "gsc uploads sessions create [flags]",
		ShortHelp:   "Create Samsung file-upload sessions.",
		Subcommands: []*ffcli.Command{createSession},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("uploads sessions requires the create command")
		},
	}
	sessions.UsageFunc = commandUsage

	command := &ffcli.Command{
		Name:        "uploads",
		ShortUsage:  "gsc uploads <command> [flags]",
		ShortHelp:   "Create upload sessions and stream files to Samsung.",
		Subcommands: []*ffcli.Command{sessions, newUploadFileCommand(dependencies, stderr)},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("uploads requires a command: sessions or file")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type commonOptions struct {
	Profile string
	Output  string
	Mode    shared.MutationMode
}

type updateOptions struct {
	Common    commonOptions
	ContentID string
	File      string
}

type submitOptions struct {
	Common    commonOptions
	ContentID string
}

type statusOptions struct {
	Common    commonOptions
	ContentID string
	Status    string
}

type binaryAddOptions struct {
	Common                      commonOptions
	ContentID                   string
	FileKey                     string
	GMS                         string
	CopyDeviceConfigurationFrom string
}

type binaryUpdateOptions struct {
	Common         commonOptions
	ContentID      string
	BinarySequence string
	GMS            string
}

type binaryDeleteOptions struct {
	Common         commonOptions
	ContentID      string
	BinarySequence string
}

type uploadFileOptions struct {
	Common    commonOptions
	SessionID string
	File      string
}

func newAppUpdateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("apps update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options updateOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&options.File, "file", "", "Required contentUpdate JSON file")
	command := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc apps update --content-id ID --file PATH [--dry-run | --confirm] [flags]",
		ShortHelp:  "Update Galaxy Store metadata while preserving JSON tri-state semantics.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps update does not accept positional arguments")
			}
			return runUpdate(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newAppSubmitCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("apps submit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options submitOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	command := &ffcli.Command{
		Name:       "submit",
		ShortUsage: "gsc apps submit --content-id ID [--dry-run | --confirm] [flags]",
		ShortHelp:  "Submit a REGISTERING app for Galaxy Store review.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps submit does not accept positional arguments")
			}
			return runSubmit(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newAppStatusCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("apps status update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options statusOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&options.Status, "status", "", "Required target: FOR_SALE, SUSPENDED, or TERMINATED")
	update := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc apps status update --content-id ID --status STATUS [--dry-run | --confirm] [flags]",
		ShortHelp:  "Distribute, suspend, or terminate an app.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("apps status update does not accept positional arguments")
			}
			return runStatusUpdate(ctx, dependencies, options)
		},
	}
	update.UsageFunc = commandUsage
	command := &ffcli.Command{
		Name:        "status",
		ShortUsage:  "gsc apps status update [flags]",
		ShortHelp:   "Change app distribution status.",
		Subcommands: []*ffcli.Command{update},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("apps status requires the update command")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinaryAddCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("binaries add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options binaryAddOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&options.FileKey, "file-key", "", "Required key returned by uploads file")
	flags.StringVar(&options.GMS, "gms", "", "Required Y or N for Google Mobile Services usage")
	flags.StringVar(
		&options.CopyDeviceConfigurationFrom,
		"copy-device-config-from",
		"",
		"Existing binary sequence whose device configuration is copied",
	)
	command := &ffcli.Command{
		Name:       "add",
		ShortUsage: "gsc binaries add --content-id ID --file-key KEY --gms Y|N [--dry-run | --confirm] [flags]",
		ShortHelp:  "Register an uploaded binary through Samsung's v2 endpoint.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("binaries add does not accept positional arguments")
			}
			return runBinaryAdd(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinaryUpdateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("binaries update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options binaryUpdateOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&options.BinarySequence, "binary-seq", "", "Required Seller Portal binary sequence")
	flags.StringVar(&options.GMS, "gms", "", "Required Y or N for Google Mobile Services usage")
	command := &ffcli.Command{
		Name:       "update",
		ShortUsage: "gsc binaries update --content-id ID --binary-seq SEQ --gms Y|N [--dry-run | --confirm] [flags]",
		ShortHelp:  "Update binary GMS metadata through Samsung's v2 endpoint.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("binaries update does not accept positional arguments")
			}
			return runBinaryUpdate(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newBinaryDeleteCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("binaries delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options binaryDeleteOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.ContentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&options.BinarySequence, "binary-seq", "", "Required Seller Portal binary sequence")
	command := &ffcli.Command{
		Name:       "delete",
		ShortUsage: "gsc binaries delete --content-id ID --binary-seq SEQ [--dry-run | --confirm] [flags]",
		ShortHelp:  "Permanently delete a binary through Samsung's v2 endpoint.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("binaries delete does not accept positional arguments")
			}
			return runBinaryDelete(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newUploadSessionCreateCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("uploads sessions create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options commonOptions
	bindCommonFlags(flags, &options)
	command := &ffcli.Command{
		Name:       "create",
		ShortUsage: "gsc uploads sessions create [--dry-run | --confirm] [flags]",
		ShortHelp:  "Create a Samsung upload session valid for 24 hours.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("uploads sessions create does not accept positional arguments")
			}
			return runUploadSessionCreate(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newUploadFileCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("uploads file", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options uploadFileOptions
	bindCommonFlags(flags, &options.Common)
	flags.StringVar(&options.SessionID, "session-id", "", "Required Samsung upload session ID")
	flags.StringVar(&options.File, "file", "", "Required regular, non-symlink file to stream")
	command := &ffcli.Command{
		Name:       "file",
		ShortUsage: "gsc uploads file --session-id ID --file PATH [--dry-run | --confirm] [flags]",
		ShortHelp:  "Stream an APK, AAB, image, or review file to Samsung.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("uploads file does not accept positional arguments")
			}
			return runUploadFile(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func bindCommonFlags(flags *flag.FlagSet, options *commonOptions) {
	flags.StringVar(&options.Profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&options.Output, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&options.Mode.DryRun, "dry-run", false, "Validate inputs and print the mutation plan without remote changes")
	flags.BoolVar(&options.Mode.Confirm, "confirm", false, "Confirm the remote mutation")
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

func loadRegularFile(path string) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maximumUpdateFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read input file: %w", err)
	}
	if len(data) > maximumUpdateFileSize {
		return nil, fmt.Errorf("input file exceeds %d bytes", maximumUpdateFileSize)
	}
	return data, nil
}

func validateRegularFile(path string) error {
	file, err := openRegularFile(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func openRegularFile(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--file is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect --file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("--file must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("--file must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open --file: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect open --file: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("--file changed while it was being opened")
	}
	return file, nil
}
