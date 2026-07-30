package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	if err := json.Unmarshal([]byte(client.response), result); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK}, nil
}

func TestNewRequiresClient(t *testing.T) {
	t.Parallel()

	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) unexpectedly succeeded")
	}
}

func TestGetStatusUsesExactMethodPathAndNoBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"subscriptionStatus":"ACTIVE",
		"subscriptionEndDate":"2026-08-30 12:00:00 GMT",
		"itemID":"monthly",
		"price":{"localCurrencyCode":"USD","localPrice":9.99,"supplyPrice":10.49},
		"futureField":{"preserved":true}
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status, err := service.GetStatus(context.Background(), Reference{
		PackageName: "com.example.app",
		PurchaseID:  "purchase-1",
	})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	assertCall(
		t,
		client,
		http.MethodGet,
		"/iap/seller/v6/applications/com.example.app/purchases/subscriptions/purchase-1",
		"",
	)
	if status.SubscriptionStatus != "ACTIVE" ||
		status.ItemID != "monthly" ||
		status.Price == nil ||
		status.Price.LocalPrice.String() != "9.99" {
		t.Fatalf("status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"futureField":{"preserved":true}`) {
		t.Fatalf("lossless JSON = %s", encoded)
	}
}

func TestCancelUsesTypedCallerBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caller Caller
		body   string
	}{
		{name: "default", caller: CallerDefault, body: `{"action":"cancel"}`},
		{name: "admin", caller: CallerAdmin, body: `{"action":"cancel","caller":"admin"}`},
		{name: "user", caller: CallerUser, body: `{"action":"cancel","caller":"user"}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{
				response: `{"code":"0000","message":"Success","futureField":true}`,
			}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			result, err := service.Cancel(context.Background(), validReference(), test.caller)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			assertCall(
				t,
				client,
				http.MethodPatch,
				"/iap/seller/v6/applications/com.example.app/purchases/subscriptions/purchase-1",
				test.body,
			)
			if result.Code != "0000" || result.Message != "Success" {
				t.Fatalf("result = %+v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(encoded), `"futureField":true`) {
				t.Fatalf("lossless JSON = %s", encoded)
			}
		})
	}
}

func TestRefundAndRevokeUseDistinctExactActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(*Service) (*ActionResult, error)
		body   string
	}{
		{
			name: "refund",
			invoke: func(service *Service) (*ActionResult, error) {
				return service.Refund(context.Background(), validReference())
			},
			body: `{"action":"refund"}`,
		},
		{
			name: "revoke",
			invoke: func(service *Service) (*ActionResult, error) {
				return service.Revoke(context.Background(), validReference())
			},
			body: `{"action":"revoke"}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{response: `{"code":"0000","message":"Success"}`}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := test.invoke(service); err != nil {
				t.Fatalf("%s: %v", test.name, err)
			}
			assertCall(
				t,
				client,
				http.MethodPatch,
				"/iap/seller/v6/applications/com.example.app/purchases/subscriptions/purchase-1",
				test.body,
			)
		})
	}
}

func TestSubscriptionOperationsValidateBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	invalidReferences := []Reference{
		{},
		{PackageName: "example", PurchaseID: "purchase-1"},
		{PackageName: "com.example.app", PurchaseID: ""},
		{PackageName: "com.example.app", PurchaseID: "bad/id"},
	}
	for _, reference := range invalidReferences {
		reference := reference
		t.Run(reference.PackageName+"/"+reference.PurchaseID, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.GetStatus(context.Background(), reference); err == nil {
				t.Fatal("GetStatus unexpectedly succeeded")
			}
			if _, err := service.Refund(context.Background(), reference); err == nil {
				t.Fatal("Refund unexpectedly succeeded")
			}
			if len(client.calls) != 0 {
				t.Fatalf("invalid requests made %d calls", len(client.calls))
			}
		})
	}

	client := &fakeClient{}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.Cancel(
		context.Background(),
		validReference(),
		Caller("operator"),
	); err == nil {
		t.Fatal("Cancel with invalid caller unexpectedly succeeded")
	}
	if len(client.calls) != 0 {
		t.Fatalf("invalid caller made %d calls", len(client.calls))
	}
}

func TestMethodsDoNotRepeatClientCalls(t *testing.T) {
	t.Parallel()

	tests := []func(*Service) error{
		func(service *Service) error {
			_, err := service.GetStatus(context.Background(), validReference())
			return err
		},
		func(service *Service) error {
			_, err := service.Cancel(context.Background(), validReference(), CallerAdmin)
			return err
		},
		func(service *Service) error {
			_, err := service.Refund(context.Background(), validReference())
			return err
		},
		func(service *Service) error {
			_, err := service.Revoke(context.Background(), validReference())
			return err
		},
	}
	for _, invoke := range tests {
		client := &fakeClient{err: errors.New("unavailable")}
		service, err := New(client)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := invoke(service); err == nil {
			t.Fatal("operation unexpectedly succeeded")
		}
		if got := len(client.calls); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
	}
}

func TestSamsungClientDoesNotRetrySubscriptionPatch(t *testing.T) {
	t.Parallel()

	attempts := 0
	client, err := samsung.NewClient(
		&http.Client{Transport: subscriptionRoundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":"unavailable"}`)),
			}, nil
		})},
		samsung.TokenProviderFunc(func(context.Context) (string, error) {
			return "token", nil
		}),
		"service-account",
		samsung.WithMaxRetries(5),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := service.Revoke(context.Background(), validReference()); err == nil {
		t.Fatal("Revoke unexpectedly succeeded")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts)
	}
}

func validReference() Reference {
	return Reference{
		PackageName: "com.example.app",
		PurchaseID:  "purchase-1",
	}
}

func assertCall(
	t *testing.T,
	client *fakeClient,
	method string,
	endpoint string,
	body string,
) {
	t.Helper()
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.method != method {
		t.Fatalf("method = %q, want %q", call.method, method)
	}
	if call.endpoint != endpoint {
		t.Fatalf("endpoint = %q, want %q", call.endpoint, endpoint)
	}
	if body == "" {
		if call.body != nil {
			t.Fatalf("body = %#v, want nil", call.body)
		}
		return
	}
	encoded, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	if got := string(encoded); got != body {
		t.Fatalf("body = %s, want %s", got, body)
	}
}

type subscriptionRoundTripFunc func(*http.Request) (*http.Response, error)

func (function subscriptionRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
