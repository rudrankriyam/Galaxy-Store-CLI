package shipcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/session"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

const uploadSessionLifetime = 24 * time.Hour

// OpenRemote opens an authenticated production adapter for one profile.
type OpenRemote func(profile string) (ship.Remote, error)

type appService interface {
	View(context.Context, string) ([]apps.App, error)
}

type contentService interface {
	CreateUploadSession(context.Context) (*samsungcontent.UploadSession, error)
	Upload(context.Context, string, string) (*samsungcontent.UploadResult, error)
	AddBinary(context.Context, samsungcontent.AddBinaryRequest) (*samsungcontent.AddBinaryResult, error)
	Update(context.Context, string, json.RawMessage) (*samsungcontent.Result, error)
	Submit(context.Context, string) (*samsungcontent.Result, error)
}

type remoteAdapter struct {
	apps    appService
	content contentService
	now     func() time.Time
}

// DefaultOpenRemote creates the production Galaxy Store shipping adapter
// without resolving credentials until the returned function is called.
func DefaultOpenRemote() (OpenRemote, error) {
	factory, err := session.DefaultFactory()
	if err != nil {
		return nil, err
	}
	return func(profile string) (ship.Remote, error) {
		active, openErr := factory.Open(profile)
		if openErr != nil {
			return nil, openErr
		}
		appService, appErr := apps.New(active.Client)
		if appErr != nil {
			return nil, appErr
		}
		contentService, contentErr := samsungcontent.New(active.Client)
		if contentErr != nil {
			return nil, contentErr
		}
		return &remoteAdapter{
			apps:    appService,
			content: contentService,
			now:     time.Now,
		}, nil
	}, nil
}

func (adapter *remoteAdapter) InspectRegistration(
	ctx context.Context,
	contentID string,
) (ship.RemoteState, error) {
	if adapter == nil || adapter.apps == nil {
		return ship.RemoteState{}, errors.New("shipping app service is not configured")
	}
	records, err := adapter.apps.View(ctx, contentID)
	if err != nil {
		return ship.RemoteState{}, err
	}

	var selected *apps.App
	for index := range records {
		if records[index].AppStatus != "REGISTRATION" {
			continue
		}
		if selected != nil {
			return ship.RemoteState{}, errors.New(
				"samsung returned multiple REGISTRATION records for the content ID",
			)
		}
		selected = &records[index]
	}
	if selected == nil {
		return ship.RemoteState{}, errors.New(
			"samsung returned no REGISTRATION record for the content ID",
		)
	}
	if len(selected.Raw) == 0 || !json.Valid(selected.Raw) {
		return ship.RemoteState{}, errors.New(
			"samsung returned an invalid raw REGISTRATION record",
		)
	}

	binaries := make([]ship.RemoteBinary, len(selected.Binaries))
	for index, binary := range selected.Binaries {
		binaries[index] = ship.RemoteBinary{Sequence: binary.Sequence}
	}
	return ship.RemoteState{
		ContentID:     selected.ContentID,
		AppStatus:     selected.AppStatus,
		ContentStatus: selected.ContentStatus,
		Source:        append(json.RawMessage(nil), selected.Raw...),
		Binaries:      binaries,
		ReviewState:   reviewState(selected.ContentStatus),
	}, nil
}

func (adapter *remoteAdapter) CreateUploadSession(
	ctx context.Context,
) (ship.UploadSession, error) {
	if adapter == nil || adapter.content == nil {
		return ship.UploadSession{}, errors.New("shipping content service is not configured")
	}
	if adapter.now == nil {
		return ship.UploadSession{}, errors.New("shipping clock is not configured")
	}
	result, err := adapter.content.CreateUploadSession(ctx)
	if err != nil {
		return ship.UploadSession{}, err
	}
	if result == nil || strings.TrimSpace(result.SessionID) == "" {
		return ship.UploadSession{}, errors.New(
			"create upload session: samsung returned no session ID",
		)
	}
	return ship.UploadSession{
		ID:        result.SessionID,
		ExpiresAt: adapter.now().UTC().Add(uploadSessionLifetime),
	}, nil
}

