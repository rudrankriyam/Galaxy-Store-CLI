// Package apps provides read-only access to apps registered in Galaxy Store
// Seller Portal.
package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	contentListPath = "/seller/contentList"
	contentInfoPath = "/seller/contentInfo"
)

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(
		context.Context,
		string,
		string,
		any,
		any,
	) (*http.Response, error)
}

// Service reads Galaxy Store app records.
type Service struct {
	client JSONClient
}

// New creates an app service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("Galaxy Store client is required")
	}
	return &Service{client: client}, nil
}

// App is the stable subset shared by contentList and contentInfo. Raw preserves
// the complete Samsung object for lossless JSON output.
type App struct {
	ContentID     string          `json:"contentId"`
	AppStatus     string          `json:"appStatus,omitempty"`
	ContentStatus string          `json:"contentStatus,omitempty"`
	Title         string          `json:"title,omitempty"`
	PackageName   string          `json:"packageName,omitempty"`
	Binaries      []Binary        `json:"binaries,omitempty"`
	Raw           json.RawMessage `json:"-"`

	// UnknownFields supports typed inspection without discarding API drift.
	UnknownFields map[string]json.RawMessage `json:"-"`
}

// Binary is the stable read-only projection of an entry returned in
// contentInfo.binaryList. The field remains valid in responses even though
// sending binaryList to contentUpdate is forbidden.
type Binary struct {
	Sequence    string `json:"binarySeq,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	VersionCode string `json:"versionCode,omitempty"`
	VersionName string `json:"versionName,omitempty"`
	PackageName string `json:"packageName,omitempty"`
}

// UnmarshalJSON accepts the contentName returned by contentList and appTitle
// returned by contentInfo while preserving fields the package does not model.
func (app *App) UnmarshalJSON(data []byte) error {
	raw := append(json.RawMessage(nil), data...)
	*app = App{Raw: raw}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	contentID, err := stringField(fields["contentId"])
	if err != nil {
		return fmt.Errorf("decode contentId: %w", err)
	}
	appStatus, err := stringField(fields["appStatus"])
	if err != nil {
		return fmt.Errorf("decode appStatus: %w", err)
	}
	contentStatus, err := stringField(fields["contentStatus"])
	if err != nil {
		return fmt.Errorf("decode contentStatus: %w", err)
	}
	appTitle, err := stringField(fields["appTitle"])
	if err != nil {
		return fmt.Errorf("decode appTitle: %w", err)
	}
	contentName, err := stringField(fields["contentName"])
	if err != nil {
		return fmt.Errorf("decode contentName: %w", err)
	}
	packageName, err := stringField(fields["packageName"])
	if err != nil {
		return fmt.Errorf("decode packageName: %w", err)
	}

	app.ContentID = contentID
	app.AppStatus = appStatus
	app.ContentStatus = contentStatus
	app.Title = appTitle
	if app.Title == "" {
		app.Title = contentName
	}
	app.PackageName = packageName
	if rawBinaries := fields["binaryList"]; len(rawBinaries) != 0 &&
		!bytes.Equal(bytes.TrimSpace(rawBinaries), []byte("null")) {
		if err := json.Unmarshal(rawBinaries, &app.Binaries); err != nil {
			return fmt.Errorf("decode binaryList: %w", err)
		}
		if app.PackageName == "" {
			app.PackageName = commonPackageName(app.Binaries)
		}
	}

	for _, known := range []string{
		"contentId",
		"appStatus",
		"contentStatus",
		"appTitle",
		"contentName",
		"packageName",
		"binaryList",
	} {
		delete(fields, known)
	}
	if len(fields) == 0 {
		app.UnknownFields = nil
	} else {
		app.UnknownFields = fields
	}
	return nil
}

// MarshalJSON emits Samsung's original app object when available. This keeps
// unknown response fields lossless for automation while typed fields power
// table output and validation.
func (app App) MarshalJSON() ([]byte, error) {
	if len(app.Raw) != 0 {
		if !json.Valid(app.Raw) {
			return nil, errors.New("app raw response is invalid JSON")
		}
		return append([]byte(nil), app.Raw...), nil
	}
	type appAlias App
	return json.Marshal(appAlias(app))
}

// ListOptions selects a local page from contentList. Samsung's contentList
// endpoint has no server-side pagination parameters and always returns the
// complete seller app list, so Offset and Limit are intentionally not sent as
// query parameters.
type ListOptions struct {
	Offset int
	Limit  int
}

// Pagination describes the local page selected from Samsung's complete
// contentList response.
type Pagination struct {
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	Total      int  `json:"total"`
	HasMore    bool `json:"hasMore"`
	NextOffset int  `json:"nextOffset,omitempty"`
}

// ListResult contains one local page of seller apps.
type ListResult struct {
	Apps       []App      `json:"apps"`
	Pagination Pagination `json:"pagination"`
}

// List calls contentList once and returns the requested local page.
func (service *Service) List(ctx context.Context, options ListOptions) (*ListResult, error) {
	if options.Offset < 0 {
		return nil, errors.New("list offset cannot be negative")
	}
	if options.Limit < 0 {
		return nil, errors.New("list limit cannot be negative")
	}

	var allApps []App
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		contentListPath,
		nil,
		&allApps,
	); err != nil {
		return nil, fmt.Errorf("list Galaxy Store apps: %w", err)
	}

	start := min(options.Offset, len(allApps))
	end := len(allApps)
	if options.Limit > 0 {
		end = min(start+options.Limit, len(allApps))
	}

	pageApps := make([]App, end-start)
	copy(pageApps, allApps[start:end])
	limit := options.Limit
	if limit == 0 {
		limit = len(pageApps)
	}
	result := &ListResult{
		Apps: pageApps,
		Pagination: Pagination{
			Offset:  options.Offset,
			Limit:   limit,
			Total:   len(allApps),
			HasMore: end < len(allApps),
		},
	}
	if result.Pagination.HasMore {
		result.Pagination.NextOffset = end
	}
	return result, nil
}

// View returns every contentInfo record for an exact 12-digit content ID.
// Samsung can return distinct SALE and REGISTRATION records simultaneously;
// callers must not silently choose one.
func (service *Service) View(ctx context.Context, contentID string) ([]App, error) {
	if err := ValidateContentID(contentID); err != nil {
		return nil, err
	}

	query := make(url.Values)
	query.Set("contentId", contentID)

	var records []App
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		contentInfoPath+"?"+query.Encode(),
		nil,
		&records,
	); err != nil {
		return nil, fmt.Errorf("get Galaxy Store app: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("get Galaxy Store app: Samsung returned no app")
	}
	for _, record := range records {
		if record.ContentID != "" && record.ContentID != contentID {
			return nil, errors.New("get Galaxy Store app: Samsung returned a different content ID")
		}
	}
	return records, nil
}

// ValidateContentID requires Samsung's exact 12-digit content ID format.
func ValidateContentID(contentID string) error {
	if contentID != strings.TrimSpace(contentID) || len(contentID) != 12 {
		return errors.New("content ID must contain exactly 12 digits")
	}
	for _, character := range contentID {
		if character < '0' || character > '9' {
			return errors.New("content ID must contain exactly 12 digits")
		}
	}
	return nil
}

func stringField(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err == nil {
		return number.String(), nil
	}
	return "", errors.New("expected a string or number")
}

func commonPackageName(binaries []Binary) string {
	var common string
	for _, binary := range binaries {
		name := strings.TrimSpace(binary.PackageName)
		if name == "" {
			continue
		}
		if common == "" {
			common = name
			continue
		}
		if name != common {
			return ""
		}
	}
	return common
}
