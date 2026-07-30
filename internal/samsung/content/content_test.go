package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedCall struct {
	method   string
	endpoint string
	body     any
}

type fakeClient struct {
	calls             []recordedCall
	response          string
	err               error
	uploadRequestMade bool
	uploadWasStreamed bool
	uploadContentType string
	uploadBody        []byte
}

func (client *fakeClient) DoJSON(
	_ context.Context,
	method string,
	endpoint string,
	body any,
	result any,
) (*http.Response, error) {
	client.calls = append(client.calls, recordedCall{
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

func (client *fakeClient) NewUploadRequest(
	ctx context.Context,
	body io.Reader,
	contentType string,
) (*http.Request, error) {
	client.uploadRequestMade = true
	_, client.uploadWasStreamed = body.(*io.PipeReader)
	client.uploadContentType = contentType
	return http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		expectedFileUploadURL,
		io.NopCloser(body),
	)
}

func (client *fakeClient) Do(request *http.Request, result any) (*http.Response, error) {
	if client.err != nil {
		return nil, client.err
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	client.uploadBody = body
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

func TestUpdatePreservesTriStatePayloadAndUsesExactEndpoint(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"resultCode":"0000","resultMessage":"Ok"}`}
	service := mustService(t, client)
	payload := json.RawMessage(`{
		"contentId":"000007654321",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"03",
		"screenshots":null,
		"addLanguage":[],
		"sellCountryList":[{"countryCode":"USA","price":"0"}]
	}`)

	result, err := service.Update(t.Context(), "000007654321", payload)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.ResultCode != "0000" {
		t.Fatalf("result = %+v", result)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.method != http.MethodPost || call.endpoint != "/seller/contentUpdate" {
		t.Fatalf("call = %+v", call)
	}
	gotBody := compactJSON(t, call.body)
	wantBody := compactJSON(t, payload)
	if gotBody != wantBody {
		t.Fatalf("body = %s, want %s", gotBody, wantBody)
	}
	for _, fragment := range []string{
		`"screenshots":null`,
		`"addLanguage":[]`,
		`"sellCountryList":[`,
	} {
		if !strings.Contains(gotBody, fragment) {
			t.Fatalf("body = %s, want %s", gotBody, fragment)
		}
	}
}

func TestUpdateRejectsUnsafeOrIncompletePayloadBeforeTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		contentID string
		payload   string
		contains  string
	}{
		{
			name:      "legacy binary list",
			contentID: "000007654321",
			payload:   validUpdate(`"binaryList":[]`),
			contains:  "must not contain binaryList",
		},
		{
			name:      "case variant binary list",
			contentID: "000007654321",
			payload:   validUpdate(`"BinaryList":[]`),
			contains:  "must not contain binaryList",
		},
		{
			name:      "mismatched ID",
			contentID: "000007654321",
			payload: `{
				"contentId":"000000000001",
				"defaultLanguageCode":"ENG",
				"paid":"N",
				"publicationType":"01"
			}`,
			contains: "does not match",
		},
		{
			name:      "missing default language",
			contentID: "000007654321",
			payload: `{
				"contentId":"000007654321",
				"paid":"N",
				"publicationType":"01"
			}`,
			contains: "defaultLanguageCode is required",
		},
		{
			name:      "invalid paid",
			contentID: "000007654321",
			payload: `{
				"contentId":"000007654321",
				"defaultLanguageCode":"ENG",
				"paid":"false",
				"publicationType":"01"
			}`,
			contains: "paid must be Y or N",
		},
		{
			name:      "invalid publication type",
			contentID: "000007654321",
			payload: `{
				"contentId":"000007654321",
				"defaultLanguageCode":"ENG",
				"paid":"N",
				"publicationType":"04"
			}`,
			contains: "publicationType must be",
		},
		{
			name:      "not object",
			contentID: "000007654321",
			payload:   `[]`,
			contains:  "must be a JSON object",
		},
		{
			name:      "invalid content ID",
			contentID: "7654321",
			payload:   `{}`,
			contains:  "exactly 12 digits",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service := mustService(t, client)
			_, err := service.Update(t.Context(), test.contentID, json.RawMessage(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
			if len(client.calls) != 0 {
				t.Fatalf("made %d transport calls", len(client.calls))
			}
		})
	}
}

func TestSubmitAndChangeStatusUseExactRequests(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{"resultCode":"0000"}`}
	service := mustService(t, client)

	if _, err := service.Submit(t.Context(), "000007654321"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := service.ChangeStatus(t.Context(), "000007654321", " suspended "); err != nil {
		t.Fatalf("ChangeStatus: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(client.calls))
	}
	assertCall(
		t,
		client.calls[0],
		http.MethodPost,
		"/seller/contentSubmit",
		`{"contentId":"000007654321"}`,
	)
	assertCall(
		t,
		client.calls[1],
		http.MethodPost,
		"/seller/contentStatusUpdate",
		`{"contentId":"000007654321","contentStatus":"SUSPENDED"}`,
	)
}

func TestChangeStatusRejectsUnsupportedTransitionBeforeTransport(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	service := mustService(t, client)
	if _, err := service.ChangeStatus(t.Context(), "000007654321", "REGISTERING"); err == nil {
		t.Fatal("expected unsupported status to fail")
	}
	if len(client.calls) != 0 {
		t.Fatalf("made %d transport calls", len(client.calls))
	}
}

func TestBinaryMutationsUseCurrentV2EndpointAndExactShapes(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		response: `{"resultCode":"0000","resultMessage":"Ok","data":{"binarySeq":3}}`,
	}
	service := mustService(t, client)

	added, err := service.AddBinary(t.Context(), AddBinaryRequest{
		ContentID:                   "000007654321",
		FileKey:                     "5c6a365c-9713-4509-bc77-4b295a5a1d8c",
		GMS:                         "n",
		BinarySequenceForDeviceInfo: "1",
	})
	if err != nil {
		t.Fatalf("AddBinary: %v", err)
	}
	if added.Data.BinarySequence != "3" {
		t.Fatalf("binary sequence = %q, want 3", added.Data.BinarySequence)
	}

	if _, err := service.UpdateBinary(t.Context(), UpdateBinaryRequest{
		ContentID:      "000007654321",
		BinarySequence: "3",
		GMS:            "y",
	}); err != nil {
		t.Fatalf("UpdateBinary: %v", err)
	}
	if _, err := service.DeleteBinary(t.Context(), "000007654321", "2"); err != nil {
		t.Fatalf("DeleteBinary: %v", err)
	}

	if len(client.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(client.calls))
	}
	assertCall(
		t,
		client.calls[0],
		http.MethodPost,
		"/seller/v2/content/binary",
		`{
			"contentId":"000007654321",
			"filekey":"5c6a365c-9713-4509-bc77-4b295a5a1d8c",
			"gms":"N",
			"binarySeqForDeviceInfo":"1"
		}`,
	)
	assertCall(
		t,
		client.calls[1],
		http.MethodPut,
		"/seller/v2/content/binary",
		`{"contentId":"000007654321","binarySeq":"3","gms":"Y"}`,
	)
	deleteCall := client.calls[2]
	if deleteCall.method != http.MethodDelete {
		t.Fatalf("delete method = %q", deleteCall.method)
	}
	parsed, err := url.Parse(deleteCall.endpoint)
	if err != nil {
		t.Fatalf("parse delete endpoint: %v", err)
	}
	if parsed.Path != "/seller/v2/content/binary" {
		t.Fatalf("delete path = %q", parsed.Path)
	}
	if got, want := parsed.Query(), (url.Values{
		"contentId": {"000007654321"},
		"binarySeq": {"2"},
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("delete query = %v, want %v", got, want)
	}
	if deleteCall.body != nil {
		t.Fatalf("delete body = %#v, want nil", deleteCall.body)
	}
}

func TestBinaryMutationsValidateBeforeTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "add bad content ID",
			run: func(service *Service) error {
				_, err := service.AddBinary(t.Context(), AddBinaryRequest{
					ContentID: "bad",
					FileKey:   "key",
					GMS:       "N",
				})
				return err
			},
		},
		{
			name: "add missing file key",
			run: func(service *Service) error {
				_, err := service.AddBinary(t.Context(), AddBinaryRequest{
					ContentID: "000007654321",
					GMS:       "N",
				})
				return err
			},
		},
		{
			name: "add bad device sequence",
			run: func(service *Service) error {
				_, err := service.AddBinary(t.Context(), AddBinaryRequest{
					ContentID:                   "000007654321",
					FileKey:                     "key",
					GMS:                         "N",
					BinarySequenceForDeviceInfo: "one",
				})
				return err
			},
		},
		{
			name: "update bad GMS",
			run: func(service *Service) error {
				_, err := service.UpdateBinary(t.Context(), UpdateBinaryRequest{
					ContentID:      "000007654321",
					BinarySequence: "1",
					GMS:            "maybe",
				})
				return err
			},
		},
		{
			name: "delete bad sequence",
			run: func(service *Service) error {
				_, err := service.DeleteBinary(t.Context(), "000007654321", "1.5")
				return err
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeClient{}
			service := mustService(t, client)
			if err := test.run(service); err == nil {
				t.Fatal("expected validation error")
			}
			if len(client.calls) != 0 {
				t.Fatalf("made %d transport calls", len(client.calls))
			}
		})
	}
}

func TestMutationResultCodeFailureIsNotReportedAsSuccess(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		response: `{"resultCode":"3201","resultMessage":"The application status is not REGISTERING"}`,
	}
	service := mustService(t, client)
	_, err := service.Submit(t.Context(), "000007654321")
	if err == nil || !strings.Contains(err.Error(), "3201") {
		t.Fatalf("error = %v, want Samsung result code", err)
	}
}

func TestMethodsWrapTransportErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("offline")
	client := &fakeClient{err: sentinel}
	service := mustService(t, client)
	if _, err := service.Submit(t.Context(), "000007654321"); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
}

func TestCreateUploadSessionUsesExactRequestAndChecksHost(t *testing.T) {
	t.Parallel()

	client := &fakeClient{response: `{
		"url":"https://seller.samsungapps.com/galaxyapi/fileUpload",
		"sessionId":"d7ca6869-128e-4bfb-a56d-674d77f08848"
	}`}
	service := mustService(t, client)
	session, err := service.CreateUploadSession(t.Context())
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	if session.SessionID != "d7ca6869-128e-4bfb-a56d-674d77f08848" {
		t.Fatalf("session = %+v", session)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.method != http.MethodPost ||
		call.endpoint != "/seller/createUploadSessionId" ||
		call.body != nil {
		t.Fatalf("call = %+v", call)
	}

	client = &fakeClient{response: `{
		"url":"https://example.com/capture",
		"sessionId":"d7ca6869-128e-4bfb-a56d-674d77f08848"
	}`}
	service = mustService(t, client)
	if _, err := service.CreateUploadSession(t.Context()); err == nil {
		t.Fatal("expected unexpected upload host to fail")
	}
}

func TestUploadStreamsExactMultipartForm(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "app-release.aab")
	const contents = "android-app-bundle-bytes"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	client := &fakeClient{response: `{
		"fileKey":"5d33cb93-b399-41c0-9c41-667946736d09",
		"fileName":"app-release.aab",
		"fileSize":"24",
		"errorCode":null,
		"errorMsg":null
	}`}
	service := mustService(t, client)
	result, err := service.Upload(
		t.Context(),
		"d7ca6869-128e-4bfb-a56d-674d77f08848",
		path,
	)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if result.FileKey != "5d33cb93-b399-41c0-9c41-667946736d09" {
		t.Fatalf("result = %+v", result)
	}
	if !client.uploadRequestMade || !client.uploadWasStreamed {
		t.Fatalf(
			"upload request made = %v, streamed = %v",
			client.uploadRequestMade,
			client.uploadWasStreamed,
		)
	}
	if !strings.HasPrefix(client.uploadContentType, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q", client.uploadContentType)
	}

	_, parameters, err := mime.ParseMediaType(client.uploadContentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(client.uploadBody), parameters["boundary"])
	fields := make(map[string]string)
	var fileName string
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("next multipart part: %v", nextErr)
		}
		data, readErr := io.ReadAll(part)
		if readErr != nil {
			t.Fatalf("read multipart part: %v", readErr)
		}
		if part.FormName() == "file" {
			fileName = part.FileName()
			fields["file"] = string(data)
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	if fileName != "app-release.aab" {
		t.Fatalf("file name = %q", fileName)
	}
	if fields["file"] != contents {
		t.Fatalf("file contents = %q", fields["file"])
	}
	if fields["sessionId"] != "d7ca6869-128e-4bfb-a56d-674d77f08848" {
		t.Fatalf("session ID = %q", fields["sessionId"])
	}
}