func (adapter *remoteAdapter) UploadBinary(
	ctx context.Context,
	sessionID string,
	path string,
) (ship.UploadResult, error) {
	if adapter == nil || adapter.content == nil {
		return ship.UploadResult{}, errors.New("shipping content service is not configured")
	}
	result, err := adapter.content.Upload(ctx, sessionID, path)
	if err != nil {
		return ship.UploadResult{}, err
	}
	if result == nil || strings.TrimSpace(result.FileKey) == "" {
		return ship.UploadResult{}, errors.New(
			"upload binary: samsung returned no file key",
		)
	}
	return ship.UploadResult{FileKey: result.FileKey}, nil
}

func (adapter *remoteAdapter) AddBinary(
	ctx context.Context,
	request ship.AddBinaryRequest,
) (string, error) {
	if adapter == nil || adapter.content == nil {
		return "", errors.New("shipping content service is not configured")
	}
	result, err := adapter.content.AddBinary(ctx, samsungcontent.AddBinaryRequest{
		ContentID:                   request.ContentID,
		FileKey:                     request.FileKey,
		GMS:                         request.GMS,
		BinarySequenceForDeviceInfo: request.CopyDeviceConfigurationFrom,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("add binary: samsung returned no result")
	}
	sequence := string(result.Data.BinarySequence)
	if err := validateSequence(sequence); err != nil {
		return "", fmt.Errorf("add binary: %w", err)
	}
	return sequence, nil
}

func (adapter *remoteAdapter) ApplyMetadata(
	ctx context.Context,
	contentID string,
	payload json.RawMessage,
) error {
	if adapter == nil || adapter.content == nil {
		return errors.New("shipping content service is not configured")
	}
	result, err := adapter.content.Update(ctx, contentID, payload)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("apply metadata: samsung returned no result")
	}
	return nil
}

func (adapter *remoteAdapter) SubmitReview(
	ctx context.Context,
	contentID string,
) error {
	if adapter == nil || adapter.content == nil {
		return errors.New("shipping content service is not configured")
	}
	result, err := adapter.content.Submit(ctx, contentID)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("submit review: samsung returned no result")
	}
	return nil
}

func reviewState(contentStatus string) ship.ReviewState {
	switch contentStatus {
	case "REGISTERING", "UPDATING", "RE_REGISTERING":
		return ship.ReviewStateDraft
	case "READY_FOR_REVIEW",
		"READY_TO_PRE_REVIEWS",
		"UNDER_PRE_REVIEWS",
		"PRE_REVIEWS_SUSPENDED",
		"PRE_REVIEWS_REJECTED",
		"PRE_REVIEWS_DELAYED",
		"PRE_REVIEWS_CANCELED",
		"READY_FOR_CONTENT_REVIEW",
		"UNDER_CONTENT_REVIEW",
		"CONTENT_REVIEW_REJECTED",
		"CONTENT_REVIEW_SUSPENDED",
		"CONTENT_REVIEW_DELAYED",
		"CONTENT_REVIEW_CANCELED",
		"READY_FOR_DEVICE_TEST",
		"UNDER_DEVICE_TEST",
		"DEVICE_TEST_REJECTED",
		"DEVICE_TEST_SUSPENDED",
		"DEVICE_TEST_DELAYED",
		"DEVICE_TEST_CANCELED",
		"READY_FOR_TEST_CONFIRMATION",
		"UNDER_TEST_CONFIRMATION",
		"TEST_CONFIRMATION_REJECTED",
		"TEST_CONFIRMATION_SUSPENDED",
		"TEST_CONFIRMATION_DELAYED",
		"TEST_CONFIRMATION_CANCELED",
		"READY_FOR_SALE",
		"READY_FOR_CHANGE",
		"CANCELED":
		return ship.ReviewStateSubmitted
	default:
		return ship.ReviewStateUnknown
	}
}

func validateSequence(sequence string) error {
	if sequence == "" || sequence != strings.TrimSpace(sequence) {
		return errors.New("samsung returned an invalid binary sequence")
	}
	for _, character := range sequence {
		if character < '0' || character > '9' {
			return errors.New("samsung returned an invalid binary sequence")
		}
	}
	return nil
}

var _ ship.Remote = (*remoteAdapter)(nil)
