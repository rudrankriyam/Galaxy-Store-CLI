// Package stats provides read-only access to Galaxy Store Statistics metrics.
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	sellerMetricPath  = "/gss/query/sellerMetric"
	contentMetricPath = "/gss/query/contentMetric"

	// SellerMetricUniqueInstalls reports first-time installs by device.
	SellerMetricUniqueInstalls = "total_unique_installs_filter"
	// SellerMetricRevenue reports total app and item revenue.
	SellerMetricRevenue = "revenue_total"
	// SellerMetricDeviceDownloads reports downloads including repeat downloads.
	SellerMetricDeviceDownloads = "dn_by_total_dvce"
	// SellerMetricItemRevenue reports in-app item revenue.
	SellerMetricItemRevenue = "revenue_item"

	// ContentMetricUniqueInstalls reports first-time installs for one app.
	ContentMetricUniqueInstalls = "total_unique_installs_filter"
	// ContentMetricRevenue reports total revenue for one app.
	ContentMetricRevenue = "revenue_total"
	// ContentMetricIAPOrders reports purchase and canceled-payment counts.
	ContentMetricIAPOrders = "revenue_iap_order_count"
	// ContentMetricRatingScore reports rating-score totals. Samsung returns
	// scores on a 1-10 scale, where two points equal one displayed star.
	ContentMetricRatingScore = "daily_rat_score"
	// ContentMetricRatingVolume is intentionally spelled as Samsung documents
	// and requires in the public API.
	//nolint:misspell // Samsung's published metric ID contains this typo.
	ContentMetricRatingVolume = "daily_rat_volumne"

	// AggregationDay groups trends by day.
	AggregationDay = "day"
	// AggregationWeek groups trends by week.
	AggregationWeek = "week"
	// AggregationMonth groups trends by month.
	AggregationMonth = "month"
)

var (
	sellerMetricIDs = map[string]struct{}{
		SellerMetricUniqueInstalls:  {},
		SellerMetricRevenue:         {},
		SellerMetricDeviceDownloads: {},
		SellerMetricItemRevenue:     {},
	}
	contentMetricIDs = map[string]struct{}{
		ContentMetricUniqueInstalls: {},
		ContentMetricRevenue:        {},
		ContentMetricIAPOrders:      {},
		ContentMetricRatingScore:    {},
		ContentMetricRatingVolume:   {},
	}
)

// JSONClient is the authenticated Galaxy Store Developer API client surface
// used by this package.
type JSONClient interface {
	DoJSON(
		context.Context,
		string,
		string,
		any,
		any,
	) (*http.Response, error)
}

// Service reads GSS metrics. Although Samsung models both queries as POST,
// these operations only read statistics.
type Service struct {
	client JSONClient
}

// New creates a statistics service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// Period is an inclusive GMT reporting range. Samsung refreshes GSS data
// daily, and dates use YYYY-MM-DD.
type Period struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

// Filters optionally restrict metric breakdowns by Samsung country or device
// names.
type Filters struct {
	Countries []string `json:"country,omitempty"`
	Devices   []string `json:"device,omitempty"`
}

// SellerQuery requests metrics across all apps owned by a seller.
type SellerQuery struct {
	MetricIDs             []string
	Periods               []Period
	GetDailyMetric        bool
	GetBreakdownsByFilter bool
	NoContentMetadata     bool
	Filters               Filters
	TrendAggregation      string
}

// ContentQuery requests metrics for one 12-digit Galaxy Store content ID.
type ContentQuery struct {
	ContentID        string
	MetricIDs        []string
	Periods          []Period
	NoBreakdown      bool
	Filters          Filters
	TrendAggregation string
}

// Response contains the stable GSS response envelope. Data stays as JSON
// because Samsung returns metric-dependent and evolving nested structures.
type Response struct {
	MetricIDs             []string        `json:"metricIds"`
	Data                  json.RawMessage `json:"data"`
	ContentID             string          `json:"contentId,omitempty"`
	Periods               []Period        `json:"periods,omitempty"`
	Filters               Filters         `json:"filters,omitempty"`
	TrendAggregation      string          `json:"trendAggregation,omitempty"`
	GetDailyMetric        bool            `json:"getDailyMetric,omitempty"`
	GetBreakdownsByFilter bool            `json:"getBreakdownsByFilter,omitempty"`
	NoContentMetadata     bool            `json:"noContentMetadata,omitempty"`
	NoBreakdown           bool            `json:"noBreakdown,omitempty"`
}

type sellerRequest struct {
	MetricIDs             []string `json:"metricIds"`
	Periods               []Period `json:"periods"`
	GetDailyMetric        bool     `json:"getDailyMetric"`
	GetBreakdownsByFilter bool     `json:"getBreakdownsByFilter"`
	NoContentMetadata     bool     `json:"noContentMetadata"`
	Filters               Filters  `json:"filters"`
	TrendAggregation      string   `json:"trendAggregation"`
}

