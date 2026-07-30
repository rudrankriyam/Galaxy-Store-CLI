package shipcmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

type fakeAppService struct {
	records []apps.App
	err     error
}

func (service *fakeAppService) View(context.Context, string) ([]apps.App, error) {
	return service.records, service.err
}

type fakeContentService struct {
	session    *samsungcontent.UploadSession
	sessionErr error
	upload     *samsungcontent.UploadResult
	uploadErr  error
	add        *samsungcontent.AddBinaryResult
	addErr     error
	update     *samsungcontent.Result
	updateErr  error
	submit     *samsungcontent.Result
	submitErr  error
	addRequest samsungcontent.AddBinaryRequest
	updateID   string
	updateBody json.RawMessage
	submitID   string
	uploadID   string
	uploadPath string
}

func (service *fakeContentService) CreateUploadSession(
	context.Context,
) (*samsungcontent.UploadSession, error) {
	return service.session, service.sessionErr
}

func (service *fakeContentService) Upload(
	_ context.Context,
	sessionID string,
	path string,
) (*samsungcontent.UploadResult, error) {
	service.uploadID = sessionID
	service.uploadPath = path
	return service.upload, service.uploadErr
}

func (service *fakeContentService) AddBinary(
	_ context.Context,
	request samsungcontent.AddBinaryRequest,
) (*samsungcontent.AddBinaryResult, error) {
	service.addRequest = request
	return service.add, service.addErr
}

func (service *fakeContentService) Update(
	_ context.Context,
	contentID string,
	payload json.RawMessage,
) (*samsungcontent.Result, error) {
	service.updateID = contentID
	service.updateBody = append(json.RawMessage(nil), payload...)
	return service.update, service.updateErr
}

func (service *fakeContentService) Submit(
	_ context.Context,
	contentID string,
) (*samsungcontent.Result, error) {
	service.submitID = contentID
	return service.submit, service.submitErr
}

func TestInspectRegistrationSelectsExactVariantAndPreservesRawData(t *testing.T) {
	raw := json.RawMessage(`{
		"contentId":"000007654321",
		"appStatus":"REGISTRATION",
		"contentStatus":"REGISTERING",
		"binaryList":[{"binarySeq":"42"}],
		"futureField":{"preserved":true}
	}`)
	adapter := &remoteAdapter{
		apps: &fakeAppService{records: []apps.App{
			{
				ContentID:     "000007654321",
				AppStatus:     "SALE",
				ContentStatus: "FOR_SALE",
				Raw:           json.RawMessage(`{"appStatus":"SALE"}`),
			},
			{
				ContentID:     "000007654321",
				AppStatus:     "REGISTRATION",
				ContentStatus: "REGISTERING",
				Binaries:      []apps.Binary{{Sequence: "42"}},
				Raw:           raw,
			},
		}},
	}

	state, err := adapter.InspectRegistration(t.Context(), "000007654321")
	if err != nil {
		t.Fatalf("InspectRegistration: %v", err)
	}
	if state.ContentID != "000007654321" ||
		state.AppStatus != "REGISTRATION" ||
		state.ContentStatus != "REGISTERING" ||
		state.ReviewState != ship.ReviewStateDraft {
		t.Fatalf("state = %#v", state)
	}
	if len(state.Binaries) != 1 || state.Binaries[0].Sequence != "42" {
		t.Fatalf("binaries = %#v", state.Binaries)
	}
	if string(state.Source) != string(raw) {
		t.Fatalf("source changed:\n got %s\nwant %s", state.Source, raw)
	}

	raw[0] = '['
	if state.Source[0] != '{' {
		t.Fatal("source aliases Samsung record storage")
	}
}

