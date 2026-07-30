package rollout

import (
	"context"
	"encoding/json"
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

func TestViewRateUsesExactRequestAndResponse(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{`{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{
			"rolloutRate":30,
			"countries":[
				{"countryCode":"USA","rolloutRate":35},
				{"countryCode":"KOR","rolloutRate":40}
			]
		}
	}`}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.ViewRate(context.Background(), "000007654321", "REGISTRATION")
	if err != nil {
		t.Fatalf("ViewRate: %v", err)
	}
	if got, want := client.calls, []call{{
		method:   http.MethodGet,
		endpoint: "/seller/v2/content/stagedRolloutRate?appStatus=REGISTRATION&contentId=000007654321",
		body:     nil,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	if result.RolloutRate != 30 || !reflect.DeepEqual(result.Countries, []CountryRate{
		{CountryCode: "USA", RolloutRate: 35},
		{CountryCode: "KOR", RolloutRate: 40},
	}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestSetRateReadsThenSendsExactMonotonicRequest(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{
		`{"resultCode":"0000","resultMessage":"Ok","data":{"rolloutRate":30,"countries":[{"countryCode":"USA","rolloutRate":35}]}}`,
		`{"resultCode":"0000","resultMessage":"Ok","data":{}}`,
	}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.SetRate(context.Background(), SetRateInput{
		ContentID:   "000007654321",
		AppStatus:   "SALE",
		RolloutRate: 40,
		Countries: []CountryRate{
			{CountryCode: "USA", RolloutRate: 45},
			{CountryCode: "KOR", RolloutRate: 50},
		},
	})
	if err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want GET then PUT", len(client.calls))
	}
	if client.calls[0].method != http.MethodGet ||
		client.calls[0].endpoint != "/seller/v2/content/stagedRolloutRate?appStatus=SALE&contentId=000007654321" {
		t.Fatalf("read call = %#v", client.calls[0])
	}
	if client.calls[1].method != http.MethodPut || client.calls[1].endpoint != ratePath {
		t.Fatalf("write call = %#v", client.calls[1])
	}
	encoded, marshalErr := json.Marshal(client.calls[1].body)
	if marshalErr != nil {
		t.Fatalf("Marshal body: %v", marshalErr)
	}
	wantBody := `{"contentId":"000007654321","function":"ENABLE_ROLLOUT","appStatus":"SALE","rolloutRate":40,"countries":[{"countryCode":"USA","rolloutRate":45},{"countryCode":"KOR","rolloutRate":50}]}`
	if string(encoded) != wantBody {
		t.Fatalf("body = %s, want %s", encoded, wantBody)
	}
	if result.Function != functionEnable || result.Completed {
		t.Fatalf("result = %#v", result)
	}
}

func TestSetRateRejectsDefaultAndCountryDecreasesAfterRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     SetRateInput
		response  string
		wantError string
	}{
		{
			name: "default equal",
			input: SetRateInput{
				ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 30,
			},
			response:  `{"resultCode":"0000","data":{"rolloutRate":30}}`,
			wantError: "must increase",
		},
		{
			name: "default decrease",
			input: SetRateInput{
				ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 20,
			},
			response:  `{"resultCode":"0000","data":{"rolloutRate":30}}`,
			wantError: "must increase",
		},
		{
			name: "country equal",
			input: SetRateInput{
				ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 40,
				Countries: []CountryRate{{CountryCode: "USA", RolloutRate: 45}},
			},
			response:  `{"resultCode":"0000","data":{"rolloutRate":30,"countries":[{"countryCode":"USA","rolloutRate":45}]}}`,
			wantError: "USA must increase",
		},
		{
			name: "country not above advancing default",
			input: SetRateInput{
				ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 40,
				Countries: []CountryRate{{CountryCode: "KOR", RolloutRate: 40}},
			},
			response:  `{"resultCode":"0000","data":{"rolloutRate":30}}`,
			wantError: "greater than the default",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{responses: []string{test.response}}
			service, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = service.SetRate(context.Background(), test.input)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("SetRate error = %v, want containing %q", err, test.wantError)
			}
			if len(client.calls) != 1 || client.calls[0].method != http.MethodGet {
				t.Fatalf("calls = %#v, want one safety read and no mutation", client.calls)
			}
		})
	}
}

func TestCompleteMeansGlobalDeploymentAndUsesExactRequest(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{
		`{"resultCode":"0000","resultMessage":"Ok","data":{"rolloutRate":50,"countries":[{"countryCode":"USA","rolloutRate":50}]}}`,
		`{"resultCode":"0000","resultMessage":"Ok","data":{}}`,
	}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.Complete(context.Background(), "000007654321", "SALE")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Completed || result.Function != functionDisable {
		t.Fatalf("result = %#v, want completed DISABLE_ROLLOUT", result)
	}
	if len(client.calls) != 2 || client.calls[0].method != http.MethodGet || client.calls[1].method != http.MethodPut {
		t.Fatalf("calls = %#v, want GET then PUT", client.calls)
	}
	encoded, marshalErr := json.Marshal(client.calls[1].body)
	if marshalErr != nil {
		t.Fatalf("Marshal body: %v", marshalErr)
	}
	wantBody := `{"contentId":"000007654321","function":"DISABLE_ROLLOUT","appStatus":"SALE"}`
	if string(encoded) != wantBody {
		t.Fatalf("body = %s, want %s", encoded, wantBody)
	}
}