type contentRequest struct {
	ContentID        string   `json:"contentId"`
	MetricIDs        []string `json:"metricIds"`
	Periods          []Period `json:"periods"`
	NoBreakdown      bool     `json:"noBreakdown"`
	Filters          Filters  `json:"filters"`
	TrendAggregation string   `json:"trendAggregation"`
}

// Seller reads seller-wide metrics.
func (service *Service) Seller(ctx context.Context, query SellerQuery) (*Response, error) {
	if err := validateMetrics(query.MetricIDs, sellerMetricIDs, "seller"); err != nil {
		return nil, err
	}
	if err := validateQuery(query.Periods, query.Filters, query.TrendAggregation); err != nil {
		return nil, err
	}

	request := sellerRequest{
		MetricIDs:             append([]string(nil), query.MetricIDs...),
		Periods:               append([]Period(nil), query.Periods...),
		GetDailyMetric:        query.GetDailyMetric,
		GetBreakdownsByFilter: query.GetBreakdownsByFilter,
		NoContentMetadata:     query.NoContentMetadata,
		Filters:               cloneFilters(query.Filters),
		TrendAggregation:      query.TrendAggregation,
	}
	var result Response
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		sellerMetricPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("query Galaxy Store seller metrics: %w", err)
	}
	return &result, nil
}

// Content reads metrics for one app.
func (service *Service) Content(ctx context.Context, query ContentQuery) (*Response, error) {
	if err := validateContentID(query.ContentID); err != nil {
		return nil, err
	}
	if err := validateMetrics(query.MetricIDs, contentMetricIDs, "content"); err != nil {
		return nil, err
	}
	if err := validateQuery(query.Periods, query.Filters, query.TrendAggregation); err != nil {
		return nil, err
	}

	request := contentRequest{
		ContentID:        query.ContentID,
		MetricIDs:        append([]string(nil), query.MetricIDs...),
		Periods:          append([]Period(nil), query.Periods...),
		NoBreakdown:      query.NoBreakdown,
		Filters:          cloneFilters(query.Filters),
		TrendAggregation: query.TrendAggregation,
	}
	var result Response
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		contentMetricPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("query Galaxy Store content metrics: %w", err)
	}
	if result.ContentID != "" && result.ContentID != query.ContentID {
		return nil, errors.New("query Galaxy Store content metrics: Samsung returned a different content ID")
	}
	return &result, nil
}

func validateQuery(periods []Period, filters Filters, aggregation string) error {
	if len(periods) == 0 {
		return errors.New("at least one metric period is required")
	}
	for index, period := range periods {
		start, err := parseDate(period.StartDate)
		if err != nil {
			return fmt.Errorf("period %d start date must use YYYY-MM-DD", index+1)
		}
		end, err := parseDate(period.EndDate)
		if err != nil {
			return fmt.Errorf("period %d end date must use YYYY-MM-DD", index+1)
		}
		if start.After(end) {
			return fmt.Errorf("period %d start date cannot be after its end date", index+1)
		}
	}
	if aggregation != AggregationDay &&
		aggregation != AggregationWeek &&
		aggregation != AggregationMonth {
		return errors.New("trend aggregation must be day, week, or month")
	}
	if err := validateFilterValues(filters.Countries, "country"); err != nil {
		return err
	}
	return validateFilterValues(filters.Devices, "device")
}

func validateMetrics(metricIDs []string, allowed map[string]struct{}, kind string) error {
	if len(metricIDs) == 0 {
		return errors.New("at least one metric ID is required")
	}
	seen := make(map[string]struct{}, len(metricIDs))
	for _, metricID := range metricIDs {
		if metricID != strings.TrimSpace(metricID) {
			return fmt.Errorf("%s metric ID must not contain surrounding whitespace", kind)
		}
		if _, ok := allowed[metricID]; !ok {
			return fmt.Errorf("unsupported %s metric ID", kind)
		}
		if _, ok := seen[metricID]; ok {
			return fmt.Errorf("duplicate %s metric ID", kind)
		}
		seen[metricID] = struct{}{}
	}
	return nil
}

func validateFilterValues(values []string, kind string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s filter values must be non-empty and have no surrounding whitespace", kind)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s filter values must not contain control characters", kind)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate %s filter value", kind)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateContentID(contentID string) error {
	if contentID != strings.TrimSpace(contentID) || len(contentID) != 12 {
		return errors.New("content ID must contain exactly 12 digits")
	}
	for _, character := range contentID {
		if character < '0' || character > '9' {
			return errors.New("content ID must contain exactly 12 digits")
		}
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

func cloneFilters(filters Filters) Filters {
	return Filters{
		Countries: append([]string(nil), filters.Countries...),
		Devices:   append([]string(nil), filters.Devices...),
	}
}
