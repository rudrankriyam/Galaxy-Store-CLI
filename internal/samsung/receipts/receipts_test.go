package receipts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestNewValidatesOptions(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, WithRequestTimeout(0)); err == nil {
		t.Fatal("expected invalid timeout to fail")
	}
}

func TestVerifyUsesExactUnauthenticatedRequest(t *testing.T) {
	t.Parallel()

	const purchaseID = "7efef23271b0a48746a9d7c391e367c7a802980d391d7f9b75010e8138c66c36"
	var calls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if got, want := request.Method, http.MethodGet; got != want {
			t.Fatalf("method = %q, want %q", got, want)
		}
		if got, want := request.URL.String(), VerificationURL+"?purchaseID="+purchaseID; got != want {
			t.Fatalf("URL = %q, want %q", got, want)
		}
		if got := request.URL.Query(); len(got) != 1 || got.Get("purchaseID") != purchaseID {
			t.Fatalf("query = %v, want only purchaseID", got)
		}
		if request.Body != nil && request.Body != http.NoBody {
			t.Fatal("verification request unexpectedly has a body")
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want omitted", got)
		}
		if got := request.Header.Get("service-account-id"); got != "" {
			t.Fatalf("service-account-id = %q, want omitted", got)
		}
		if got, want := request.Header.Get("Accept"), "application/json"; got != want {
			t.Fatalf("Accept = %q, want %q", got, want)
		}
		return receiptResponse(request, http.StatusOK, `{
			"itemId":"57515",
			"paymentId":"20191129013006730832TRAN",
			"orderId":"S20191129KRA1908197",
			"packageName":"com.samsung.android.test",
			"itemName":"Test Pack",
			"itemDesc":"IAP Test Item",
			"itemType":"Item",
			"countryCode":"KOR",
			"purchaseDate":"2019-11-29 01:32:41",
			"paymentAmount":"100.000",
			"status":"success",
			"paymentMethod":"Credit Card",
			"mode":"PRODUCTION",
			"consumeYN":"Y",
			"comsumeDate":"2019-11-29 01:33:28",
			"consumeDeviceModel":"SM-N960N",
			"acknowledgeYN":"Y",
			"acknowledgeDate":"2025-03-20 06:58:06",
			"acknowledgeDeviceModel":"SM-N960N",
			"currencyCode":"KRW",
			"currencyUnit":"KRW",
			"obfuscatedAccountId":"account",
			"obfuscatedProfileId":"profile"
		}`), nil
	})
	service, err := New(&http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	receipt, err := service.Verify(t.Context(), purchaseID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !receipt.Successful() || receipt.Canceled() {
		t.Fatalf("receipt status = %q", receipt.Status)
	}
	if receipt.ItemID != "57515" ||
		receipt.PackageName != "com.samsung.android.test" ||
		receipt.ConsumeDate != "2019-11-29 01:33:28" ||
		receipt.AcknowledgeYN != "Y" {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestVerifyModelsFailedAndCanceledResultsWithoutTreatingThemAsHTTPFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		response      string
		wantStatus    string
		wantErrorCode int
		wantCanceled  bool
	}{
		{
			name:          "failed",
			response:      `{"status":"fail","errorCode":9135,"errorMessage":"not exist order"}`,
			wantStatus:    StatusFail,
			wantErrorCode: 9135,
		},
		{
			name:         "canceled",
			response:     `{"status":"cancel","cancelDate":"2019-11-29 00:01:52"}`,
			wantStatus:   StatusCancel,
			wantCanceled: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := New(&http.Client{Transport: roundTripFunc(
				func(request *http.Request) (*http.Response, error) {
					return receiptResponse(request, http.StatusOK, test.response), nil
				},
			)})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			receipt, err := service.Verify(t.Context(), "purchase-id")
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if receipt.Status != test.wantStatus ||
				receipt.ErrorCode != test.wantErrorCode ||
				receipt.Canceled() != test.wantCanceled ||
				receipt.Successful() {
				t.Fatalf("receipt = %+v", receipt)
			}
		})
	}
}

func TestVerifyValidatesPurchaseIDBeforeNetwork(t *testing.T) {
	t.Parallel()

	var calls int
	service, err := New(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			calls++
			return receiptResponse(request, http.StatusOK, `{}`), nil
		},
	)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	invalid := []string{
		"",
		" ",
		" purchase-id",
		"purchase-id ",
		"purchase\nid",
		strings.Repeat("x", maxPurchaseIDBytes+1),
	}
	for _, purchaseID := range invalid {
		if _, verifyErr := service.Verify(t.Context(), purchaseID); verifyErr == nil {
			t.Fatalf("Verify(%q) expected error", purchaseID)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid IDs made %d network calls", calls)
	}
}

func TestVerifyRedactsPurchaseIDFromAllErrors(t *testing.T) {
	t.Parallel()

	const purchaseID = "private-purchase-id"
	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{
			name: "transport",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("request failed for " + purchaseID)
			},
		},
		{
			name: "HTTP response",
			transport: func(request *http.Request) (*http.Response, error) {
				return receiptResponse(
					request,
					http.StatusBadGateway,
					`{"errorCode":1000,"errorMessage":"failed for `+purchaseID+`"}`,
				), nil
			},
		},
		{
			name: "invalid JSON",
			transport: func(request *http.Request) (*http.Response, error) {
				return receiptResponse(request, http.StatusOK, `{`+purchaseID), nil
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := New(&http.Client{Transport: test.transport})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = service.Verify(t.Context(), purchaseID)
			if err == nil {
				t.Fatal("expected verification error")
			}
			if strings.Contains(err.Error(), purchaseID) {
				t.Fatalf("error leaked purchase ID: %v", err)
			}
		})
	}
}

func TestVerifyRejectsRedirectOutsideFixedAllowlistWithoutLeakingID(t *testing.T) {
	t.Parallel()

	const purchaseID = "secret-purchase-id"
	service, err := New(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location": []string{"https://example.com/steal?purchaseID=" + purchaseID},
				},
				Body:    io.NopCloser(strings.NewReader("")),
				Request: request,
			}, nil
		},
	)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = service.Verify(t.Context(), purchaseID)
	if err == nil {
		t.Fatal("expected redirect to fail")
	}
	if strings.Contains(err.Error(), purchaseID) || strings.Contains(err.Error(), "example.com") {
		t.Fatalf("redirect error leaked request data: %v", err)
	}
}

func TestVerifyHonorsContextAndConfiguredTimeout(t *testing.T) {
	t.Parallel()

	service, err := New(
		&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		WithRequestTimeout(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = service.Verify(t.Context(), "purchase-id")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Verify error = %v, want deadline exceeded", err)
	}
}

func receiptResponse(
	request *http.Request,
	statusCode int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