func TestInspectRegistrationRejectsMissingDuplicateAndInvalidRawRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []apps.App
		want    string
	}{
		{
			name:    "missing",
			records: []apps.App{{AppStatus: "SALE", Raw: json.RawMessage(`{}`)}},
			want:    "no REGISTRATION",
		},
		{
			name: "duplicate",
			records: []apps.App{
				{AppStatus: "REGISTRATION", Raw: json.RawMessage(`{}`)},
				{AppStatus: "REGISTRATION", Raw: json.RawMessage(`{}`)},
			},
			want: "multiple REGISTRATION",
		},
		{
			name:    "missing raw",
			records: []apps.App{{AppStatus: "REGISTRATION"}},
			want:    "invalid raw",
		},
		{
			name: "invalid raw",
			records: []apps.App{{
				AppStatus: "REGISTRATION",
				Raw:       json.RawMessage(`{"contentId":`),
			}},
			want: "invalid raw",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := &remoteAdapter{
				apps: &fakeAppService{records: test.records},
			}
			_, err := adapter.InspectRegistration(t.Context(), "000007654321")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestInspectRegistrationPropagatesViewError(t *testing.T) {
	want := errors.New("view failed")
	adapter := &remoteAdapter{
		apps: &fakeAppService{err: want},
	}
	_, err := adapter.InspectRegistration(t.Context(), "000007654321")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestReviewStateIsConservative(t *testing.T) {
	tests := []struct {
		status string
		want   ship.ReviewState
	}{
		{status: "REGISTERING", want: ship.ReviewStateDraft},
		{status: "READY_FOR_REVIEW", want: ship.ReviewStateSubmitted},
		{status: "UNDER_CONTENT_REVIEW", want: ship.ReviewStateSubmitted},
		{status: "DEVICE_TEST_REJECTED", want: ship.ReviewStateSubmitted},
		{status: "READY_FOR_SALE", want: ship.ReviewStateSubmitted},
		{status: "UPDATING", want: ship.ReviewStateDraft},
		{status: "RE_REGISTERING", want: ship.ReviewStateDraft},
		{status: "BETA_DEPLOYED", want: ship.ReviewStateUnknown},
		{status: "SAMSUNG_NEW_STATE", want: ship.ReviewStateUnknown},
		{status: "", want: ship.ReviewStateUnknown},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if got := reviewState(test.status); got != test.want {
				t.Fatalf("reviewState(%q) = %q, want %q", test.status, got, test.want)
			}
		})
	}
}

func TestRemoteAdapterTranslatesContentOperations(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.FixedZone("test", 5*60*60+30*60))
	add := &samsungcontent.AddBinaryResult{}
	add.Data.BinarySequence = samsungcontent.BinarySequence("987")
	content := &fakeContentService{
		session: &samsungcontent.UploadSession{SessionID: "session-id"},
		upload:  &samsungcontent.UploadResult{FileKey: "file-key"},
		add:     add,
		update:  &samsungcontent.Result{},
		submit:  &samsungcontent.Result{},
	}
	adapter := &remoteAdapter{content: content, now: func() time.Time { return now }}

	session, err := adapter.CreateUploadSession(t.Context())
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	wantExpiry := now.UTC().Add(24 * time.Hour)
	if session.ID != "session-id" || !session.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("session = %#v, want expiry %s", session, wantExpiry)
	}

	upload, err := adapter.UploadBinary(t.Context(), "session-id", "/tmp/app.aab")
	if err != nil {
		t.Fatalf("UploadBinary: %v", err)
	}
	if upload.FileKey != "file-key" ||
		content.uploadID != "session-id" ||
		content.uploadPath != "/tmp/app.aab" {
		t.Fatalf("upload = %#v; service = %#v", upload, content)
	}

	sequence, err := adapter.AddBinary(t.Context(), ship.AddBinaryRequest{
		ContentID:                   "000007654321",
		FileKey:                     "file-key",
		GMS:                         "Y",
		CopyDeviceConfigurationFrom: "123",
	})
	if err != nil {
		t.Fatalf("AddBinary: %v", err)
	}
	if sequence != "987" {
		t.Fatalf("sequence = %q", sequence)
	}
	wantAdd := samsungcontent.AddBinaryRequest{
		ContentID:                   "000007654321",
		FileKey:                     "file-key",
		GMS:                         "Y",
		BinarySequenceForDeviceInfo: "123",
	}
	if content.addRequest != wantAdd {
		t.Fatalf("add request = %#v, want %#v", content.addRequest, wantAdd)
	}

	payload := json.RawMessage(`{"contentId":"000007654321"}`)
	if err := adapter.ApplyMetadata(t.Context(), "000007654321", payload); err != nil {
		t.Fatalf("ApplyMetadata: %v", err)
	}
	if content.updateID != "000007654321" ||
		string(content.updateBody) != string(payload) {
		t.Fatalf("update = %q %s", content.updateID, content.updateBody)
	}

	if err := adapter.SubmitReview(t.Context(), "000007654321"); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if content.submitID != "000007654321" {
		t.Fatalf("submit content ID = %q", content.submitID)
	}
}

func TestRemoteAdapterRejectsInvalidMutationResults(t *testing.T) {
	now := time.Now
	tests := []struct {
		name string
		call func(*remoteAdapter) error
		want string
	}{
		{
			name: "missing session",
			call: func(adapter *remoteAdapter) error {
				_, err := adapter.CreateUploadSession(t.Context())
				return err
			},
			want: "no session ID",
		},
		{
			name: "missing file key",
			call: func(adapter *remoteAdapter) error {
				_, err := adapter.UploadBinary(t.Context(), "session", "app.aab")
				return err
			},
			want: "no file key",
		},
		{
			name: "missing binary result",
			call: func(adapter *remoteAdapter) error {
				_, err := adapter.AddBinary(t.Context(), ship.AddBinaryRequest{})
				return err
			},
			want: "no result",
		},
		{
			name: "missing update result",
			call: func(adapter *remoteAdapter) error {
				return adapter.ApplyMetadata(t.Context(), "000007654321", json.RawMessage(`{}`))
			},
			want: "no result",
		},
		{
			name: "missing submit result",
			call: func(adapter *remoteAdapter) error {
				return adapter.SubmitReview(t.Context(), "000007654321")
			},
			want: "no result",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := &remoteAdapter{
				content: &fakeContentService{},
				now:     now,
			}
			err := test.call(adapter)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestAddBinaryRejectsInvalidSequenceAndPropagatesServiceErrors(t *testing.T) {
	add := &samsungcontent.AddBinaryResult{}
	add.Data.BinarySequence = samsungcontent.BinarySequence("not-digits")
	adapter := &remoteAdapter{content: &fakeContentService{add: add}}
	if _, err := adapter.AddBinary(t.Context(), ship.AddBinaryRequest{}); err == nil ||
		!strings.Contains(err.Error(), "invalid binary sequence") {
		t.Fatalf("invalid sequence error = %v", err)
	}

	want := errors.New("add failed")
	adapter.content = &fakeContentService{addErr: want}
	if _, err := adapter.AddBinary(t.Context(), ship.AddBinaryRequest{}); !errors.Is(err, want) {
		t.Fatalf("service error = %v, want %v", err, want)
	}
}
