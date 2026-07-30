package reviews

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
	calls    []call
	response string
	err      error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
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
	if result != nil && client.response != "" {
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

func TestListUsesDocumentedGETJSONBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{
			"contentId":"000005021191",
			"totalCount":7,
			"pageNo":2,
			"totalPage":3,
			"comments":[{
				"commentId":"5501585",
				"countryCode":"USA",
				"buyerId":"adzc**",
				"rating":8,
				"date":"2025-11-04",
				"commentText":"Four stars !!",
				"countryName":"USA",
				"device":"Galaxy S10",
				"appVersion":"2.0",
				"replyId":"44323",
				"replyText":"Thank You",
				"futureField":{"kept":true}
			}]
		},
		"trace":{"kept":true}
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.List(context.Background(), ListOptions{
		ContentID: "000005021191",
		CommentID: "5501585",
		Page:      2,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if got, want := call.method, http.MethodGet; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := call.endpoint, "/seller/v2/content/comment"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := call.body, (listRequest{
		ContentID: "000005021191",
		CommentID: "5501585",
		Page:      2,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, want %#v", got, want)
	}
	encodedBody, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if got, want := string(encodedBody), `{"contentId":"000005021191","commentId":"5501585","pageNo":2}`; got != want {
		t.Fatalf("body JSON = %s, want %s", got, want)
	}

	if result.ResultCode != "0000" ||
		result.Data.ContentID != "000005021191" ||
		result.Data.TotalCount != 7 ||
		result.Data.Page != 2 ||
		result.Data.TotalPages != 3 ||
		len(result.Data.Comments) != 1 {
		t.Fatalf("result = %+v", result)
	}
	comment := result.Data.Comments[0]
	if comment.CommentID != "5501585" ||
		comment.CountryCode != "USA" ||
		comment.BuyerID != "adzc**" ||
		comment.Rating == nil ||
		*comment.Rating != 8 ||
		comment.ReplyID != "44323" {
		t.Fatalf("comment = %+v", comment)
	}
}

func TestListSendsDocumentedGETJSONBodyOnWire(t *testing.T) {
	t.Parallel()

	var capturedMethod string
	var capturedURL string
	var capturedHeaders http.Header
	var capturedBody string
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("ReadAll request: %v", err)
			}
			capturedMethod = request.Method
			capturedURL = request.URL.String()
			capturedHeaders = request.Header.Clone()
			capturedBody = string(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"resultCode":"0000","resultMessage":"Ok","data":{"contentId":"000005021191","totalCount":0,"pageNo":1,"totalPage":0,"comments":[]}}`,
				)),
				Request: request,
			}, nil
		}),
	}
	apiClient, err := samsung.NewClient(
		httpClient,
		samsung.TokenProviderFunc(func(context.Context) (string, error) {
			return "access-token", nil
		}),
		"service-account",
		samsung.WithMaxRetries(0),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	service, err := New(apiClient)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := service.List(context.Background(), ListOptions{
		ContentID: "000005021191",
		CommentID: "5501585",
		Page:      3,
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := capturedMethod, http.MethodGet; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := capturedURL, "https://devapi.samsungapps.com/seller/v2/content/comment"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := capturedHeaders.Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := capturedHeaders.Get("Authorization"), "Bearer access-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := capturedHeaders.Get(samsung.ServiceAccountIDHeader), "service-account"; got != want {
		t.Fatalf("%s = %q, want %q", samsung.ServiceAccountIDHeader, got, want)
	}
	if got, want := capturedBody, `{"contentId":"000005021191","commentId":"5501585","pageNo":3}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestListOmitsOptionalFieldsAndPreservesEmptyComments(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{
			"contentId":500000000001,
			"totalCount":0,
			"pageNo":1,
			"totalPage":0,
			"comments":null
		}
	}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.List(context.Background(), ListOptions{ContentID: "500000000001"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	body, err := json.Marshal(client.calls[0].body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if got, want := string(body), `{"contentId":"500000000001"}`; got != want {
		t.Fatalf("body JSON = %s, want %s", got, want)
	}
	if result.Data.ContentID != "500000000001" {
		t.Fatalf("ContentID = %q", result.Data.ContentID)
	}
	if result.Data.Comments == nil || len(result.Data.Comments) != 0 {
		t.Fatalf("Comments = %#v, want non-nil empty slice", result.Data.Comments)
	}
	encoded, err := json.Marshal(result.Data.Comments)
	if err != nil {
		t.Fatalf("Marshal comments: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("comments JSON = %s, want []", encoded)
	}
}

func TestListPreservesUnknownResponseFieldsAtEveryUsefulLevel(t *testing.T) {
	t.Parallel()

	response := `{"resultCode":"0000","resultMessage":"Ok","futureTop":1,"data":{"contentId":"000005021191","totalCount":1,"pageNo":1,"totalPage":1,"futurePage":2,"comments":[{"commentId":"5501585","countryCode":"USA","buyerId":"a**","rating":null,"date":"2026-01-01","commentText":"Good","countryName":"USA","device":"Galaxy","appVersion":null,"replyId":null,"replyText":null,"futureComment":3}]}}`
	client := &fakeClient{response: response}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := service.List(context.Background(), ListOptions{ContentID: "000005021191"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Data.Comments[0].Rating != nil ||
		result.Data.Comments[0].AppVersion != "" ||
		result.Data.Comments[0].ReplyID != "" {
		t.Fatalf("nullable fields = %+v", result.Data.Comments[0])
	}

	for name, value := range map[string]any{
		"response": result,
		"page":     result.Data,
		"comment":  result.Data.Comments[0],
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal %s: %v", name, err)
		}
		expectedField := map[string]string{
			"response": `"futureTop":1`,
			"page":     `"futurePage":2`,
			"comment":  `"futureComment":3`,
		}[name]
		if !strings.Contains(string(encoded), expectedField) {
			t.Fatalf("%s JSON = %s, want %s", name, encoded, expectedField)
		}
	}
}

func TestReplyUsesExactPOSTRequest(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"resultCode":"0000","resultMessage":"Ok","requestId":"future"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := ReplyRequest{
		ContentID:   "000005021191",
		CommentID:   "5501581",
		CountryCode: "KOR",
		ReplyText:   "감사합니다",
	}
	result, err := service.Reply(context.Background(), request)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.method != http.MethodPost ||
		call.endpoint != "/seller/v2/content/comment/reply" ||
		!reflect.DeepEqual(call.body, request) {
		t.Fatalf("call = %#v", call)
	}
	body, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if got, want := string(body), `{"contentId":"000005021191","commentId":"5501581","countryCode":"KOR","replyText":"감사합니다"}`; got != want {
		t.Fatalf("body JSON = %s, want %s", got, want)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal result: %v", err)
	}
	if !strings.Contains(string(encodedResult), `"requestId":"future"`) {
		t.Fatalf("result JSON = %s, want preserved requestId", encodedResult)
	}
}

func TestDeleteReplyUsesExactDELETEJSONBody(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"resultCode":"0000","resultMessage":"Ok"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := DeleteReplyRequest{ReplyID: "252"}
	if _, err := service.DeleteReply(context.Background(), request); err != nil {
		t.Fatalf("DeleteReply: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.method != http.MethodDelete ||
		call.endpoint != "/seller/v2/content/comment/reply" ||
		!reflect.DeepEqual(call.body, request) {
		t.Fatalf("call = %#v", call)
	}
	body, err := json.Marshal(call.body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if got, want := string(body), `{"replyId":"252"}`; got != want {
		t.Fatalf("body JSON = %s, want %s", got, want)
	}
}

func TestValidationHappensBeforeRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "list content ID",
			run: func(service *Service) error {
				_, err := service.List(context.Background(), ListOptions{ContentID: "123"})
				return err
			},
		},
		{
			name: "list comment ID",
			run: func(service *Service) error {
				_, err := service.List(context.Background(), ListOptions{
					ContentID: "000005021191",
					CommentID: "55x1585",
				})
				return err
			},
		},
		{
			name: "negative page",
			run: func(service *Service) error {
				_, err := service.List(context.Background(), ListOptions{
					ContentID: "000005021191",
					Page:      -1,
				})
				return err
			},
		},
		{
			name: "reply country",
			run: func(service *Service) error {
				_, err := service.Reply(context.Background(), ReplyRequest{
					ContentID:   "000005021191",
					CommentID:   "5501581",
					CountryCode: "us",
					ReplyText:   "Thanks",
				})
				return err
			},
		},
		{
			name: "blank reply",
			run: func(service *Service) error {
				_, err := service.Reply(context.Background(), ReplyRequest{
					ContentID:   "000005021191",
					CommentID:   "5501581",
					CountryCode: "USA",
					ReplyText:   " \n ",
				})
				return err
			},
		},
		{
			name: "oversized UTF-8 reply",
			run: func(service *Service) error {
				_, err := service.Reply(context.Background(), ReplyRequest{
					ContentID:   "000005021191",
					CommentID:   "5501581",
					CountryCode: "USA",
					ReplyText:   strings.Repeat("🙂", 351),
				})
				return err
			},
		},
		{
			name: "invalid UTF-8 reply",
			run: func(service *Service) error {
				_, err := service.Reply(context.Background(), ReplyRequest{
					ContentID:   "000005021191",
					CommentID:   "5501581",
					CountryCode: "USA",
					ReplyText:   string([]byte{0xff}),
				})
				return err
			},
		},
		{
			name: "delete reply ID",
			run: func(service *Service) error {
				_, err := service.DeleteReply(
					context.Background(),
					DeleteReplyRequest{ReplyID: " 252"},
				)
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
			if err := test.run(service); err == nil {
				t.Fatal("expected validation error")
			}
			if len(client.calls) != 0 {
				t.Fatalf("calls = %d, want 0", len(client.calls))
			}
		})
	}
}

func TestReplyAcceptsExactly1400UTF8Bytes(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"resultCode":"0000","resultMessage":"Ok"}`}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := ReplyRequest{
		ContentID:   "000005021191",
		CommentID:   "5501581",
		CountryCode: "USA",
		ReplyText:   strings.Repeat("🙂", 350),
	}
	if _, err := service.Reply(context.Background(), request); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
}

func TestMethodsWrapClientErrorsAndMutationsDoNotRetry(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("API unavailable")
	client := &fakeClient{err: sentinel}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := service.List(context.Background(), ListOptions{
		ContentID: "000005021191",
	}); !errors.Is(err, sentinel) {
		t.Fatalf("List error = %v, want wrapped sentinel", err)
	}
	if _, err := service.Reply(context.Background(), ReplyRequest{
		ContentID:   "000005021191",
		CommentID:   "5501581",
		CountryCode: "USA",
		ReplyText:   "Thanks",
	}); !errors.Is(err, sentinel) {
		t.Fatalf("Reply error = %v, want wrapped sentinel", err)
	}
	if _, err := service.DeleteReply(
		context.Background(),
		DeleteReplyRequest{ReplyID: "252"},
	); !errors.Is(err, sentinel) {
		t.Fatalf("DeleteReply error = %v, want wrapped sentinel", err)
	}
	if len(client.calls) != 3 {
		t.Fatalf("calls = %d, want exactly one call per method", len(client.calls))
	}
	if client.calls[1].method != http.MethodPost || client.calls[2].method != http.MethodDelete {
		t.Fatalf("mutation calls = %#v", client.calls[1:])
	}
}

func TestSharedClientDoesNotRetryReplyOrDeleteMutations(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"resultCode":"9999","resultMessage":"temporarily unavailable"}`,
				)),
				Request: request,
			}, nil
		}),
	}
	apiClient, err := samsung.NewClient(
		httpClient,
		samsung.TokenProviderFunc(func(context.Context) (string, error) {
			return "access-token", nil
		}),
		"service-account",
		samsung.WithMaxRetries(5),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	service, err := New(apiClient)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, replyErr := service.Reply(context.Background(), ReplyRequest{
		ContentID:   "000005021191",
		CommentID:   "5501581",
		CountryCode: "USA",
		ReplyText:   "Thanks",
	})
	if replyErr == nil {
		t.Fatal("Reply expected error")
	}
	_, deleteErr := service.DeleteReply(
		context.Background(),
		DeleteReplyRequest{ReplyID: "252"},
	)
	if deleteErr == nil {
		t.Fatal("DeleteReply expected error")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want exactly one per mutation", attempts)
	}
}
