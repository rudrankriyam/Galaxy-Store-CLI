// Package statscmd implements Galaxy Store Statistics commands.
package statscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/stats"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
)

const maxRequestFileBytes = 1 << 20

var (
	sellerMetricIDs = map[string]struct{}{
		stats.SellerMetricUniqueInstalls:  {},
		stats.SellerMetricRevenue:         {},
		stats.SellerMetricDeviceDownloads: {},
		stats.SellerMetricItemRevenue:     {},
	}
	contentMetricIDs = map[string]struct{}{
		stats.ContentMetricUniqueInstalls: {},
		stats.ContentMetricRevenue:        {},
		stats.ContentMetricIAPOrders:      {},
		stats.ContentMetricRatingScore:    {},
		stats.ContentMetricRatingVolume:   {},
	}
)

// Service is the read-only GSS API used by commands.
type Service interface {
	Seller(context.Context, stats.SellerQuery) (*stats.Response, error)
	Content(context.Context, stats.ContentQuery) (*stats.Response, error)
}

// Printer renders command results.
type Printer interface {
	Print(output.Format, any) error
}

// FileReader reads a local GSS request file.
type FileReader func(path string) ([]byte, error)

// Dependencies keeps statistics commands deterministic and testable.
type Dependencies struct {
	Stderr      io.Writer
	Printer     Printer
	ReadFile    FileReader
	OpenService func(profile string) (Service, error)
}

// DefaultDependencies creates production dependencies without reading a file
// or resolving credentials until the command has validated local flags.
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
			service, serviceErr := stats.New(active.Client)
			if serviceErr != nil {
				return nil, serviceErr
			}
			return service, nil
		},
	}, nil
}

