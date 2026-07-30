package apps

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

func TestListUsesExactContentListRequestAndModelsFields(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[
		{
			"contentName":"Samsung Pay",
			"contentId":"000001234567",
			"appStatus":"REGISTRATION",
			"contentStatus":"REGISTERING",
			"packageName":"com.example.pay",
			"modifyDate":"2026-07-30 10:00:00.0"
		}
	]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	if got, want := client.calls[0].method, http.MethodGet; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := client.calls[0].endpoint, "/seller/contentList"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if client.calls[0].body != nil {
		t.Fatalf("body = %#v, want nil", client.calls[0].body)
	}
	if len(result.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(result.Apps))
	}
	app := result.Apps[0]
	if app.ContentID != "000001234567" ||
		app.AppStatus != "REGISTRATION" ||
		app.ContentStatus != "REGISTERING" ||
		app.Title != "Samsung Pay" ||
		app.PackageName != "com.example.pay" {
		t.Fatalf("app = %+v", app)
	}
	if _, ok := app.UnknownFields["modifyDate"]; !ok {
		t.Fatalf("unknown fields = %v, want modifyDate", app.UnknownFields)
	}
	if got, want := result.Pagination, (Pagination{
		Offset: 0,
		Limit:  1,
		Total:  1,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("pagination = %+v, want %+v", got, want)
	}
}

func TestListPaginatesLocallyWithoutInventingAPIQueryParameters(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[
		{"contentId":"000000000001","contentName":"One"},
		{"contentId":"000000000002","contentName":"Two"},
		{"contentId":"000000000003","contentName":"Three"}
	]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(context.Background(), ListOptions{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := client.calls[0].endpoint; got != "/seller/contentList" {
		t.Fatalf("endpoint = %q, want no query parameters", got)
	}
	if got := []string{result.Apps[0].ContentID}; !reflect.DeepEqual(got, []string{"000000000002"}) {
		t.Fatalf("content IDs = %v", got)
	}
	if got, want := result.Pagination, (Pagination{
		Offset:     1,
		Limit:      1,
		Total:      3,
		HasMore:    true,
		NextOffset: 2,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("pagination = %+v, want %+v", got, want)
	}
}

func TestListValidatesPaginationBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	for _, options := range []ListOptions{
		{Offset: -1},
		{Limit: -1},
	} {
		client := &fakeClient{}
		service, err := New(client)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := service.List(context.Background(), options); err == nil {
			t.Fatalf("List(%+v) expected error", options)
		}
		if len(client.calls) != 0 {
			t.Fatalf("List(%+v) made %d calls", options, len(client.calls))
		}
	}
}

func TestListReturnsEmptyArrayWhenSellerHasNoApps(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	encoded, err := json.Marshal(result.Apps)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(encoded), "[]"; got != want {
		t.Fatalf("apps JSON = %s, want %s", got, want)
	}
}

func TestGetUsesExactContentInfoQueryAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[
		{
			"contentId":"000007654321",
			"appTitle":"The best app ever!",
			"appStatus":"SALE",
			"contentStatus":"FOR_SALE",
			"packageName":"com.example.current",
			"defaultLanguageCode":"ENG"
		}
	]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	records, err := service.View(context.Background(), "000007654321")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	app := records[0]
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	if got, want := client.calls[0].method, http.MethodGet; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := client.calls[0].endpoint, "/seller/contentInfo?contentId=000007654321"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if client.calls[0].body != nil {
		t.Fatalf("body = %#v, want nil", client.calls[0].body)
	}
	if app.Title != "The best app ever!" ||
		app.PackageName != "com.example.current" ||
		app.AppStatus != "SALE" ||
		app.ContentStatus != "FOR_SALE" {
		t.Fatalf("app = %+v", app)
	}
	if _, ok := app.UnknownFields["defaultLanguageCode"]; !ok {
		t.Fatalf("unknown fields = %v, want defaultLanguageCode", app.UnknownFields)
	}
}

func TestViewReadsBinaryListWithoutUsingItAsAnUpdateContract(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[
		{
			"contentId":"000007654321",
			"appTitle":"Legacy response",
			"binaryList":[{"packageName":"com.example.legacy"}]
		}
	]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	records, err := service.View(context.Background(), "000007654321")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	app := records[0]
	if app.PackageName != "com.example.legacy" {
		t.Fatalf("PackageName = %q, want common read-only binary package", app.PackageName)
	}
	if len(app.Binaries) != 1 || app.Binaries[0].PackageName != "com.example.legacy" {
		t.Fatalf("Binaries = %#v", app.Binaries)
	}
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"binaryList"`) {
		t.Fatalf("lossless app JSON = %s, want original binaryList", encoded)
	}
}

func TestGetValidatesExactTwelveDigitContentIDBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"123",
		"0000076543210",
		"00000765432x",
		" 000007654321",
		"000007654321 ",
		"０００００７６５４３２１",
	}
	for _, contentID := range invalid {
		contentID := contentID
		t.Run(contentID, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.View(context.Background(), contentID); err == nil {
				t.Fatalf("View(%q) expected error", contentID)
			}
			if len(client.calls) != 0 {
				t.Fatalf("Get(%q) made %d calls", contentID, len(client.calls))
			}
		})
	}
}

func TestMethodsPreserveClientErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("API unavailable")
	client := &fakeClient{err: sentinel}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := service.List(context.Background(), ListOptions{}); !errors.Is(err, sentinel) {
		t.Fatalf("List error = %v, want wrapped sentinel", err)
	}
	if _, err := service.View(context.Background(), "000007654321"); !errors.Is(err, sentinel) {
		t.Fatalf("View error = %v, want wrapped sentinel", err)
	}
}

func TestViewHandlesAmbiguousAndInvalidResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		contains string
	}{
		{name: "empty", response: `[]`, contains: "no app"},
		{
			name:     "mismatch",
			response: `[{"contentId":"000000000001"}]`,
			contains: "different content ID",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{response: test.response}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = service.View(context.Background(), "000007654321")
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Get error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestViewReturnsSaleAndRegistrationVariantsWithoutChoosing(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[
		{"contentId":"000007654321","appStatus":"SALE","contentStatus":"FOR_SALE"},
		{"contentId":"000007654321","appStatus":"REGISTRATION","contentStatus":"REGISTERING"}
	]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	records, err := service.View(context.Background(), "000007654321")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(records) != 2 || records[0].AppStatus != "SALE" || records[1].AppStatus != "REGISTRATION" {
		t.Fatalf("records = %#v", records)
	}
}

func TestViewDoesNotGuessWhenBinaryPackageNamesDiffer(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `[{
		"contentId":"000007654321",
		"binaryList":[
			{"packageName":"com.example.one"},
			{"packageName":"com.example.two"}
		]
	}]`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	records, err := service.View(context.Background(), "000007654321")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if records[0].PackageName != "" {
		t.Fatalf("PackageName = %q, want no ambiguous guess", records[0].PackageName)
	}
}

func TestAppAcceptsNumericContentIDWithoutFloatingPointConversion(t *testing.T) {
	t.Parallel()

	var app App
	if err := json.Unmarshal([]byte(`{"contentId":123456789012}`), &app); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if app.ContentID != "123456789012" {
		t.Fatalf("ContentID = %q", app.ContentID)
	}
}
