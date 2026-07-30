package reviewscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/reviews"
)

type fakeService struct {
	listResults []*reviews.ListResult
	listErr     error
	listCalls   []reviews.ListOptions

	replyResult *reviews.MutationResult
	replyErr    error
	replyCalls  []reviews.ReplyRequest

	deleteResult *reviews.MutationResult
	deleteErr    error
	deleteCalls  []reviews.DeleteReplyRequest
}

func (service *fakeService) List(
	_ context.Context,
	options reviews.ListOptions,
) (*reviews.ListResult, error) {
	service.listCalls = append(service.listCalls, options)
	if service.listErr != nil {
		return nil, service.listErr
	}
	if len(service.listResults) == 0 {
		return nil, nil
	}
	index := min(len(service.listCalls)-1, len(service.listResults)-1)
	return service.listResults[index], nil
}

func (service *fakeService) Reply(
	_ context.Context,
	request reviews.ReplyRequest,
) (*reviews.MutationResult, error) {
	service.replyCalls = append(service.replyCalls, request)
	return service.replyResult, service.replyErr
}

func (service *fakeService) DeleteReply(
	_ context.Context,
	request reviews.DeleteReplyRequest,
) (*reviews.MutationResult, error) {
	service.deleteCalls = append(service.deleteCalls, request)
	return service.deleteResult, service.deleteErr
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "reviews" || len(command.Subcommands) != 2 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "list" ||
		command.Subcommands[1].Name != "reply" ||
		len(command.Subcommands[1].Subcommands) != 1 ||
		command.Subcommands[1].Subcommands[0].Name != "delete" {
		t.Fatalf("subcommands = %#v", command.Subcommands)
	}
}

func TestListPassesProfilePaginationAndFilterAndPreservesJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{listResults: []*reviews.ListResult{
		rawList(t, `{
			"resultCode":"0000",
			"resultMessage":"Ok",
			"futureTop":{"kept":1},
			"data":{
				"contentId":"000005021191",
				"totalCount":1,
				"pageNo":2,
				"totalPage":2,
				"comments":[{
					"commentId":"5501585",
					"countryCode":"USA",
					"buyerId":"adzc**",
					"rating":8,
					"date":"2025-11-04",
					"commentText":"Four stars",
					"countryName":"USA",
					"device":"Galaxy",
					"futureComment":true
				}]
			}
		}`),
	}}
	var openedProfile string
	dependencies := testDependencies(&stdout, service, &openedProfile)
	err := execute(
		NewCommand(dependencies),
		"list",
		"--profile", "production",
		"--content-id", "000005021191",
		"--comment-id", "5501585",
		"--page", "2",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if openedProfile != "production" {
		t.Fatalf("profile = %q", openedProfile)
	}
	if got, want := service.listCalls, []reviews.ListOptions{{
		ContentID: "000005021191",
		CommentID: "5501585",
		Page:      2,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list calls = %#v, want %#v", got, want)
	}
	for _, expected := range []string{
		`"futureTop":{"kept":1}`,
		`"futureComment":true`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("lossless JSON missing %s: %s", expected, stdout.String())
		}
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "[") {
		t.Fatalf("single-page JSON unexpectedly wrapped in array: %s", stdout.String())
	}
}

func TestListPaginateFetchesEveryPageAndPreservesEachRawResponse(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{listResults: []*reviews.ListResult{
		rawList(t, `{"resultCode":"0000","resultMessage":"Ok","pageExtension":"one","data":{"contentId":"000005021191","totalCount":2,"pageNo":1,"totalPage":2,"comments":[{"commentId":"5501581","countryCode":"USA","buyerId":"a**","rating":10,"date":"2026-01-01","commentText":"One","countryName":"USA","device":"Galaxy"}]}}`),
		rawList(t, `{"resultCode":"0000","resultMessage":"Ok","pageExtension":"two","data":{"contentId":"000005021191","totalCount":2,"pageNo":2,"totalPage":2,"comments":[{"commentId":"5501582","countryCode":"KOR","buyerId":"b**","rating":8,"date":"2026-01-02","commentText":"Two","countryName":"Korea","device":"Galaxy"}]}}`),
	}}
	dependencies := testDependencies(&stdout, service, nil)
	err := execute(
		NewCommand(dependencies),
		"list",
		"--content-id", "000005021191",
		"--paginate",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, want := service.listCalls, []reviews.ListOptions{
		{ContentID: "000005021191", Page: 1},
		{ContentID: "000005021191", Page: 2},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list calls = %#v, want %#v", got, want)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "[") ||
		!strings.Contains(stdout.String(), `"pageExtension":"one"`) ||
		!strings.Contains(stdout.String(), `"pageExtension":"two"`) {
		t.Fatalf("paginated JSON = %s", stdout.String())
	}
}

func TestListTableAndMarkdownHaveUsefulReviewColumns(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"table", "markdown"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			rating := 9
			service := &fakeService{listResults: []*reviews.ListResult{{
				Data: reviews.CommentPage{
					ContentID: "000005021191",
					Comments: []reviews.Comment{{
						CommentID:   "5501585",
						CountryCode: "USA",
						BuyerID:     "a**",
						Rating:      &rating,
						Date:        "2026-01-01",
						CommentText: "Useful | feedback\nline",
						AppVersion:  "2.0",
						ReplyID:     "252",
						ReplyText:   "Thanks",
					}},
				},
			}}}
			dependencies := testDependencies(&stdout, service, nil)
			err := execute(
				NewCommand(dependencies),
				"list",
				"--content-id", "000005021191",
				"--output", format,
			)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			for _, expected := range []string{
				"COMMENT ID",
				"RATING",
				"COUNTRY",
				"COMMENT",
				"5501585",
				"4.5/5",
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("%s missing %q:\n%s", format, expected, stdout.String())
				}
			}
		})
	}
}