// NewCommand creates the gsc stats command group.
func NewCommand(dependencies Dependencies) *ffcli.Command {
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	seller := newSellerCommand(dependencies, stderr)
	content := newContentCommand(dependencies, stderr)
	command := &ffcli.Command{
		Name:        "stats",
		ShortUsage:  "gsc stats <command> [flags]",
		ShortHelp:   "Query Galaxy Store seller and app statistics.",
		Subcommands: []*ffcli.Command{seller, content},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("stats requires a command: seller or content")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newSellerCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("stats seller", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var file string
	var profile string
	var outputValue string
	flags.StringVar(&file, "file", "", "Required JSON seller-metric request file")
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "seller",
		ShortUsage: "gsc stats seller --file FILE [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Query statistics across all apps owned by the seller.",
		LongHelp: `Query statistics across all apps owned by the seller.

The JSON file uses Samsung's sellerMetric fields: metricIds, periods,
getDailyMetric, getBreakdownsByFilter, noContentMetadata, filters, and
trendAggregation.

Examples:
  gsc stats seller --file seller-metrics.json
  gsc stats seller --file seller-metrics.json --profile production --output table`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("stats seller does not accept positional arguments")
			}
			return runSeller(ctx, dependencies, commandOptions{
				File:    file,
				Profile: profile,
				Output:  outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newContentCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("stats content", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var file string
	var profile string
	var outputValue string
	flags.StringVar(&file, "file", "", "Required JSON content-metric request file")
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "content",
		ShortUsage: "gsc stats content --file FILE [--profile NAME] [--output FORMAT]",
		ShortHelp:  "Query statistics for one Galaxy Store app.",
		LongHelp: `Query statistics for one Galaxy Store app.

The JSON file uses Samsung's contentMetric fields: contentId, metricIds,
periods, noBreakdown, filters, and trendAggregation.

Examples:
  gsc stats content --file content-metrics.json
  gsc stats content --file content-metrics.json --output markdown`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("stats content does not accept positional arguments")
			}
			return runContent(ctx, dependencies, commandOptions{
				File:    file,
				Profile: profile,
				Output:  outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

type commandOptions struct {
	File    string
	Profile string
	Output  string
}

type sellerFile struct {
	MetricIDs             []string       `json:"metricIds"`
	Periods               []stats.Period `json:"periods"`
	GetDailyMetric        bool           `json:"getDailyMetric"`
	GetBreakdownsByFilter bool           `json:"getBreakdownsByFilter"`
	NoContentMetadata     bool           `json:"noContentMetadata"`
	Filters               stats.Filters  `json:"filters"`
	TrendAggregation      string         `json:"trendAggregation"`
}

type contentFile struct {
	ContentID        string         `json:"contentId"`
	MetricIDs        []string       `json:"metricIds"`
	Periods          []stats.Period `json:"periods"`
	NoBreakdown      bool           `json:"noBreakdown"`
	Filters          stats.Filters  `json:"filters"`
	TrendAggregation string         `json:"trendAggregation"`
}

func runSeller(ctx context.Context, dependencies Dependencies, options commandOptions) error {
	file, format, err := validateLocalOptions(dependencies, options)
	if err != nil {
		return err
	}
	var input sellerFile
	if err := readRequestFile(dependencies.ReadFile, file, &input); err != nil {
		return err
	}
	query := stats.SellerQuery{
		MetricIDs:             input.MetricIDs,
		Periods:               input.Periods,
		GetDailyMetric:        input.GetDailyMetric,
		GetBreakdownsByFilter: input.GetBreakdownsByFilter,
		NoContentMetadata:     input.NoContentMetadata,
		Filters:               input.Filters,
		TrendAggregation:      input.TrendAggregation,
	}
	if err := validateQuery(query.MetricIDs, sellerMetricIDs, query.Periods, query.Filters, query.TrendAggregation); err != nil {
		return err
	}

	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store GSS session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store GSS session: statistics service is nil")
	}
	response, err := service.Seller(ctx, query)
	if err != nil {
		return err
	}
	result, err := newStatsResult(response, "seller", "", query.MetricIDs)
	if err != nil {
		return err
	}
	return dependencies.Printer.Print(format, result)
}

func runContent(ctx context.Context, dependencies Dependencies, options commandOptions) error {
	file, format, err := validateLocalOptions(dependencies, options)
	if err != nil {
		return err
	}
	var input contentFile
	if err := readRequestFile(dependencies.ReadFile, file, &input); err != nil {
		return err
	}
	if err := shared.ValidateContentID(input.ContentID); err != nil {
		return err
	}
	query := stats.ContentQuery{
		ContentID:        input.ContentID,
		MetricIDs:        input.MetricIDs,
		Periods:          input.Periods,
		NoBreakdown:      input.NoBreakdown,
		Filters:          input.Filters,
		TrendAggregation: input.TrendAggregation,
	}
	if err := validateQuery(query.MetricIDs, contentMetricIDs, query.Periods, query.Filters, query.TrendAggregation); err != nil {
		return err
	}

	service, err := dependencies.OpenService(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store GSS session: %w", err)
	}
	if service == nil {
		return errors.New("open Galaxy Store GSS session: statistics service is nil")
	}
	response, err := service.Content(ctx, query)
	if err != nil {
		return err
	}
	result, err := newStatsResult(response, "content", query.ContentID, query.MetricIDs)
	if err != nil {
		return err
	}
	return dependencies.Printer.Print(format, result)
}

func validateLocalOptions(
	dependencies Dependencies,
	options commandOptions,
) (string, output.Format, error) {
	if err := shared.RequireValue("--file", options.File); err != nil {
		return "", "", err
	}
	file := strings.TrimSpace(options.File)
	if file != options.File {
		return "", "", shared.UsageErrorf("--file must not contain surrounding whitespace")
	}
	format, err := output.ParseFormat(options.Output)
	if err != nil {
		return "", "", shared.UsageErrorf("%v", err)
	}
	switch {
	case dependencies.Printer == nil:
		return "", "", errors.New("stats command output printer is not configured")
	case dependencies.ReadFile == nil:
		return "", "", errors.New("stats command file reader is not configured")
	case dependencies.OpenService == nil:
		return "", "", errors.New("stats command session factory is not configured")
	default:
		return file, format, nil
	}
}

func readRequestFile(readFile FileReader, path string, target any) error {
	data, err := readFile(path)
	if err != nil {
		return fmt.Errorf("read statistics request file: %w", err)
	}
	if len(data) > maxRequestFileBytes {
		return shared.UsageErrorf("statistics request file must not exceed %d bytes", maxRequestFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return shared.UsageErrorf("invalid statistics request file: %v", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == nil {
		return shared.UsageErrorf("invalid statistics request file: multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return shared.UsageErrorf("invalid statistics request file: %v", err)
	}
	return nil
}

func validateQuery(
	metricIDs []string,
	allowed map[string]struct{},
	periods []stats.Period,
	filters stats.Filters,
	aggregation string,
) error {
	if len(metricIDs) == 0 {
		return shared.UsageErrorf("at least one metric ID is required")
	}
	seenMetrics := make(map[string]struct{}, len(metricIDs))
	for _, metricID := range metricIDs {
		if metricID != strings.TrimSpace(metricID) {
			return shared.UsageErrorf("metric IDs must not contain surrounding whitespace")
		}
		if _, ok := allowed[metricID]; !ok {
			return shared.UsageErrorf("unsupported metric ID")
		}
		if _, ok := seenMetrics[metricID]; ok {
			return shared.UsageErrorf("duplicate metric ID")
		}
		seenMetrics[metricID] = struct{}{}
	}
	if len(periods) == 0 {
		return shared.UsageErrorf("at least one metric period is required")
	}
	for index, period := range periods {
		start, startErr := parseDate(period.StartDate)
		if startErr != nil {
			return shared.UsageErrorf("period %d start date must use YYYY-MM-DD", index+1)
		}
		end, endErr := parseDate(period.EndDate)
		if endErr != nil {
			return shared.UsageErrorf("period %d end date must use YYYY-MM-DD", index+1)
		}
		if start.After(end) {
			return shared.UsageErrorf("period %d start date cannot be after its end date", index+1)
		}
	}
	if aggregation != stats.AggregationDay &&
		aggregation != stats.AggregationWeek &&
		aggregation != stats.AggregationMonth {
		return shared.UsageErrorf("trend aggregation must be day, week, or month")
	}
	if err := validateFilterValues(filters.Countries, "country"); err != nil {
		return err
	}
	return validateFilterValues(filters.Devices, "device")
}

func validateFilterValues(values []string, kind string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return shared.UsageErrorf("%s filter values must be non-empty and have no surrounding whitespace", kind)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return shared.UsageErrorf("%s filter values must not contain control characters", kind)
		}
		if _, ok := seen[value]; ok {
			return shared.UsageErrorf("duplicate %s filter value", kind)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return time.Time{}, errors.New("invalid date")
	}
	return parsed, nil
}

type statsResult struct {
	*stats.Response
	rows [][]string
}

func newStatsResult(
	response *stats.Response,
	scope string,
	contentID string,
	requestedMetricIDs []string,
) (statsResult, error) {
	if response == nil {
		return statsResult{}, errors.New("samsung returned an invalid statistics response")
	}
	metricIDs := response.MetricIDs
	if len(metricIDs) == 0 {
		metricIDs = requestedMetricIDs
	}
	rows, err := metricRows(response.Data, scope, contentID, metricIDs)
	if err != nil {
		return statsResult{}, err
	}
	return statsResult{Response: response, rows: rows}, nil
}

func (result statsResult) OutputHeaders() []string {
	return []string{"SCOPE", "CONTENT ID", "START DATE", "END DATE", "METRIC", "VALUE"}
}

func (result statsResult) OutputRows() [][]string {
	rows := make([][]string, len(result.rows))
	for index, row := range result.rows {
		rows[index] = append([]string(nil), row...)
	}
	return rows
}

func metricRows(
	data json.RawMessage,
	scope string,
	contentID string,
	metricIDs []string,
) ([][]string, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var envelope struct {
		Periods []map[string]json.RawMessage `json:"periods"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("samsung returned invalid statistics data")
	}

	rows := make([][]string, 0, len(envelope.Periods)*len(metricIDs))
	for _, period := range envelope.Periods {
		startDate, err := rawString(period["startDate"])
		if err != nil {
			return nil, errors.New("samsung returned invalid statistics period")
		}
		endDate, err := rawString(period["endDate"])
		if err != nil {
			return nil, errors.New("samsung returned invalid statistics period")
		}
		values, err := periodMetricValues(period, scope, contentID)
		if err != nil {
			return nil, err
		}
		for _, metricID := range metricIDs {
			value, err := rawValue(values[metricID])
			if err != nil {
				return nil, errors.New("samsung returned invalid statistics metric value")
			}
			rows = append(rows, []string{
				scope,
				contentID,
				startDate,
				endDate,
				metricID,
				value,
			})
		}
	}
	return rows, nil
}

func periodMetricValues(
	period map[string]json.RawMessage,
	scope string,
	contentID string,
) (map[string]json.RawMessage, error) {
	if scope == "seller" {
		var summaries map[string]json.RawMessage
		if raw := period["metricSummaries"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &summaries); err != nil {
				return nil, errors.New("samsung returned invalid seller metric summaries")
			}
		}
		return summaries, nil
	}

	var metrics map[string]json.RawMessage
	if raw := period[contentID]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &metrics); err != nil {
			return nil, errors.New("samsung returned invalid content metrics")
		}
	}
	values := make(map[string]json.RawMessage, len(metrics))
	for metricID, rawMetric := range metrics {
		var metric struct {
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(rawMetric, &metric); err != nil {
			return nil, errors.New("samsung returned invalid content metric")
		}
		values[metricID] = metric.Value
	}
	return values, nil
}

func rawString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func commandUsage(command *ffcli.Command) string {
	return fmt.Sprintf("Usage:\n  %s\n\n%s\n", command.ShortUsage, command.ShortHelp)
}

var _ output.RowSource = statsResult{}