func TestUploadRejectsNonRegularFilesBeforeRequest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	client := &fakeClient{}
	service := mustService(t, client)

	if _, err := service.Upload(t.Context(), "session", directory); err == nil {
		t.Fatal("expected directory upload to fail")
	}
	if client.uploadRequestMade {
		t.Fatal("directory upload created a request")
	}

	target := filepath.Join(directory, "target.aab")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(directory, "link.aab")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := service.Upload(t.Context(), "session", link); err == nil ||
		!strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
	if client.uploadRequestMade {
		t.Fatal("symlink upload created a request")
	}
}

func TestUploadRejectsSamsungBodyErrorAndMissingFileKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(path, []byte("apk"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, response := range []string{
		`{"errorCode":"7002","errorMsg":"SessionId does not exist"}`,
		`{"errorCode":null,"errorMsg":null}`,
	} {
		client := &fakeClient{response: response}
		service := mustService(t, client)
		if _, err := service.Upload(t.Context(), "session", path); err == nil {
			t.Fatalf("response %s expected error", response)
		}
	}
}

func mustService(t *testing.T, client Client) *Service {
	t.Helper()
	service, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

func assertCall(
	t *testing.T,
	call recordedCall,
	method string,
	endpoint string,
	body string,
) {
	t.Helper()
	if call.method != method || call.endpoint != endpoint {
		t.Fatalf("call = %+v, want %s %s", call, method, endpoint)
	}
	if got, want := compactJSON(t, call.body), compactJSON(t, json.RawMessage(body)); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func compactJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return compact.String()
}

func validUpdate(extra string) string {
	return `{
		"contentId":"000007654321",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		` + extra + `
	}`
}
