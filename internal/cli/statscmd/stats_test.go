package statscmd

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
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/stats"
)

type fakeService struct {
	sellerResult *stats.Response
	sellerError  error
	sellerCalls  int
	sellerQuery  stats.SellerQuery

	contentResult *stats.Response
	contentError  error
	contentCalls  int
	contentQuery  stats.ContentQuery
}

func (service *fakeService) Seller(
	_ context.Context,
	query stats.SellerQuery,
) (*stats.Response, error) {
	service.sellerCalls++
	service.sellerQuery = query
	return service.sellerResult, service.sellerError
}

func (service *fakeService) Content(
	_ context.Context,
	query stats.ContentQuery,
) (*stats.Response, error) {
	service.contentCalls++
	service.contentQuery = query
	return service.contentResult, service.contentError
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	command := NewCommand(Dependencies{})
	if command.Name != "stats" || len(command.Subcommands) != 2 {
		t.Fatalf("command = %#v", command)
	}
	if command.Subcommands[0].Name != "seller" ||
		command.Subcommands[1].Name != "content" {
		t.Fatalf(
			"subcommands = %q, %q",
			command.Subcommands[0].Name,
			command.Subcommands[1].Name,
		)
	}
}

func TestSellerReadsFileBeforeSessionAndPassesExactProfileAndQuery(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{sellerResult: &stats.Response{
		MetricIDs: []string{stats.SellerMetricRevenue},
		Data: json.RawMessage(`{
			"periods":[{
				"startDate":"2026-07-01",
				"endDate":"2026-07-07",
				"metricSummaries":{"revenue_total":12.5}
			}]
		}`),
		TrendAggregation: stats.AggregationDay,
	}}
	var events []string
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(path string) ([]byte, error) {
			events = append(events, "read:"+path)
			return []byte(`{
				"metricIds":["revenue_total"],
				"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
				"getDailyMetric":true,
				"getBreakdownsByFilter":true,
				"noContentMetadata":true,
				"filters":{"country":["USA"],"device":["Galaxy S25"]},
				"trendAggregation":"day"
			}`), nil
		},
		OpenService: func(profile string) (Service, error) {
			events = append(events, "open:"+profile)
			return service, nil
		},
	}

	if err := execute(
		NewCommand(dependencies),
		"seller",
		"--file", "seller.json",
		"--profile", "production",
		"--output", "json",
	); err != nil {
		t.Fatalf("seller: %v", err)
	}
	if got, want := events, []string{"read:seller.json", "open:production"}; !equalStrings(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if service.sellerCalls != 1 {
		t.Fatalf("seller calls = %d, want 1", service.sellerCalls)
	}
	wantQuery := stats.SellerQuery{
		MetricIDs:             []string{stats.SellerMetricRevenue},
		Periods:               []stats.Period{{StartDate: "2026-07-01", EndDate: "2026-07-07"}},
		GetDailyMetric:        true,
		GetBreakdownsByFilter: true,
		NoContentMetadata:     true,
		Filters:               stats.Filters{Countries: []string{"USA"}, Devices: []string{"Galaxy S25"}},
		TrendAggregation:      stats.AggregationDay,
	}
	if !equalSellerQuery(service.sellerQuery, wantQuery) {
		t.Fatalf("query = %#v, want %#v", service.sellerQuery, wantQuery)
	}
	for _, expected := range []string{
		`"metricIds":["revenue_total"]`,
		`"metricSummaries":{"revenue_total":12.5}`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("JSON missing %q: %s", expected, stdout.String())
		}
	}
}

