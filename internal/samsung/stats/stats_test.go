package stats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	method   string
	endpoint string
	body     any
}

type fakeClient struct {
	calls    []call
	response string
	err      error
}

func (client *fakeClient) DoJSON(
	_ context.Context,
	method string,
	endpoint string,
	body any,
	result any,
) (*http.Response, error) {
	client.calls = append(client.calls, call{
		method:   method,
		endpoint: endpoint,
		body:     body,
	})
	if client.err != nil {
		return nil, client.err
	}
	if err := json.Unmarshal([]byte(client.response), result); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK}, nil
}

func TestNewRequiresClient(t *testing.T) {
	t.Parallel()

	if _, err := New(nil); err == nil {
		t.Fatal("expected missing client to fail")
	}
}

func TestSellerUsesExactReadOnlyPOSTRequest(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"metricIds":["total_unique_installs_filter","revenue_total"],
		"data":{"periods":[{"metricSummaries":{"revenue_total":12.5}}]},
		"trendAggregation":"day",
		"getDailyMetric":true,
		"getBreakdownsByFilter":true,
		"noContentMetadata":true
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.Seller(t.Context(), SellerQuery{
		MetricIDs: []string{
			SellerMetricUniqueInstalls,
			SellerMetricRevenue,
		},
		Periods: []Period{
			{StartDate: "2026-07-01", EndDate: "2026-07-07"},
			{StartDate: "2026-06-24", EndDate: "2026-06-30"},
		},
		GetDailyMetric:        true,
		GetBreakdownsByFilter: true,
		NoContentMetadata:     true,
		Filters: Filters{
			Countries: []string{"USA", "Korea"},
			Devices:   []string{"Galaxy S25"},
		},
		TrendAggregation: AggregationDay,
	})
	if err != nil {
		t.Fatalf("Seller: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	gotCall := client.calls[0]
	if gotCall.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotCall.method)
	}
	if gotCall.endpoint != "/gss/query/sellerMetric" {
		t.Fatalf("endpoint = %q, want /gss/query/sellerMetric", gotCall.endpoint)
	}
	wantBody := sellerRequest{
		MetricIDs: []string{
			SellerMetricUniqueInstalls,
			SellerMetricRevenue,
		},
		Periods: []Period{
			{StartDate: "2026-07-01", EndDate: "2026-07-07"},
			{StartDate: "2026-06-24", EndDate: "2026-06-30"},
		},
		GetDailyMetric:        true,
		GetBreakdownsByFilter: true,
		NoContentMetadata:     true,
		Filters: Filters{
			Countries: []string{"USA", "Korea"},
			Devices:   []string{"Galaxy S25"},
		},
		TrendAggregation: AggregationDay,
	}
	if !reflect.DeepEqual(gotCall.body, wantBody) {
		t.Fatalf("body = %#v, want %#v", gotCall.body, wantBody)
	}
	encodedBody, err := json.Marshal(gotCall.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	wantJSON := `{"metricIds":["total_unique_installs_filter","revenue_total"],` +
		`"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"},` +
		`{"startDate":"2026-06-24","endDate":"2026-06-30"}],` +
		`"getDailyMetric":true,"getBreakdownsByFilter":true,` +
		`"noContentMetadata":true,` +
		`"filters":{"country":["USA","Korea"],"device":["Galaxy S25"]},` +
		`"trendAggregation":"day"}`
	if got := string(encodedBody); got != wantJSON {
		t.Fatalf("encoded body = %s, want %s", got, wantJSON)
	}
	if !reflect.DeepEqual(result.MetricIDs, wantBody.MetricIDs) {
		t.Fatalf("metric IDs = %v", result.MetricIDs)
	}
	if !json.Valid(result.Data) {
		t.Fatalf("data is invalid JSON: %s", result.Data)
	}
}

//nolint:misspell // Samsung's published metric ID contains this typo.
func TestContentUsesExactReadOnlyPOSTRequest(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"contentId":"000007654321",
		"metricIds":["daily_rat_score","daily_rat_volumne"],
		"data":{"periods":[]},
		"noBreakdown":true,
		"trendAggregation":"week"
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.Content(t.Context(), ContentQuery{
		ContentID: "000007654321",
		MetricIDs: []string{
			ContentMetricRatingScore,
			ContentMetricRatingVolume,
		},
		Periods: []Period{
			{StartDate: "2026-07-01", EndDate: "2026-07-07"},
		},
		NoBreakdown:      true,
		Filters:          Filters{},
		TrendAggregation: AggregationWeek,
	})
	if err != nil {
		t.Fatalf("Content: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	gotCall := client.calls[0]
	if gotCall.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotCall.method)
	}
	if gotCall.endpoint != "/gss/query/contentMetric" {
		t.Fatalf("endpoint = %q, want /gss/query/contentMetric", gotCall.endpoint)
	}
	wantBody := contentRequest{
		ContentID: "000007654321",
		MetricIDs: []string{
			ContentMetricRatingScore,
			ContentMetricRatingVolume,
		},
		Periods: []Period{
			{StartDate: "2026-07-01", EndDate: "2026-07-07"},
		},
		NoBreakdown:      true,
		Filters:          Filters{},
		TrendAggregation: AggregationWeek,
	}
	if !reflect.DeepEqual(gotCall.body, wantBody) {
		t.Fatalf("body = %#v, want %#v", gotCall.body, wantBody)
	}
	encodedBody, err := json.Marshal(gotCall.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	wantJSON := `{"contentId":"000007654321",` +
		`"metricIds":["daily_rat_score","daily_rat_volumne"],` +
		`"periods":[{"startDate":"2026-07-01","endDate":"2026-07-07"}],` +
		`"noBreakdown":true,"filters":{},"trendAggregation":"week"}`
	if got := string(encodedBody); got != wantJSON {
		t.Fatalf("encoded body = %s, want %s", got, wantJSON)
	}
	if result.ContentID != "000007654321" {
		t.Fatalf("content ID = %q", result.ContentID)
	}
}

func TestQueriesValidateBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	validPeriod := []Period{{StartDate: "2026-07-01", EndDate: "2026-07-07"}}
	tests := []struct {
		name    string
		run     func(*Service) error
		message string
	}{
		{
			name: "seller no metrics",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					Periods:          validPeriod,
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "metric ID",
		},
		{
			name: "seller unsupported metric",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs:        []string{ContentMetricRatingScore},
					Periods:          validPeriod,
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "seller metric ID",
		},
		{
			name: "content invalid content ID",
			run: func(service *Service) error {
				_, err := service.Content(t.Context(), ContentQuery{
					ContentID:        "not-an-id",
					MetricIDs:        []string{ContentMetricRevenue},
					Periods:          validPeriod,
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "12 digits",
		},
		{
			name: "content unsupported metric",
			run: func(service *Service) error {
				_, err := service.Content(t.Context(), ContentQuery{
					ContentID:        "000007654321",
					MetricIDs:        []string{SellerMetricItemRevenue},
					Periods:          validPeriod,
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "content metric ID",
		},
		{
			name: "duplicate metric",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs: []string{
						SellerMetricRevenue,
						SellerMetricRevenue,
					},
					Periods:          validPeriod,
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "duplicate",
		},
		{
			name: "missing periods",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs:        []string{SellerMetricRevenue},
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "period",
		},
		{
			name: "invalid date",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs: []string{SellerMetricRevenue},
					Periods: []Period{
						{StartDate: "2026-02-30", EndDate: "2026-03-01"},
					},
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "start date",
		},
		{
			name: "reversed period",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs: []string{SellerMetricRevenue},
					Periods: []Period{
						{StartDate: "2026-07-08", EndDate: "2026-07-01"},
					},
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "after",
		},
		{
			name: "invalid aggregation",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs:        []string{SellerMetricRevenue},
					Periods:          validPeriod,
					TrendAggregation: "hour",
				})
				return err
			},
			message: "aggregation",
		},
		{
			name: "empty filter",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs:        []string{SellerMetricRevenue},
					Periods:          validPeriod,
					Filters:          Filters{Countries: []string{""}},
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "country filter",
		},
		{
			name: "duplicate filter",
			run: func(service *Service) error {
				_, err := service.Seller(t.Context(), SellerQuery{
					MetricIDs:        []string{SellerMetricRevenue},
					Periods:          validPeriod,
					Filters:          Filters{Devices: []string{"Galaxy S25", "Galaxy S25"}},
					TrendAggregation: AggregationDay,
				})
				return err
			},
			message: "duplicate",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = test.run(service)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want containing %q", err, test.message)
			}
			if len(client.calls) != 0 {
				t.Fatalf("validation made %d API calls", len(client.calls))
			}
		})
	}
}

func TestQueriesPreserveClientErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("API unavailable")
	client := &fakeClient{err: sentinel}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	periods := []Period{{StartDate: "2026-07-01", EndDate: "2026-07-07"}}

	_, err = service.Seller(t.Context(), SellerQuery{
		MetricIDs:        []string{SellerMetricRevenue},
		Periods:          periods,
		TrendAggregation: AggregationDay,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Seller error = %v, want wrapped sentinel", err)
	}

	_, err = service.Content(t.Context(), ContentQuery{
		ContentID:        "000007654321",
		MetricIDs:        []string{ContentMetricRevenue},
		Periods:          periods,
		TrendAggregation: AggregationDay,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Content error = %v, want wrapped sentinel", err)
	}
}
