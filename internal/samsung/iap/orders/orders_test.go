package orders

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

func TestListUsesExactReadOnlyPostPathAndBody(t *testing.T) {
	t.Parallel()

	next := "next-token"
	client := &fakeClient{response: `{
		"continuationToken":"next-token",
		"orderItemList":[{
			"orderId":"S20260730KR00000001",
			"purchaseId":"purchase-1",
			"status":"2",
			"usdPrice":"4.99",
			"futureField":"preserved"
		}],
		"futureTopLevel":true
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(context.Background(), ListOptions{
		SellerSequence:    "000123456789",
		PackageName:       "com.example.app",
		RequestDate:       "20260730",
		ContinuationToken: "current-token",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertSingleCall(
		t,
		client,
		http.MethodPost,
		"/iap/seller/orders",
		`{"sellerSeq":"000123456789","packageName":"com.example.app","requestDate":"20260730","continuationToken":"current-token"}`,
	)
	if result.ContinuationToken == nil || *result.ContinuationToken != next {
		t.Fatalf("continuation token = %#v", result.ContinuationToken)
	}
	if len(result.OrderItemList) != 1 ||
		result.OrderItemList[0].OrderID != "S20260730KR00000001" ||
		result.OrderItemList[0].Status != "2" {
		t.Fatalf("result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"futureTopLevel":true`) ||
		!strings.Contains(string(encoded), `"futureField":"preserved"`) {
		t.Fatalf("lossless JSON = %s", encoded)
	}
}

func TestListOmitsOptionalBodyFields(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"continuationToken":null,"orderItemList":[]}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(context.Background(), ListOptions{
		SellerSequence: "000123456789",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertSingleCall(
		t,
		client,
		http.MethodPost,
		"/iap/seller/orders",
		`{"sellerSeq":"000123456789"}`,
	)
	if result.ContinuationToken != nil {
		t.Fatalf("continuation token = %#v, want nil", result.ContinuationToken)
	}
	if result.OrderItemList == nil {
		t.Fatal("orderItemList should preserve Samsung's empty array")
	}
}

func TestListValidatesBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	tests := []ListOptions{
		{},
		{SellerSequence: "123"},
		{SellerSequence: "00012345678x"},
		{SellerSequence: "000123456789", PackageName: "invalid"},
		{SellerSequence: "000123456789", RequestDate: "20260230"},
		{SellerSequence: "000123456789", RequestDate: "2026-07-30"},
		{SellerSequence: "000123456789", ContinuationToken: " token"},
	}
	for _, options := range tests {
		options := options
		t.Run(options.SellerSequence+"/"+options.RequestDate, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := service.List(context.Background(), options); err == nil {
				t.Fatalf("List(%+v) unexpectedly succeeded", options)
			}
			if len(client.calls) != 0 {
				t.Fatalf("invalid request made %d calls", len(client.calls))
			}
		})
	}
}

func TestListDoesNotRepeatClientCall(t *testing.T) {
	t.Parallel()

	client := &fakeClient{err: errors.New("unavailable")}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.List(context.Background(), ListOptions{
		SellerSequence: "000123456789",
	}); err == nil {
		t.Fatal("List unexpectedly succeeded")
	}
	if got := len(client.calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestSamsungClientDoesNotRetryReadOnlyOrdersPost(t *testing.T) {
	t.Parallel()

	attempts := 0
	client, err := samsung.NewClient(
		&http.Client{Transport: ordersRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			if request.Method != http.MethodPost {
				t.Fatalf("method = %q, want POST", request.Method)
			}
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

	if _, err := service.List(context.Background(), ListOptions{
		SellerSequence: "000123456789",
	}); err == nil {
		t.Fatal("List unexpectedly succeeded")
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

type ordersRoundTripFunc func(*http.Request) (*http.Response, error)

func (function ordersRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