//nolint:misspell // Samsung's published metric ID contains this typo.
func TestContentPassesExactQueryAndProducesMarkdownSummary(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{contentResult: &stats.Response{
		ContentID: "000007654321",
		MetricIDs: []string{
			stats.ContentMetricRatingScore,
			stats.ContentMetricRatingVolume,
		},
		Data: json.RawMessage(`{
			"periods":[{
				"startDate":"2026-07-01",
				"endDate":"2026-07-07",
				"000007654321":{
					"daily_rat_score":{"value":40},
					"daily_rat_volumne":{"value":10}
				}
			}]
		}`),
		TrendAggregation: stats.AggregationWeek,
	}}
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{
				"contentId":"000007654321",
				"metricIds":["daily_rat_score","daily_rat_volumne"],
				"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
				"noBreakdown":true,
				"filters":{},
				"trendAggregation":"week"
			}`), nil
		},
		OpenService: func(string) (Service, error) {
			return service, nil
		},
	}

	if err := execute(
		NewCommand(dependencies),
		"content",
		"--file", "content.json",
		"--output", "markdown",
	); err != nil {
		t.Fatalf("content: %v", err)
	}
	if service.contentCalls != 1 ||
		service.contentQuery.ContentID != "000007654321" ||
		!service.contentQuery.NoBreakdown ||
		service.contentQuery.TrendAggregation != stats.AggregationWeek {
		t.Fatalf("content query = %+v", service.contentQuery)
	}
	for _, expected := range []string{
		"| SCOPE | CONTENT ID | START DATE | END DATE | METRIC | VALUE |",
		"| content | 000007654321 | 2026-07-01 | 2026-07-07 | daily_rat_score | 40 |",
		"| content | 000007654321 | 2026-07-01 | 2026-07-07 | daily_rat_volumne | 10 |",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestSellerTableSummarizesPeriodsAndMetricsDeterministically(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{sellerResult: &stats.Response{
		MetricIDs: []string{
			stats.SellerMetricRevenue,
			stats.SellerMetricUniqueInstalls,
		},
		Data: json.RawMessage(`{
			"periods":[{
				"startDate":"2026-07-01",
				"endDate":"2026-07-07",
				"metricSummaries":{
					"total_unique_installs_filter":22,
					"revenue_total":12.5
				}
			}]
		}`),
	}}
	dependencies := testDependencies(
		&stdout,
		service,
		`{
			"metricIds":["revenue_total","total_unique_installs_filter"],
			"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
			"filters":{},
			"trendAggregation":"day"
		}`,
	)

	if err := execute(
		NewCommand(dependencies),
		"seller",
		"--file", "seller.json",
		"--output", "table",
	); err != nil {
		t.Fatalf("seller: %v", err)
	}
	outputText := stdout.String()
	revenueIndex := strings.Index(outputText, "revenue_total")
	installsIndex := strings.Index(outputText, "total_unique_installs_filter")
	if revenueIndex < 0 || installsIndex < 0 || revenueIndex > installsIndex {
		t.Fatalf("metric rows are missing or unordered:\n%s", outputText)
	}
	for _, expected := range []string{
		"SCOPE",
		"START DATE",
		"seller",
		"12.5",
		"22",
	} {
		if !strings.Contains(outputText, expected) {
			t.Fatalf("table missing %q:\n%s", expected, outputText)
		}
	}
}

func TestValidationAndFileParsingRunBeforeSession(t *testing.T) {
	t.Parallel()

	validSeller := `{
		"metricIds":["revenue_total"],
		"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
		"filters":{},
		"trendAggregation":"day"
	}`
	validContent := `{
		"contentId":"000007654321",
		"metricIds":["revenue_total"],
		"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
		"filters":{},
		"trendAggregation":"day"
	}`
	tests := []struct {
		name     string
		args     []string
		fileData string
	}{
		{name: "seller positional", args: []string{"seller", "--file", "request.json", "extra"}, fileData: validSeller},
		{name: "seller missing file", args: []string{"seller"}, fileData: validSeller},
		{name: "seller padded file", args: []string{"seller", "--file", " request.json "}, fileData: validSeller},
		{name: "seller bad output", args: []string{"seller", "--file", "request.json", "--output", "yaml"}, fileData: validSeller},
		{name: "seller invalid JSON", args: []string{"seller", "--file", "request.json"}, fileData: `{`},
		{name: "seller extra JSON", args: []string{"seller", "--file", "request.json"}, fileData: validSeller + `{}`},
		{name: "seller unknown field", args: []string{"seller", "--file", "request.json"}, fileData: `{"unexpected":true}`},
		{name: "seller unsupported metric", args: []string{"seller", "--file", "request.json"}, fileData: strings.Replace(validSeller, "revenue_total", "daily_rat_score", 1)},
		{name: "seller reversed dates", args: []string{"seller", "--file", "request.json"}, fileData: strings.Replace(validSeller, `"2026-07-01","endDate":"2026-07-07"`, `"2026-07-08","endDate":"2026-07-07"`, 1)},
		{name: "seller invalid aggregation", args: []string{"seller", "--file", "request.json"}, fileData: strings.Replace(validSeller, `"day"`, `"hour"`, 1)},
		{name: "content invalid ID", args: []string{"content", "--file", "request.json"}, fileData: strings.Replace(validContent, "000007654321", "invalid", 1)},
		{name: "content unsupported metric", args: []string{"content", "--file", "request.json"}, fileData: strings.Replace(validContent, "revenue_total", "revenue_item", 1)},
		{name: "content bad filter", args: []string{"content", "--file", "request.json"}, fileData: strings.Replace(validContent, `"filters":{}`, `"filters":{"device":[""]}`, 1)},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var readCalls int
			var openCalls int
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				ReadFile: func(string) ([]byte, error) {
					readCalls++
					return []byte(test.fileData), nil
				},
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(NewCommand(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want *shared.UsageError", err, err)
			}
			if openCalls != 0 {
				t.Fatalf("session opened %d times", openCalls)
			}
			if strings.Contains(test.name, "positional") ||
				strings.Contains(test.name, "missing file") ||
				strings.Contains(test.name, "padded file") ||
				strings.Contains(test.name, "bad output") {
				if readCalls != 0 {
					t.Fatalf("local flag validation read file %d times", readCalls)
				}
			} else if readCalls != 1 {
				t.Fatalf("read calls = %d, want 1", readCalls)
			}
		})
	}
}

func TestOversizedAndUnreadableFilesDoNotOpenSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		readFile FileReader
	}{
		{
			name: "oversized",
			readFile: func(string) ([]byte, error) {
				return bytes.Repeat([]byte{'x'}, maxRequestFileBytes+1), nil
			},
		},
		{
			name: "unreadable",
			readFile: func(string) ([]byte, error) {
				return nil, errors.New("permission denied")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			dependencies := Dependencies{
				Printer:  output.NewPrinter(io.Discard, nil),
				ReadFile: test.readFile,
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(
				NewCommand(dependencies),
				"seller",
				"--file", "request.json",
			)
			if err == nil {
				t.Fatal("expected file error")
			}
			if openCalls != 0 {
				t.Fatalf("session opened %d times", openCalls)
			}
		})
	}
}

func TestServiceAndPrinterErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("samsung unavailable")
	service := &fakeService{sellerError: sentinel}
	dependencies := testDependencies(
		io.Discard,
		service,
		`{
			"metricIds":["revenue_total"],
			"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
			"filters":{},
			"trendAggregation":"day"
		}`,
	)
	if err := execute(
		NewCommand(dependencies),
		"seller",
		"--file", "request.json",
	); !errors.Is(err, sentinel) {
		t.Fatalf("seller error = %v, want sentinel", err)
	}

	service = &fakeService{contentResult: &stats.Response{
		ContentID: "000007654321",
		MetricIDs: []string{stats.ContentMetricRevenue},
		Data:      json.RawMessage(`{"periods":[]}`),
	}}
	dependencies = testDependencies(
		io.Discard,
		service,
		`{
			"contentId":"000007654321",
			"metricIds":["revenue_total"],
			"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],
			"filters":{},
			"trendAggregation":"day"
		}`,
	)
	dependencies.Printer = errorPrinter{err: sentinel}
	if err := execute(
		NewCommand(dependencies),
		"content",
		"--file", "request.json",
	); !errors.Is(err, sentinel) {
		t.Fatalf("content error = %v, want sentinel", err)
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
	fileData string,
) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		ReadFile: func(string) ([]byte, error) {
			return []byte(fileData), nil
		},
		OpenService: func(string) (Service, error) {
			return service, nil
		},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalSellerQuery(left, right stats.SellerQuery) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
