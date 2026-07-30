// Package apicmd implements the constrained raw Galaxy Store API command.
package apicmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

const maximumRequestFileSize = 10 << 20

// Client sends one authenticated request through the hardened Samsung client.
type Client interface {
	Request(context.Context, string, string, json.RawMessage) (*Response, error)
}

// Session contains the raw API client and non-secret resolved profile metadata.
type Session struct {
	Client  Client
	Profile string
}

// Printer renders raw request plans and responses.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps path, file, credential, and transport behavior injectable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	ReadFile    func(string) ([]byte, error)
	OpenSession func(profile string) (*Session, error)
}

// Response preserves an arbitrary JSON response body without projecting it
// into an endpoint-specific schema.
type Response struct {
	Method     string          `json:"method"`
	Path       string          `json:"path"`
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// OutputHeaders implements output.RowSource.
func (response Response) OutputHeaders() []string {
	return []string{"METHOD", "PATH", "STATUS", "BODY"}
}

// OutputRows implements output.RowSource.
func (response Response) OutputRows() [][]string {
	status := fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode))
	return [][]string{{
		response.Method,
		response.Path,
		strings.TrimSpace(status),
		string(response.Body),
	}}
}

type requestPlan struct {
	Method string       `json:"method"`
	Path   string       `json:"path"`
	File   string       `json:"file,omitempty"`
	DryRun bool         `json:"dryRun"`
	Plan   *shared.Plan `json:"plan"`
}

func (plan requestPlan) OutputHeaders() []string {
	return []string{"METHOD", "PATH", "FILE", "STATUS"}
}

func (plan requestPlan) OutputRows() [][]string {
	return [][]string{{plan.Method, plan.Path, plan.File, "planned"}}
}

type requestOptions struct {
	Method  string
	Path    string
	File    string
	Profile string
	Output  string
	Mode    shared.MutationMode
}

// DefaultDependencies creates production dependencies without resolving
// credentials until method, path, output, confirmation, and body are valid.
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
		ReadFile: readRegularJSONFile,
		OpenSession: func(profile string) (*Session, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			if active == nil || active.Client == nil {
				return nil, errors.New("open Galaxy Store session: client is nil")
			}
			return &Session{
				Client:  samsungClient{client: active.Client},
				Profile: active.Profile,
			}, nil
		},
	}, nil
}

// NewCommand creates the gsc api command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	request := newRequestCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "api",
		ShortUsage:  "gsc api request [flags]",
		ShortHelp:   "Call a documented Galaxy Store Developer API path safely.",
		Subcommands: []*ffcli.Command{request},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("api requires the request command")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newRequestCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("api request", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var options requestOptions
	flags.StringVar(&options.Method, "method", "", "Required HTTP method: GET, HEAD, POST, PUT, PATCH, or DELETE")
	flags.StringVar(&options.Path, "path", "", "Required relative Samsung Developer API path, beginning with /")
	flags.StringVar(&options.File, "file", "", "Optional regular file containing one JSON request value")
	flags.StringVar(&options.Profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&options.Output, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&options.Mode.Confirm, "confirm", false, "Confirm a POST, PUT, PATCH, or DELETE request")
	flags.BoolVar(&options.Mode.DryRun, "dry-run", false, "Validate and print the request plan without opening a session")

	command := &ffcli.Command{
		Name:       "request",
		ShortUsage: "gsc api request --method METHOD --path PATH [--file JSON] [--profile NAME] [--output FORMAT] [--dry-run | --confirm]",
		ShortHelp:  "Send one authenticated request to an allowlisted Samsung API path.",
		LongHelp: `Send one authenticated request to an allowlisted Samsung API path.

Only relative paths under Samsung's documented /seller, /iap, /gss, and token
validation/revocation endpoints are accepted. Authentication headers are always
resolved by gsc and cannot be overridden. Mutating requests require --confirm
and are never retried.

Examples:
  gsc api request --method GET --path '/seller/contentInfo?contentId=000007654321'
  gsc api request --method POST --path /gss/query/contentMetric --file query.json --confirm
  gsc api request --method PATCH --path /iap/v6/applications/com.example/items --file update.json --dry-run`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("api request does not accept positional arguments")
			}
			return runRequest(ctx, dependencies, options)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func runRequest(ctx context.Context, dependencies Dependencies, options requestOptions) error {
	method, mutating, err := normalizeMethod(options.Method)
	if err != nil {
		return err
	}
	path, err := validatePath(options.Path)
	if err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if dependencies.Printer == nil {
		return errors.New("api command output printer is not configured")
	}

	file := strings.TrimSpace(options.File)
	if file != options.File {
		return shared.UsageErrorf("--file must not have surrounding whitespace")
	}
	var body json.RawMessage
	if file != "" {
		if dependencies.ReadFile == nil {
			return errors.New("api command file reader is not configured")
		}
		data, readErr := dependencies.ReadFile(file)
		if readErr != nil {
			return fmt.Errorf("read --file: %w", readErr)
		}
		if len(data) > maximumRequestFileSize {
			return shared.UsageErrorf("--file exceeds the %d-byte input limit", maximumRequestFileSize)
		}
		if !json.Valid(data) {
			return shared.UsageErrorf("--file must contain exactly one valid JSON value")
		}
		body = append(json.RawMessage(nil), data...)
	}

	action := strings.ToLower(method) + " " + path
	if mutating {
		if err := options.Mode.RequireConfirmation(action); err != nil {
			return err
		}
	}
	if options.Mode.DryRun {
		return dependencies.Printer.Print(format, requestPlan{
			Method: method,
			Path:   path,
			File:   file,
			DryRun: true,
			Plan: &shared.Plan{
				Operations: []shared.Operation{{
					Action:   strings.ToLower(method),
					Resource: path,
					Details:  requestDetails(file),
				}},
				Warnings:             requestWarnings(mutating),
				RequiresConfirmation: mutating,
				MutationsPerformed:   false,
			},
		})
	}

	if dependencies.OpenSession == nil {
		return errors.New("api command session factory is not configured")
	}
	active, err := dependencies.OpenSession(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if active == nil || active.Client == nil {
		return errors.New("open Galaxy Store session: raw API client is nil")
	}
	response, err := active.Client.Request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("samsung returned an invalid raw API response")
	}
	response.Method = method
	response.Path = path
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("samsung raw API client returned a non-success response without an error")
	}
	return dependencies.Printer.Print(format, *response)
}