func TestReplyDryRunPrintsPlanWithoutOpeningSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var openCalls int
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		OpenService: func(string) (Service, error) {
			openCalls++
			return nil, errors.New("must not open")
		},
	}
	err := execute(
		NewCommand(dependencies),
		"reply",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--country-code", "USA",
		"--body", "Thank you",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("reply dry-run: %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("open calls = %d, want 0", openCalls)
	}
	for _, expected := range []string{
		`"action":"reply"`,
		`"requiresConfirmation":true`,
		`"mutationsPerformed":false`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("plan missing %s: %s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Thank you") {
		t.Fatalf("dry-run unexpectedly echoed reply body: %s", stdout.String())
	}
}

func TestReplyRequiresConfirmationBeforeSessionAndSendsExactRequest(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		replyResult: rawMutation(t, `{
			"resultCode":"0000",
			"resultMessage":"Ok",
			"futureResult":"kept"
		}`),
	}
	var stdout bytes.Buffer
	var openCalls int
	dependencies := testDependencies(&stdout, service, nil)
	originalOpen := dependencies.OpenService
	dependencies.OpenService = func(profile string) (Service, error) {
		openCalls++
		return originalOpen(profile)
	}
	baseArgs := []string{
		"reply",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--country-code", "KOR",
		"--body", "감사합니다",
		"--output", "json",
	}
	err := execute(NewCommand(dependencies), baseArgs...)
	if !errors.Is(err, shared.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want confirmation error", err)
	}
	if openCalls != 0 || len(service.replyCalls) != 0 {
		t.Fatalf("open calls = %d, reply calls = %d", openCalls, len(service.replyCalls))
	}

	err = execute(NewCommand(dependencies), append(baseArgs, "--confirm")...)
	if err != nil {
		t.Fatalf("confirmed reply: %v", err)
	}
	if openCalls != 1 {
		t.Fatalf("open calls = %d, want 1", openCalls)
	}
	if got, want := service.replyCalls, []reviews.ReplyRequest{{
		ContentID:   "000005021191",
		CommentID:   "5501581",
		CountryCode: "KOR",
		ReplyText:   "감사합니다",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reply calls = %#v, want %#v", got, want)
	}
	if !strings.Contains(stdout.String(), `"futureResult":"kept"`) {
		t.Fatalf("lossless result JSON = %s", stdout.String())
	}
}

func TestDeleteReplyDryRunDoesNotResolveOrMutate(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{}
	dependencies := testDependencies(&stdout, service, nil)
	err := execute(
		NewCommand(dependencies),
		"reply", "delete",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("delete dry-run: %v", err)
	}
	if len(service.listCalls) != 0 || len(service.deleteCalls) != 0 {
		t.Fatalf("list calls = %d, delete calls = %d", len(service.listCalls), len(service.deleteCalls))
	}
	if !strings.Contains(stdout.String(), `"action":"resolve"`) ||
		!strings.Contains(stdout.String(), `"action":"delete"`) {
		t.Fatalf("delete plan = %s", stdout.String())
	}
}

func TestDeleteReplyResolvesReplyIDThenSendsOneDelete(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{
		listResults: []*reviews.ListResult{{
			Data: reviews.CommentPage{
				ContentID: "000005021191",
				Comments: []reviews.Comment{{
					CommentID: "5501581",
					ReplyID:   "252",
				}},
			},
		}},
		deleteResult: rawMutation(t, `{"resultCode":"0000","resultMessage":"Ok","futureDelete":true}`),
	}
	dependencies := testDependencies(&stdout, service, nil)
	err := execute(
		NewCommand(dependencies),
		"reply", "delete",
		"--profile", "production",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, want := service.listCalls, []reviews.ListOptions{{
		ContentID: "000005021191",
		CommentID: "5501581",
		Page:      1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list calls = %#v, want %#v", got, want)
	}
	if got, want := service.deleteCalls, []reviews.DeleteReplyRequest{{
		ReplyID: "252",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete calls = %#v, want %#v", got, want)
	}
	if !strings.Contains(stdout.String(), `"futureDelete":true`) {
		t.Fatalf("lossless delete JSON = %s", stdout.String())
	}
}

func TestDeleteReplyStopsWhenCommentHasNoReply(t *testing.T) {
	t.Parallel()

	service := &fakeService{listResults: []*reviews.ListResult{{
		Data: reviews.CommentPage{
			ContentID: "000005021191",
			Comments: []reviews.Comment{{
				CommentID: "5501581",
			}},
		},
	}}}
	dependencies := testDependencies(io.Discard, service, nil)
	err := execute(
		NewCommand(dependencies),
		"reply", "delete",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--confirm",
	)
	if err == nil || !strings.Contains(err.Error(), "no seller reply") {
		t.Fatalf("error = %v, want no-reply error", err)
	}
	if len(service.deleteCalls) != 0 {
		t.Fatalf("delete calls = %d, want 0", len(service.deleteCalls))
	}
}

func TestValidationRunsBeforeSessionOrNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "list positional", args: []string{"list", "--content-id", "000005021191", "extra"}},
		{name: "list missing content", args: []string{"list"}},
		{name: "list invalid content", args: []string{"list", "--content-id", "123"}},
		{name: "list invalid comment", args: []string{"list", "--content-id", "000005021191", "--comment-id", "abc"}},
		{name: "list invalid page", args: []string{"list", "--content-id", "000005021191", "--page", "0"}},
		{name: "list invalid pagination combination", args: []string{"list", "--content-id", "000005021191", "--comment-id", "5501581", "--paginate"}},
		{name: "list output", args: []string{"list", "--content-id", "000005021191", "--output", "yaml"}},
		{name: "reply missing comment", args: []string{"reply", "--content-id", "000005021191", "--country-code", "USA", "--body", "Thanks", "--dry-run"}},
		{name: "reply country", args: []string{"reply", "--content-id", "000005021191", "--comment-id", "5501581", "--country-code", "usa", "--body", "Thanks", "--dry-run"}},
		{name: "reply body", args: []string{"reply", "--content-id", "000005021191", "--comment-id", "5501581", "--country-code", "USA", "--body", " ", "--dry-run"}},
		{name: "reply oversized body", args: []string{"reply", "--content-id", "000005021191", "--comment-id", "5501581", "--country-code", "USA", "--body", strings.Repeat("🙂", 351), "--dry-run"}},
		{name: "delete missing comment", args: []string{"reply", "delete", "--content-id", "000005021191", "--dry-run"}},
		{name: "delete positional", args: []string{"reply", "delete", "--content-id", "000005021191", "--comment-id", "5501581", "--dry-run", "extra"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			service := &fakeService{}
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				OpenService: func(string) (Service, error) {
					openCalls++
					return service, nil
				},
			}
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want *shared.UsageError", err, err)
			}
			if openCalls != 0 ||
				len(service.listCalls) != 0 ||
				len(service.replyCalls) != 0 ||
				len(service.deleteCalls) != 0 {
				t.Fatalf(
					"side effects: open=%d list=%d reply=%d delete=%d",
					openCalls,
					len(service.listCalls),
					len(service.replyCalls),
					len(service.deleteCalls),
				)
			}
		})
	}
}

func TestServiceAndPrinterErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("samsung unavailable")
	service := &fakeService{listErr: sentinel}
	dependencies := testDependencies(io.Discard, service, nil)
	if err := execute(
		NewCommand(dependencies),
		"list",
		"--content-id", "000005021191",
	); !errors.Is(err, sentinel) {
		t.Fatalf("list error = %v, want sentinel", err)
	}

	service = &fakeService{
		replyResult: &reviews.MutationResult{ResultCode: "0000"},
	}
	dependencies = testDependencies(io.Discard, service, nil)
	dependencies.Printer = errorPrinter{err: sentinel}
	if err := execute(
		NewCommand(dependencies),
		"reply",
		"--content-id", "000005021191",
		"--comment-id", "5501581",
		"--country-code", "USA",
		"--body", "Thanks",
		"--confirm",
	); !errors.Is(err, sentinel) {
		t.Fatalf("printer error = %v, want sentinel", err)
	}
}

type errorPrinter struct {
	err error
}

func (printer errorPrinter) Print(output.Format, any) error {
	return printer.err
}

func testDependencies(
	stdout io.Writer,
	service Service,
	openedProfile *string,
) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		OpenService: func(profile string) (Service, error) {
			if openedProfile != nil {
				*openedProfile = profile
			}
			return service, nil
		},
	}
}

func rawList(t *testing.T, raw string) *reviews.ListResult {
	t.Helper()
	var result reviews.ListResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return &result
}

func rawMutation(t *testing.T, raw string) *reviews.MutationResult {
	t.Helper()
	var result reviews.MutationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode mutation: %v", err)
	}
	return &result
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
