// Package reviewscmd implements Galaxy Store buyer review commands.
package reviewscmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/reviews"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

const maxReplyBytes = 1400

// Service is the Samsung buyer-comment API used by these commands.
type Service interface {
	List(context.Context, reviews.ListOptions) (*reviews.ListResult, error)
	Reply(context.Context, reviews.ReplyRequest) (*reviews.MutationResult, error)
	DeleteReply(
		context.Context,
		reviews.DeleteReplyRequest,
	) (*reviews.MutationResult, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// Dependencies keeps review commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without resolving
// credentials until a command validates every local argument and confirmation.
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
		Stderr:  stderr,
		Printer: output.NewPrinter(stdout, isTerminal),
		OpenService: func(profile string) (Service, error) {
			active, openErr := factory.Open(profile)
			if openErr != nil {
				return nil, openErr
			}
			service, serviceErr := reviews.New(active.Client)
			if serviceErr != nil {
				return nil, serviceErr
			}
			return service, nil
		},
	}, nil
}

// NewCommand creates the gsc reviews command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	list := newListCommand(dependencies, stderr)
	reply := newReplyCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "reviews",
		ShortUsage:  "gsc reviews <command> [flags]",
		ShortHelp:   "List Galaxy Store buyer comments and manage seller replies.",
		Subcommands: []*ffcli.Command{list, reply},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("reviews requires a command: list or reply")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newListCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("reviews list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var contentID string
	var commentID string
	var page int
	var paginate bool
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&commentID, "comment-id", "", "Return one exact 7-digit buyer comment ID")
	flags.IntVar(&page, "page", 1, "Comment page number to retrieve")
	flags.BoolVar(&paginate, "paginate", false, "Retrieve every page starting at --page")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc reviews list --content-id ID [--comment-id ID] [--page N] [--paginate] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "List buyer comments for one Galaxy Store app.",
		LongHelp: `List buyer comments for one Galaxy Store app.

--comment-id filters for one exact comment. --page selects one Samsung response
page; --paginate retrieves each page and emits a lossless JSON array of the
original page responses.

Examples:
  gsc reviews list --content-id 000005021191
  gsc reviews list --content-id 000005021191 --page 2 --output table
  gsc reviews list --content-id 000005021191 --paginate --output json`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("reviews list does not accept positional arguments")
			}
			return runList(ctx, dependencies, listOptions{
				Profile:   profile,
				ContentID: contentID,
				CommentID: commentID,
				Page:      page,
				Paginate:  paginate,
				Output:    outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newReplyCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("reviews reply", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var contentID string
	var commentID string
	var countryCode string
	var body string
	var confirm bool
	var dryRun bool
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&commentID, "comment-id", "", "Required 7-digit buyer comment ID")
	flags.StringVar(&countryCode, "country-code", "", "Required 3-letter uppercase Galaxy Store country code")
	flags.StringVar(&body, "body", "", "Required seller reply text, up to 1400 UTF-8 bytes")
	flags.BoolVar(&confirm, "confirm", false, "Confirm creation of the seller reply")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and show the reply plan without changing state")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	deleteCommand := newDeleteReplyCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:       "reply",
		ShortUsage: "gsc reviews reply --content-id ID --comment-id ID --country-code CODE --body TEXT [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Add a seller reply to one buyer comment.",
		LongHelp: `Add a seller reply to one buyer comment.

Samsung permits only one reply per comment. To replace a reply, delete it
explicitly first, then create the new reply.

Examples:
  gsc reviews reply --content-id 000005021191 --comment-id 5501581 --country-code USA --body "Thank you" --dry-run
  gsc reviews reply --content-id 000005021191 --comment-id 5501581 --country-code USA --body "Thank you" --confirm`,
		FlagSet:     flags,
		Subcommands: []*ffcli.Command{deleteCommand},
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("reviews reply does not accept positional arguments")
			}
			return runReply(ctx, dependencies, replyOptions{
				Profile:     profile,
				ContentID:   contentID,
				CommentID:   commentID,
				CountryCode: countryCode,
				Body:        body,
				Output:      outputValue,
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

func newDeleteReplyCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("reviews reply delete", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var contentID string
	var commentID string
	var confirm bool
	var dryRun bool
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&contentID, "content-id", "", "Required 12-digit Galaxy Store content ID")
	flags.StringVar(&commentID, "comment-id", "", "Required 7-digit buyer comment ID whose reply will be deleted")
	flags.BoolVar(&confirm, "confirm", false, "Confirm permanent deletion of the seller reply")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and show the deletion plan without changing state")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "delete",
		ShortUsage: "gsc reviews reply delete --content-id ID --comment-id ID [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Delete the seller reply attached to one buyer comment.",
		LongHelp: `Delete the seller reply attached to one buyer comment.

The command resolves Samsung's reply ID by reading the exact buyer comment,
then sends one confirmed delete request. It never retries the mutation.

Examples:
  gsc reviews reply delete --content-id 000005021191 --comment-id 5501581 --dry-run
  gsc reviews reply delete --content-id 000005021191 --comment-id 5501581 --confirm`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("reviews reply delete does not accept positional arguments")
			}
			return runDeleteReply(ctx, dependencies, deleteOptions{
				Profile:   profile,
				ContentID: contentID,
				CommentID: commentID,
				Output:    outputValue,
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

type listOptions struct {
	Profile   string
	ContentID string
	CommentID string
	Page      int
	Paginate  bool
	Output    string
}

type replyOptions struct {
	Profile     string
	ContentID   string
	CommentID   string
	CountryCode string
	Body        string
	Output      string
	Mode        shared.MutationMode
}

type deleteOptions struct {
	Profile   string
	ContentID string
	CommentID string
	Output    string
	Mode      shared.MutationMode
}

func runList(ctx context.Context, dependencies Dependencies, options listOptions) error {
	if err := validateContentAndComment(options.ContentID, options.CommentID, false); err != nil {
		return err
	}
	if options.Page < 1 {
		return shared.UsageErrorf("--page must be 1 or greater")
	}
	if options.Paginate && options.CommentID != "" {
		return shared.UsageErrorf("--paginate cannot be combined with --comment-id")
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies, true); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	pages := make([]*reviews.ListResult, 0, 1)
	for page := options.Page; ; page++ {
		result, listErr := service.List(ctx, reviews.ListOptions{
			ContentID: options.ContentID,
			CommentID: options.CommentID,
			Page:      page,
		})
		if listErr != nil {
			return listErr
		}
		if result == nil {
			return errors.New("samsung returned an invalid buyer comments response")
		}
		if result.Data.ContentID != "" && result.Data.ContentID != options.ContentID {
			return errors.New("samsung returned buyer comments for a different content ID")
		}
		pages = append(pages, result)
		if !options.Paginate || result.Data.TotalPages <= page {
			break
		}
	}
	return dependencies.Printer.Print(format, listOutput{
		Pages:     pages,
		Paginated: options.Paginate,
	})
}

func runReply(ctx context.Context, dependencies Dependencies, options replyOptions) error {
	if err := validateContentAndComment(options.ContentID, options.CommentID, true); err != nil {
		return err
	}
	if err := validateCountryCode(options.CountryCode); err != nil {
		return err
	}
	if err := validateReplyBody(options.Body); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies, false); err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation("add the Galaxy Store buyer comment reply"); err != nil {
		return err
	}

	plan := replyPlan(options.ContentID, options.CommentID, options.CountryCode)
	if !options.Mode.ShouldExecute() {
		return dependencies.Printer.Print(format, mutationOutput{
			Action:    "reply",
			ContentID: options.ContentID,
			CommentID: options.CommentID,
			Plan:      plan,
		})
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	result, err := service.Reply(ctx, reviews.ReplyRequest{
		ContentID:   options.ContentID,
		CommentID:   options.CommentID,
		CountryCode: options.CountryCode,
		ReplyText:   options.Body,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid buyer comment reply response")
	}
	return dependencies.Printer.Print(format, mutationOutput{
		Action:    "reply",
		ContentID: options.ContentID,
		CommentID: options.CommentID,
		Result:    result,
	})
}

func runDeleteReply(
	ctx context.Context,
	dependencies Dependencies,
	options deleteOptions,
) error {
	if err := validateContentAndComment(options.ContentID, options.CommentID, true); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies, false); err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation("permanently delete the Galaxy Store buyer comment reply"); err != nil {
		return err
	}

	plan := deleteReplyPlan(options.ContentID, options.CommentID)
	if !options.Mode.ShouldExecute() {
		return dependencies.Printer.Print(format, mutationOutput{
			Action:    "delete reply",
			ContentID: options.ContentID,
			CommentID: options.CommentID,
			Plan:      plan,
		})
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	view, err := service.List(ctx, reviews.ListOptions{
		ContentID: options.ContentID,
		CommentID: options.CommentID,
		Page:      1,
	})
	if err != nil {
		return fmt.Errorf("resolve Galaxy Store buyer comment reply: %w", err)
	}
	if view == nil {
		return errors.New("resolve Galaxy Store buyer comment reply: Samsung returned an invalid response")
	}
	replyID := ""
	for _, comment := range view.Data.Comments {
		if comment.CommentID == options.CommentID {
			replyID = comment.ReplyID
			break
		}
	}
	if replyID == "" {
		return fmt.Errorf(
			"buyer comment %s has no seller reply to delete",
			options.CommentID,
		)
	}

	result, err := service.DeleteReply(
		ctx,
		reviews.DeleteReplyRequest{ReplyID: replyID},
	)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid buyer comment reply deletion response")
	}
	return dependencies.Printer.Print(format, mutationOutput{
		Action:    "delete reply",
		ContentID: options.ContentID,
		CommentID: options.CommentID,
		ReplyID:   replyID,
		Result:    result,
	})
}

type listOutput struct {
	Pages     []*reviews.ListResult
	Paginated bool
}

// MarshalJSON keeps a single page identical to Samsung's response. Explicit
// pagination emits an array of untouched page responses so unknown fields from
// every page remain available to automation.
func (result listOutput) MarshalJSON() ([]byte, error) {
	if len(result.Pages) == 1 && !result.Paginated {
		return json.Marshal(result.Pages[0])
	}
	return json.Marshal(result.Pages)
}

func (result listOutput) OutputHeaders() []string {
	return []string{
		"COMMENT ID",
		"RATING",
		"COUNTRY",
		"BUYER",
		"DATE",
		"APP VERSION",
		"REPLY ID",
		"COMMENT",
		"REPLY",
	}
}

func (result listOutput) OutputRows() [][]string {
	var rows [][]string
	for _, page := range result.Pages {
		if page == nil {
			continue
		}
		for _, comment := range page.Data.Comments {
			rating := ""
			if comment.Rating != nil {
				rating = fmt.Sprintf("%.1f/5", float64(*comment.Rating)/2)
			}
			rows = append(rows, []string{
				comment.CommentID,
				rating,
				comment.CountryCode,
				comment.BuyerID,
				comment.Date,
				comment.AppVersion,
				comment.ReplyID,
				comment.CommentText,
				comment.ReplyText,
			})
		}
	}
	return rows
}

type mutationOutput struct {
	Action    string
	ContentID string
	CommentID string
	ReplyID   string
	Result    *reviews.MutationResult
	Plan      *shared.Plan
}

// MarshalJSON emits either the stable dry-run plan or Samsung's untouched
// mutation response.
func (result mutationOutput) MarshalJSON() ([]byte, error) {
	if result.Plan != nil {
		return json.Marshal(result.Plan)
	}
	return json.Marshal(result.Result)
}

func (result mutationOutput) OutputHeaders() []string {
	return []string{
		"ACTION",
		"CONTENT ID",
		"COMMENT ID",
		"REPLY ID",
		"RESULT CODE",
		"RESULT MESSAGE",
		"STATUS",
	}
}

func (result mutationOutput) OutputRows() [][]string {
	resultCode := ""
	resultMessage := ""
	status := "planned"
	if result.Result != nil {
		resultCode = result.Result.ResultCode
		resultMessage = result.Result.ResultMessage
		status = "completed"
	}
	return [][]string{{
		result.Action,
		result.ContentID,
		result.CommentID,
		result.ReplyID,
		resultCode,
		resultMessage,
		status,
	}}
}

func replyPlan(contentID string, commentID string, countryCode string) *shared.Plan {
	return &shared.Plan{
		Operations: []shared.Operation{{
			Action:   "reply",
			Resource: "Galaxy Store buyer comment " + commentID,
			Details:  "add one seller reply for content " + contentID + " in " + countryCode,
		}},
		Warnings: []string{
			"Samsung permits only one seller reply per buyer comment.",
		},
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}
}

func deleteReplyPlan(contentID string, commentID string) *shared.Plan {
	return &shared.Plan{
		Operations: []shared.Operation{
			{
				Action:   "resolve",
				Resource: "Galaxy Store buyer comment " + commentID,
				Details:  "read the comment for content " + contentID + " to resolve its reply ID",
			},
			{
				Action:   "delete",
				Resource: "Galaxy Store seller reply",
				Details:  "permanently delete the resolved reply",
			},
		},
		Warnings: []string{
			"Deleting a seller reply is permanent.",
		},
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}
}

func validateContentAndComment(
	contentID string,
	commentID string,
	requireComment bool,
) error {
	if err := shared.RequireValue("--content-id", contentID); err != nil {
		return err
	}
	if err := shared.ValidateContentID(contentID); err != nil {
		return err
	}
	if requireComment {
		if err := shared.RequireValue("--comment-id", commentID); err != nil {
			return err
		}
	}
	if commentID == "" {
		return nil
	}
	if len(commentID) != 7 {
		return shared.UsageErrorf("comment ID must contain exactly 7 digits")
	}
	for _, character := range commentID {
		if character < '0' || character > '9' {
			return shared.UsageErrorf("comment ID must contain exactly 7 digits")
		}
	}
	return nil
}

func validateCountryCode(value string) error {
	if err := shared.RequireValue("--country-code", value); err != nil {
		return err
	}
	if len(value) != 3 {
		return shared.UsageErrorf("country code must contain exactly 3 uppercase letters")
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return shared.UsageErrorf("country code must contain exactly 3 uppercase letters")
		}
	}
	return nil
}

func validateReplyBody(value string) error {
	if err := shared.RequireValue("--body", value); err != nil {
		return err
	}
	if !utf8.ValidString(value) {
		return shared.UsageErrorf("--body must contain valid UTF-8")
	}
	if len([]byte(value)) > maxReplyBytes {
		return shared.UsageErrorf("--body cannot exceed 1400 UTF-8 bytes")
	}
	return nil
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func openService(dependencies Dependencies, profile string) (Service, error) {
	if dependencies.OpenService == nil {
		return nil, errors.New("reviews command session factory is not configured")
	}
	service, err := dependencies.OpenService(strings.TrimSpace(profile))
	if err != nil {
		return nil, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return nil, errors.New("open Galaxy Store session: reviews service is nil")
	}
	return service, nil
}

func validateDependencies(dependencies Dependencies, requireService bool) error {
	if dependencies.Printer == nil {
		return errors.New("reviews command output printer is not configured")
	}
	if requireService && dependencies.OpenService == nil {
		return errors.New("reviews command session factory is not configured")
	}
	return nil
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var (
	_ json.Marshaler   = listOutput{}
	_ json.Marshaler   = mutationOutput{}
	_ output.RowSource = listOutput{}
	_ output.RowSource = mutationOutput{}
)
