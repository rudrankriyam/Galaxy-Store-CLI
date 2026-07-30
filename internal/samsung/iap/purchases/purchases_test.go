package purchases

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

func TestConsumeUsesExactMethodPathAndBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		response: `{
			"totalCount":1,
			"purchaseItemList":[{
				"purchaseId":"purchase-1",
				"statusCode":"0",
				"statusString":"success.",
				"futureField":true
			}],
			"futureTopLevel":"preserved"
		}`,
	}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.Consume(context.Background(), Request{
		PackageName: "com.example.app",
		PurchaseID:  "purchase-1",
	})
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	assertSingleCall(
		t,
		client,
		http.MethodPatch,
		"/iap/v6/applications/com.example.app/purchases/purchase-1",
		`{"action":"consume"}`,
	)
	if result.TotalCount != 1 ||
		len(result.PurchaseItemList) != 1 ||
		result.PurchaseItemList[0].StatusCode != "0" {
		t.Fatalf("result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"futureTopLevel":"preserved"`) ||
		!strings.Contains(string(encoded), `"futureField":true`) {
		t.Fatalf("lossless JSON = %s", encoded)
	}
}

func TestAcknowledgeUsesExactBatchBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"totalCount":2,"purchaseItemList":[]}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = service.Acknowledge(context.Background(), Request{
		PackageName:     "com.example.app",
		PurchaseID:      "purchase-1",
		PurchasedIDList: []string{"purchase-2", "purchase-3"},
	})
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	assertSingleCall(
		t,
		client,
		http.MethodPatch,
		"/iap/v6/applications/com.example.app/purchases/purchase-1",
		`{"action":"acknowledge","purchasedIdList":["purchase-2","purchase-3"]}`,
	)
}

func TestProcessingValidatesBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	tests := []Request{
		{},
		{PackageName: "example", PurchaseID: "purchase-1"},
		{PackageName: "com.example.app", PurchaseID: ""},
		{PackageName: "com.example.app", PurchaseID: "bad/id"},
		{
			PackageName:     "com.example.app",
			PurchaseID:      "purchase-1",
			PurchasedIDList: []string{" "},
		},
	}
	for _, request := range tests {
		request := request
		t.Run(request.PackageName+"/"+request.PurchaseID, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.Consume(context.Background(), request); err == nil {
				t.Fatalf("Consume(%+v) unexpectedly succeeded", request)
			}
			if len(client.calls) != 0 {
				t.Fatalf("invalid request made %d calls", len(client.calls))
			}
		})
	}
}

func TestMutationsDoNotRetryClientErrors(t *testing.T) {
	t.Parallel()

	for _, invoke := range []func(*Service) error{
		func(service *Service) error {
			_, err := service.Consume(context.Background(), Request{
				PackageName: "com.example.app",
				PurchaseID:  "purchase-1",
			})
			return err
		},
		func(service *Service) error {
			_, err := service.Acknowledge(context.Background(), Request{
				PackageName: "com.example.app",
				PurchaseID:  "purchase-1",
			})
			return err
		},
	} {
		client := &fakeClient{err: errors.New("unavailable")}
		service, err := New(client)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := invoke(service); err == nil {
			t.Fatal("mutation unexpectedly succeeded")
		}
		if got := len(client.calls); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
	}
}

func TestSamsungClientDoesNotRetryPurchasePatch(t *testing.T) {
	t.Parallel()

	attempts := 0
	client, err := samsung.NewClient(
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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

	if _, err := service.Consume(context.Background(), Request{
		PackageName: "com.example.app",
		PurchaseID:  "purchase-1",
	}); err == nil {
		t.Fatal("Consume unexpectedly succeeded")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts)
	}
}

func assertSingleCall(
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
	encoded, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	if got := string(encoded); got != body {
		t.Fatalf("body = %s, want %s", got, body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
