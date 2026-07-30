package beta

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
)

type call struct {
	method   string
	endpoint string
	body     any
}

type fakeClient struct {
	calls     []call
	responses []string
	err       error
}

func (client *fakeClient) DoJSON(
	_ context.Context,
	method string,
	endpoint string,
	body any,
	result any,
) (*http.Response, error) {
	client.calls = append(client.calls, call{method: method, endpoint: endpoint, body: body})
	if client.err != nil {
		return nil, client.err
	}
	response := `{"resultCode":"0000","resultMessage":"Ok","data":{}}`
	if len(client.responses) != 0 {
		response = client.responses[0]
		client.responses = client.responses[1:]
	}
	if err := json.Unmarshal([]byte(response), result); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK}, nil
}

func TestNewRequiresClient(t *testing.T) {
	t.Parallel()

	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) expected error")
	}
}

func TestGetUsesExactRequestAndResponse(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{`{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{
			"totalNumberOfBetaTesters":4,
			"betaTesters":["12345@company.com","67890@company.com"],
			"feedbackChannel":"closed-beta-test@company.com",
			"betaTestingUrl":{
				"android":"http://apps.samsung.com/betastore/closeAppDetail.as?cId=000007654321",
				"instantPlay2":"https://apps.samsung.com/n/cloudgame/play?content_id=000007654321"
			}
		}
	}`}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.Get(context.Background(), ListOptions{
		ContentID: "000007654321",
		AppStatus: "REGISTRATION",
		Offset:    20,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got, want := client.calls, []call{{
		method:   http.MethodGet,
		endpoint: "/seller/v2/content/betaTest?appStatus=REGISTRATION&contentId=000007654321&limit=50&offset=20",
		body:     nil,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	if result.TotalNumberOfBetaTesters != 4 ||
		!reflect.DeepEqual(result.BetaTesters, []string{"12345@company.com", "67890@company.com"}) ||
		result.FeedbackChannel != "closed-beta-test@company.com" ||
		!strings.Contains(result.BetaTestingURL.Android, "000007654321") {
		t.Fatalf("result = %#v", result)
	}
}

func TestGetUsesDocumentedPaginationDefaults(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.Get(context.Background(), ListOptions{
		ContentID: "000007654321",
		AppStatus: "SALE",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := client.calls[0].endpoint; got != "/seller/v2/content/betaTest?appStatus=SALE&contentId=000007654321&limit=1000&offset=0" {
		t.Fatalf("endpoint = %q", got)
	}
	if result.BetaTesters == nil {
		t.Fatal("empty beta testers must be [] rather than null")
	}
}

func TestUpdateUsesExactBodyAndSurfacesPerTesterFailures(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{`{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{
			"additionFailedTesters":["67890@company.com"],
			"deletionFailedTesters":["24680@company.com"]
		}
	}`}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	feedback := "beta-test-feedback@company.com"
	result, err := service.Update(context.Background(), UpdateInput{
		ContentID:       "000007654321",
		AddTesters:      []string{"12345@company.com", "67890@company.com"},
		DeleteTesters:   []string{"13579@company.com", "24680@company.com"},
		FeedbackChannel: &feedback,
	})
	var testerError *TesterFailuresError
	if !errors.As(err, &testerError) {
		t.Fatalf("Update error = %T %v, want *TesterFailuresError", err, err)
	}
	if !reflect.DeepEqual(result.AdditionFailedTesters, []string{"67890@company.com"}) ||
		!reflect.DeepEqual(result.DeletionFailedTesters, []string{"24680@company.com"}) {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(testerError.AdditionFailed, result.AdditionFailedTesters) ||
		!reflect.DeepEqual(testerError.DeletionFailed, result.DeletionFailedTesters) {
		t.Fatalf("tester error = %#v", testerError)
	}

	if got, want := client.calls[0].method, http.MethodPut; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := client.calls[0].endpoint, "/seller/v2/content/betaTest"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	encoded, marshalErr := json.Marshal(client.calls[0].body)
	if marshalErr != nil {
		t.Fatalf("Marshal body: %v", marshalErr)
	}
	wantBody := `{"contentId":"000007654321","betaTestersToBeAdded":["12345@company.com","67890@company.com"],"betaTestersToBeDeleted":["13579@company.com","24680@company.com"],"feedbackChannel":"beta-test-feedback@company.com"}`
	if string(encoded) != wantBody {
		t.Fatalf("body = %s, want %s", encoded, wantBody)
	}
}

func TestUpdateSuccessReturnsEmptyFailureArrays(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.Update(context.Background(), UpdateInput{
		ContentID:  "000007654321",
		AddTesters: []string{"valid@company.com"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.AdditionFailedTesters == nil || result.DeletionFailedTesters == nil {
		t.Fatalf("failure arrays = %#v/%#v, want empty arrays", result.AdditionFailedTesters, result.DeletionFailedTesters)
	}
}

func TestRejectsNonSuccessSamsungResultOnHTTP200(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{
		`{"resultCode":"3202","resultMessage":"the content is not a beta application","data":{}}`,
	}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = service.Get(context.Background(), ListOptions{
		ContentID: "000007654321",
		AppStatus: "REGISTRATION",
	})
	if err == nil || !strings.Contains(err.Error(), "samsung result 3202") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestValidatesContentIDStatusPaginationAndBatchBeforeRequests(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	getInputs := []ListOptions{
		{ContentID: "123", AppStatus: "SALE"},
		{ContentID: "000007654321", AppStatus: ""},
		{ContentID: "000007654321", AppStatus: "sale"},
		{ContentID: "000007654321", AppStatus: " SALE"},
		{ContentID: "000007654321", AppStatus: "SALE", Offset: -1},
		{ContentID: "000007654321", AppStatus: "SALE", Limit: 1001},
	}
	for _, input := range getInputs {
		if _, err := service.Get(context.Background(), input); err == nil {
			t.Fatalf("Get(%+v) expected error", input)
		}
	}

	tooMany := make([]string, 1001)
	for index := range tooMany {
		tooMany[index] = "tester"
	}
	updateInputs := []UpdateInput{
		{ContentID: "123", AddTesters: []string{"tester"}},
		{ContentID: "000007654321"},
		{ContentID: "000007654321", AddTesters: []string{""}},
		{ContentID: "000007654321", AddTesters: []string{" padded "}},
		{ContentID: "000007654321", AddTesters: tooMany},
	}
	for _, input := range updateInputs {
		if _, err := service.Update(context.Background(), input); err == nil {
			t.Fatalf("Update(%+v) expected error", input)
		}
	}
	if len(client.calls) != 0 {
		t.Fatalf("invalid input made %d API calls", len(client.calls))
	}
}

func TestUpdateMutationIsNotRetried(t *testing.T) {
	t.Parallel()

	transport := &countingTransport{}
	client, err := samsung.NewClient(
		&http.Client{Transport: transport},
		samsung.TokenProviderFunc(func(context.Context) (string, error) { return "token", nil }),
		"service-account",
		samsung.WithMaxRetries(5),
	)
	if err != nil {
		t.Fatalf("samsung.NewClient: %v", err)
	}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.Update(context.Background(), UpdateInput{
		ContentID:  "000007654321",
		AddTesters: []string{"tester@company.com"},
	}); err == nil {
		t.Fatal("Update expected API error")
	}
	if transport.calls != 1 {
		t.Fatalf("PUT attempts = %d, want 1", transport.calls)
	}
}

type countingTransport struct {
	calls int
}

func (transport *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"resultCode":"9000","resultMessage":"unavailable"}`)),
	}, nil
}