func normalizeMethod(value string) (string, bool, error) {
	method := strings.ToUpper(strings.TrimSpace(value))
	switch method {
	case http.MethodGet, http.MethodHead:
		return method, false, nil
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return method, true, nil
	case "":
		return "", false, shared.UsageErrorf("--method is required")
	default:
		return "", false, shared.UsageErrorf(
			"unsupported --method %q: use GET, HEAD, POST, PUT, PATCH, or DELETE",
			value,
		)
	}
}

func validatePath(value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		return "", shared.UsageErrorf("--path is required")
	}
	if path != value {
		return "", shared.UsageErrorf("--path must not have surrounding whitespace")
	}
	if strings.ContainsAny(path, "\r\n\x00") {
		return "", shared.UsageErrorf("--path contains unsupported control characters")
	}

	parsed, err := url.Parse(path)
	if err != nil {
		return "", shared.UsageErrorf("--path is invalid")
	}
	if parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" ||
		!strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", shared.UsageErrorf("--path must be a relative Samsung API path without a host or fragment")
	}
	if strings.ContainsAny(parsed.Path, "\r\n\x00") {
		return "", shared.UsageErrorf("--path contains unsupported encoded control characters")
	}
	if parsed.Path == "" || containsDotSegment(parsed.EscapedPath()) {
		return "", shared.UsageErrorf("--path must not contain dot segments")
	}
	if !allowedDeveloperPath(parsed.Path) {
		return "", shared.UsageErrorf("--path is not an allowlisted Galaxy Store Developer API path")
	}
	if containsCredentialQuery(parsed.Query()) {
		return "", shared.UsageErrorf("--path must not override authentication through query parameters")
	}
	return path, nil
}

func allowedDeveloperPath(path string) bool {
	for _, prefix := range []string{"/seller/", "/iap/", "/gss/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	switch path {
	case "/auth/checkAccessToken", "/auth/revokeAccessToken":
		return true
	default:
		return false
	}
}

func containsDotSegment(escapedPath string) bool {
	for _, segment := range strings.Split(escapedPath, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return true
		}
		if decoded == "." || decoded == ".." || strings.Contains(decoded, "/") || strings.Contains(decoded, `\`) {
			return true
		}
	}
	return false
}

func containsCredentialQuery(query url.Values) bool {
	for key := range query {
		switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", "")) {
		case "authorization", "accesstoken", "serviceaccountid", "privatekey", "jwt":
			return true
		}
	}
	return false
}

func requestDetails(file string) string {
	if file == "" {
		return "send an authenticated request without a JSON body"
	}
	return "send the validated JSON value from " + file
}

func requestWarnings(mutating bool) []string {
	if !mutating {
		return nil
	}
	return []string{"POST, PUT, PATCH, and DELETE requests are sent once and are never retried."}
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

type samsungClient struct {
	client *samsung.Client
}

func (client samsungClient) Request(
	ctx context.Context,
	method string,
	path string,
	body json.RawMessage,
) (*Response, error) {
	if client.client == nil {
		return nil, errors.New("api client is nil")
	}
	var input any
	if len(body) != 0 {
		input = body
	}
	var responseBody json.RawMessage
	httpResponse, err := client.client.DoJSON(ctx, method, path, input, &responseBody)
	if err != nil {
		return nil, err
	}
	if httpResponse == nil {
		return nil, errors.New("samsung returned no HTTP response")
	}
	return &Response{
		StatusCode: httpResponse.StatusCode,
		Body:       responseBody,
	}, nil
}

func readRegularJSONFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect input file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input file must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("input file must be a regular file")
	}

	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect open input file: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("input file changed while it was being opened")
	}

	data, err := io.ReadAll(io.LimitReader(file, maximumRequestFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read input file: %w", err)
	}
	if len(data) > maximumRequestFileSize {
		return nil, fmt.Errorf("input file exceeds %d bytes", maximumRequestFileSize)
	}
	return data, nil
}

var (
	_ output.RowSource = Response{}
	_ output.RowSource = requestPlan{}
)