func TestCompleteRejectsCountryRateAboveDefaultBeforeMutation(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{
		`{"resultCode":"0000","data":{"rolloutRate":50,"countries":[{"countryCode":"USA","rolloutRate":60}]}}`,
	}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = service.Complete(context.Background(), "000007654321", "SALE")
	if err == nil || !strings.Contains(err.Error(), "cannot complete staged rollout") {
		t.Fatalf("Complete error = %v", err)
	}
	if len(client.calls) != 1 || client.calls[0].method != http.MethodGet {
		t.Fatalf("calls = %#v, want safety read only", client.calls)
	}
}

func TestViewBinariesUsesExactRequestAndResponse(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{`{
		"resultCode":"0000",
		"resultMessage":"Ok",
		"data":{"binaries":[{
			"seq":1,
			"versionCode":"10202",
			"versionName":"1.2.2",
			"fileName":"App.apk",
			"fileSize":"7.04 MB",
			"rolloutStatus":"ENABLED",
			"appStatus":"SALE"
		}]}
	}`}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := service.ViewBinaries(context.Background(), "000007654321", "SALE")
	if err != nil {
		t.Fatalf("ViewBinaries: %v", err)
	}
	if got := client.calls[0]; got.method != http.MethodGet ||
		got.endpoint != "/seller/v2/content/stagedRolloutBinary?appStatus=SALE&contentId=000007654321" ||
		got.body != nil {
		t.Fatalf("call = %#v", got)
	}
	if len(result.Binaries) != 1 ||
		result.Binaries[0].Sequence != 1 ||
		result.Binaries[0].RolloutStatus != "ENABLED" ||
		result.Binaries[0].AppStatus != "SALE" {
		t.Fatalf("result = %#v", result)
	}
}

func TestAddAndRemoveBinaryUseExactMutationBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		function string
		call     func(*Service) (*MutationResult, error)
	}{
		{
			name:     "add",
			function: functionAdd,
			call: func(service *Service) (*MutationResult, error) {
				return service.AddBinary(context.Background(), "000007654321", "1")
			},
		},
		{
			name:     "remove",
			function: functionRemove,
			call: func(service *Service) (*MutationResult, error) {
				return service.RemoveBinary(context.Background(), "000007654321", "1")
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
			result, err := test.call(service)
			if err != nil {
				t.Fatalf("binary mutation: %v", err)
			}
			if result.Function != test.function {
				t.Fatalf("result = %#v", result)
			}
			if len(client.calls) != 1 || client.calls[0].method != http.MethodPut || client.calls[0].endpoint != binaryPath {
				t.Fatalf("calls = %#v", client.calls)
			}
			encoded, marshalErr := json.Marshal(client.calls[0].body)
			if marshalErr != nil {
				t.Fatalf("Marshal body: %v", marshalErr)
			}
			wantBody := `{"contentId":"000007654321","function":"` + test.function + `","binarySeq":"1"}`
			if string(encoded) != wantBody {
				t.Fatalf("body = %s, want %s", encoded, wantBody)
			}
		})
	}
}

func TestRejectsNonSuccessSamsungResultOnHTTP200(t *testing.T) {
	t.Parallel()

	client := &fakeClient{responses: []string{
		`{"resultCode":"3103","resultMessage":"rollout rate cannot be reduced","data":{}}`,
	}}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = service.ViewRate(context.Background(), "000007654321", "SALE")
	if err == nil || !strings.Contains(err.Error(), "samsung result 3103") {
		t.Fatalf("ViewRate error = %v", err)
	}
}

func TestValidatesExactIdentifiersStatusesAndRatesBeforeRequests(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, input := range []SetRateInput{
		{ContentID: "123", AppStatus: "SALE", RolloutRate: 10},
		{ContentID: "000007654321", AppStatus: "", RolloutRate: 10},
		{ContentID: "000007654321", AppStatus: "sale", RolloutRate: 10},
		{ContentID: "000007654321", AppStatus: " SALE", RolloutRate: 10},
		{ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 0},
		{ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 101},
		{
			ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 10,
			Countries: []CountryRate{{CountryCode: "us", RolloutRate: 20}},
		},
		{
			ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 10,
			Countries: []CountryRate{
				{CountryCode: "USA", RolloutRate: 20},
				{CountryCode: "USA", RolloutRate: 30},
			},
		},
	} {
		if _, err := service.SetRate(context.Background(), input); err == nil {
			t.Fatalf("SetRate(%+v) expected error", input)
		}
	}
	for _, sequence := range []string{"", "0", "-1", "1.5", " 1"} {
		if _, err := service.AddBinary(context.Background(), "000007654321", sequence); err == nil {
			t.Fatalf("AddBinary(%q) expected error", sequence)
		}
	}
	if _, err := service.ViewBinaries(context.Background(), "000007654321", "REGISTRATION "); err == nil {
		t.Fatal("ViewBinaries(padded status) expected error")
	}
	if len(client.calls) != 0 {
		t.Fatalf("invalid input made %d API calls", len(client.calls))
	}
}

func TestRateMutationIsNotRetried(t *testing.T) {
	t.Parallel()

	transport := &sequenceTransport{}
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
	if _, err := service.SetRate(context.Background(), SetRateInput{
		ContentID: "000007654321", AppStatus: "SALE", RolloutRate: 40,
	}); err == nil {
		t.Fatal("SetRate expected API error")
	}
	if got, want := transport.methods, []string{http.MethodGet, http.MethodPut}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP attempts = %v, want %v", got, want)
	}
}

type sequenceTransport struct {
	methods []string
}

func (transport *sequenceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.methods = append(transport.methods, request.Method)
	status := http.StatusOK
	body := `{"resultCode":"0000","resultMessage":"Ok","data":{"rolloutRate":30,"countries":[]}}`
	if request.Method == http.MethodPut {
		status = http.StatusServiceUnavailable
		body = `{"resultCode":"9000","resultMessage":"unavailable"}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}
