package items

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
	client.calls = append(client.calls, call{method: method, endpoint: endpoint, body: body})
	if client.err != nil {
		return nil, client.err
	}
	if result != nil && strings.TrimSpace(client.response) != "" {
		if err := json.Unmarshal([]byte(client.response), result); err != nil {
			return nil, err
		}
	}
	return &http.Response{StatusCode: http.StatusOK}, nil
}

func TestNewRequiresClient(t *testing.T) {
	t.Parallel()

	if _, err := New(nil); err == nil {
		t.Fatal("expected missing client to fail")
	}
}

func TestListUsesExactV6PathAndQuery(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"itemList":[{
			"id":"fuel",
			"title":"Fuel",
			"description":"One tank",
			"type":"ITEM",
			"status":"PUBLISHED",
			"itemPaymentMethod":{"phoneBillStatus":true},
			"usdPrice":0.99,
			"prices":[{"countryId":"USA","currency":"USD","localPrice":"0.99"}],
			"futureField":{"enabled":true}
		}],
		"totalCount":1
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(t.Context(), "com.example.game", ListOptions{Page: 2, Size: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertCall(t, client, http.MethodGet, "/iap/v6/applications/com.example.game/items?page=2&size=50", nil)
	if result.TotalCount != 1 || len(result.Items) != 1 {
		t.Fatalf("result = %#v", result)
	}
	item := result.Items[0]
	if item.ID != "fuel" || item.USDPrice.String() != "0.99" {
		t.Fatalf("item = %#v", item)
	}
	if _, exists := item.UnknownFields["futureField"]; !exists {
		t.Fatalf("unknown fields = %v, want futureField", item.UnknownFields)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal item: %v", err)
	}
	if !strings.Contains(string(encoded), `"futureField"`) {
		t.Fatalf("lossless item JSON = %s", encoded)
	}
}

func TestListReturnsEmptyArrayForMissingItemList(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"totalCount":0}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.List(t.Context(), "com.example.game", ListOptions{Page: 1, Size: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	encoded, err := json.Marshal(result.Items)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("items JSON = %s, want []", encoded)
	}
}

func TestViewUsesExactV6ItemPath(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel_pack","type":"ITEM","status":"UNPUBLISHED"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	item, err := service.View(t.Context(), "com.example.game", "fuel_pack")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	assertCall(
		t,
		client,
		http.MethodGet,
		"/iap/v6/applications/com.example.game/items/fuel_pack",
		nil,
	)
	if item.ID != "fuel_pack" {
		t.Fatalf("item ID = %q", item.ID)
	}
}

func TestCreateUsesExactV6Body(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel","type":"ITEM","status":"PUBLISHED"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := validFullRequest()
	item, err := service.Create(t.Context(), "com.example.game", request)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertJSONCall(
		t,
		client,
		http.MethodPost,
		`{"id":"fuel","title":"Fuel","description":"One tank","type":"ITEM","status":"PUBLISHED","itemPaymentMethod":{"phoneBillStatus":false},"usdPrice":0.99,"prices":[{"countryId":"KOR","currency":"KRW","localPrice":"1000"},{"countryId":"USA","currency":"USD","localPrice":"0.99"}]}`,
	)
	if item.ID != request.ID {
		t.Fatalf("item ID = %q, want %q", item.ID, request.ID)
	}
}

func TestReplaceUsesPUTAndCompleteBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel","type":"ITEM","status":"UNPUBLISHED"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := validFullRequest()
	request.Status = StatusUnpublished
	request.Type = ""

	_, err = service.Replace(t.Context(), "com.example.game", request)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	assertJSONCall(
		t,
		client,
		http.MethodPut,
		`{"id":"fuel","title":"Fuel","description":"One tank","status":"UNPUBLISHED","itemPaymentMethod":{"phoneBillStatus":false},"usdPrice":0.99,"prices":[{"countryId":"KOR","currency":"KRW","localPrice":"1000"},{"countryId":"USA","currency":"USD","localPrice":"0.99"}]}`,
	)
}

func TestUpdateUsesRestrictedPatchBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel","type":"ITEM","status":"PUBLISHED"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	title := "Two tanks"
	request := UpdateRequest{
		ID:    "fuel",
		Title: &title,
		Prices: []PatchPrice{
			{CountryID: "USA", LocalPrice: "1.99"},
		},
	}
	_, err = service.Update(t.Context(), "com.example.game", request)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertJSONCall(
		t,
		client,
		http.MethodPatch,
		`{"id":"fuel","title":"Two tanks","prices":[{"countryId":"USA","localPrice":"1.99"}]}`,
	)
}

func TestUpdateOmitsUnspecifiedPrices(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	title := "New title"
	_, err = service.Update(
		t.Context(),
		"com.example.game",
		UpdateRequest{ID: "fuel", Title: &title},
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertJSONCall(
		t,
		client,
		http.MethodPatch,
		`{"id":"fuel","title":"New title"}`,
	)
}

func TestDeleteUsesExactV6ItemPathAndNoBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"id":"fuel"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	item, err := service.Delete(t.Context(), "com.example.game", "fuel")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertCall(
		t,
		client,
		http.MethodDelete,
		"/iap/v6/applications/com.example.game/items/fuel",
		nil,
	)
	if item.ID != "fuel" {
		t.Fatalf("item ID = %q", item.ID)
	}
}

func TestReadValidationHappensBeforeAPICall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*Service) error
	}{
		{
			name: "invalid package",
			call: func(service *Service) error {
				_, err := service.List(t.Context(), "not-a-package", ListOptions{Page: 1, Size: 20})
				return err
			},
		},
		{
			name: "zero page",
			call: func(service *Service) error {
				_, err := service.List(t.Context(), "com.example.app", ListOptions{Page: 0, Size: 20})
				return err
			},
		},
		{
			name: "zero size",
			call: func(service *Service) error {
				_, err := service.List(t.Context(), "com.example.app", ListOptions{Page: 1, Size: 0})
				return err
			},
		},
		{
			name: "unsafe item ID",
			call: func(service *Service) error {
				_, err := service.View(t.Context(), "com.example.app", "../fuel")
				return err
			},
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
			if err := test.call(service); err == nil {
				t.Fatal("expected validation error")
			}
			if len(client.calls) != 0 {
				t.Fatalf("made %d API calls", len(client.calls))
			}
		})
	}
}

func TestFullMutationValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*FullRequest)
	}{
		{name: "missing ID", mutate: func(request *FullRequest) { request.ID = "" }},
		{name: "missing title", mutate: func(request *FullRequest) { request.Title = "" }},
		{name: "missing description", mutate: func(request *FullRequest) { request.Description = "" }},
		{name: "deprecated type", mutate: func(request *FullRequest) { request.Type = "NON_CONSUMABLE" }},
		{name: "unknown type", mutate: func(request *FullRequest) { request.Type = "SUBSCRIPTION" }},
		{name: "unknown status", mutate: func(request *FullRequest) { request.Status = "DRAFT" }},
		{name: "missing USD price", mutate: func(request *FullRequest) { request.USDPrice = "" }},
		{name: "high USD price", mutate: func(request *FullRequest) { request.USDPrice = "1000" }},
		{name: "missing prices", mutate: func(request *FullRequest) { request.Prices = nil }},
		{
			name: "invalid country",
			mutate: func(request *FullRequest) {
				request.Prices[0].CountryID = "kr"
			},
		},
		{
			name: "invalid currency",
			mutate: func(request *FullRequest) {
				request.Prices[0].Currency = "krw"
			},
		},
		{
			name: "invalid local price",
			mutate: func(request *FullRequest) {
				request.Prices[0].LocalPrice = "-1"
			},
		},
		{
			name: "duplicate country",
			mutate: func(request *FullRequest) {
				request.Prices[1].CountryID = request.Prices[0].CountryID
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := validFullRequest()
			test.mutate(&request)
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.Create(t.Context(), "com.example.app", request); err == nil {
				t.Fatal("expected validation error")
			}
			if len(client.calls) != 0 {
				t.Fatalf("made %d API calls", len(client.calls))
			}
		})
	}
}

func TestPatchValidationProtectsOmittedAndEmptySemantics(t *testing.T) {
	t.Parallel()

	empty := ""
	validTitle := "Title"
	tests := []UpdateRequest{
		{ID: "fuel"},
		{ID: "fuel", Title: &empty},
		{ID: "fuel", Prices: []PatchPrice{}},
		{ID: "fuel", Title: &validTitle, Prices: []PatchPrice{{CountryID: "USA"}}},
		{ID: "fuel", Prices: []PatchPrice{{CountryID: "USA", LocalPrice: "0.99"}, {CountryID: "USA", LocalPrice: "1.99"}}},
	}
	for _, request := range tests {
		request := request
		t.Run(strings.ReplaceAll(mustJSON(t, request), "/", "_"), func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.Update(t.Context(), "com.example.app", request); err == nil {
				t.Fatal("expected validation error")
			}
			if len(client.calls) != 0 {
				t.Fatalf("made %d API calls", len(client.calls))
			}
		})
	}
}

func TestMethodsWrapClientErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("API unavailable")
	service, err := New(&fakeClient{err: sentinel})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	full := validFullRequest()
	title := "Title"
	calls := []func() error{
		func() error {
			_, err := service.List(t.Context(), "com.example.app", ListOptions{Page: 1, Size: 20})
			return err
		},
		func() error {
			_, err := service.View(t.Context(), "com.example.app", "fuel")
			return err
		},
		func() error {
			_, err := service.Create(t.Context(), "com.example.app", full)
			return err
		},
		func() error {
			_, err := service.Replace(t.Context(), "com.example.app", full)
			return err
		},
		func() error {
			_, err := service.Update(t.Context(), "com.example.app", UpdateRequest{ID: "fuel", Title: &title})
			return err
		},
		func() error {
			_, err := service.Delete(t.Context(), "com.example.app", "fuel")
			return err
		},
	}
	for index, call := range calls {
		if err := call(); !errors.Is(err, sentinel) {
			t.Fatalf("call %d error = %v, want sentinel", index, err)
		}
	}
}

func TestMutationResponsesMustNotChangeItemID(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name string
		call func(*Service) error
	}{
		{
			name: "view",
			call: func(service *Service) error {
				_, err := service.View(t.Context(), "com.example.app", "fuel")
				return err
			},
		},
		{
			name: "create",
			call: func(service *Service) error {
				_, err := service.Create(t.Context(), "com.example.app", validFullRequest())
				return err
			},
		},
		{
			name: "replace",
			call: func(service *Service) error {
				_, err := service.Replace(t.Context(), "com.example.app", validFullRequest())
				return err
			},
		},
		{
			name: "update",
			call: func(service *Service) error {
				title := "Title"
				_, err := service.Update(t.Context(), "com.example.app", UpdateRequest{ID: "fuel", Title: &title})
				return err
			},
		},
		{
			name: "delete",
			call: func(service *Service) error {
				_, err := service.Delete(t.Context(), "com.example.app", "fuel")
				return err
			},
		},
	} {
		operation := operation
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			service, err := New(&fakeClient{response: `{"id":"different"}`})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := operation.call(service); err == nil || !strings.Contains(err.Error(), "different item ID") {
				t.Fatalf("error = %v, want item-ID mismatch", err)
			}
		})
	}
}

func TestSingleItemResponsesRequireItemID(t *testing.T) {
	t.Parallel()

	service, err := New(&fakeClient{response: `{}`})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.View(t.Context(), "com.example.app", "fuel"); err == nil ||
		!strings.Contains(err.Error(), "no item ID") {
		t.Fatalf("View error = %v, want missing item ID", err)
	}
}

func validFullRequest() FullRequest {
	return FullRequest{
		ID:          "fuel",
		Title:       "Fuel",
		Description: "One tank",
		Type:        TypeItem,
		Status:      StatusPublished,
		ItemPaymentMethod: ItemPaymentMethod{
			PhoneBillStatus: false,
		},
		USDPrice: "0.99",
		Prices: []Price{
			{CountryID: "KOR", Currency: "KRW", LocalPrice: "1000"},
			{CountryID: "USA", Currency: "USD", LocalPrice: "0.99"},
		},
	}
}

func assertCall(
	t *testing.T,
	client *fakeClient,
	method string,
	endpoint string,
	body any,
) {
	t.Helper()
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	got := client.calls[0]
	if got.method != method || got.endpoint != endpoint || !reflect.DeepEqual(got.body, body) {
		t.Fatalf("call = %#v, want method=%s endpoint=%s body=%#v", got, method, endpoint, body)
	}
}

func assertJSONCall(
	t *testing.T,
	client *fakeClient,
	method string,
	body string,
) {
	t.Helper()
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	got := client.calls[0]
	endpoint := "/iap/v6/applications/com.example.game/items"
	if got.method != method || got.endpoint != endpoint {
		t.Fatalf("call = %#v, want method=%s endpoint=%s", got, method, endpoint)
	}
	if encoded := mustJSON(t, got.body); encoded != body {
		t.Fatalf("body = %s, want %s", encoded, body)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(encoded)
}
