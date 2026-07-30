// Package metadata provides the lossless, filesystem-backed metadata workflow
// used to plan Galaxy Store contentUpdate requests.
package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

const (
	// SchemaVersion is the current on-disk metadata bundle schema.
	SchemaVersion = 1

	// ManifestFilename, MetadataFilename, and SourceFilename are the three
	// files that comprise a metadata bundle.
	ManifestFilename = "manifest.json"
	MetadataFilename = "metadata.json"
	SourceFilename   = "source.json"
)

// AppStatus identifies the independently editable Samsung contentInfo variant.
type AppStatus string

const (
	AppStatusSale         AppStatus = "SALE"
	AppStatusRegistration AppStatus = "REGISTRATION"
)

// Manifest binds editable metadata to the exact contentInfo variant from which
// it was pulled.
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	ContentID     string    `json:"contentId"`
	AppStatus     AppStatus `json:"appStatus"`
	ContentStatus string    `json:"contentStatus,omitempty"`
	PulledAt      string    `json:"pulledAt"`
	SourceSHA256  string    `json:"sourceSha256"`
}

// Bundle is the lossless source snapshot, its safe editable update envelope,
// and the manifest that binds the two together.
type Bundle struct {
	Manifest Manifest
	Metadata json.RawMessage
	Source   json.RawMessage
}

var (
	// ErrDrift indicates that live contentInfo data no longer matches the
	// source snapshot recorded in a bundle.
	ErrDrift = errors.New("metadata source has drifted")

	// ErrOverwrite indicates that a bundle already exists and explicit
	// overwrite permission was not supplied.
	ErrOverwrite = errors.New("metadata bundle already exists")
)

var allowedUpdateFields = map[string]struct{}{
	"addLanguage":          {},
	"ageLimit":             {},
	"appTitle":             {},
	"autoAddCountry":       {},
	"chinaAgeLimit":        {},
	"contentId":            {},
	"copyrightHolder":      {},
	"defaultLanguageCode":  {},
	"edgescreen":           {},
	"edgescreenKey":        {},
	"edgescreenplus":       {},
	"edgescreenplusKey":    {},
	"heroImage":            {},
	"heroImageKey":         {},
	"icon":                 {},
	"iconKey":              {},
	"longDescription":      {},
	"miitData":             {},
	"newFeature":           {},
	"notifyResult":         {},
	"openSourceURL":        {},
	"paid":                 {},
	"privatePolicyURL":     {},
	"privatePolicyURLYN":   {},
	"publicationType":      {},
	"reviewComment":        {},
	"reviewFilekey":        {},
	"reviewFilename":       {},
	"screenshots":          {},
	"sellCountryList":      {},
	"shortDescription":     {},
	"standardPrice":        {},
	"startPublicationDate": {},
	"stopPublicationDate":  {},
	"supportEMail":         {},
	"supportedLanguages":   {},
	"supportedSiteUrl":     {},
	"usExportLaws":         {},
	"youTubeURL":           {},
}

// AllowedUpdateFields returns a sorted copy of the Samsung contentUpdate
// fields accepted by the compiler and validator.
func AllowedUpdateFields() []string {
	fields := make([]string, 0, len(allowedUpdateFields))
	for field := range allowedUpdateFields {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	return fields
}

// SelectRecord selects exactly one SALE or REGISTRATION contentInfo record.
// When more than one record exists, status is required rather than guessed.
func SelectRecord(
	records []apps.App,
	contentID string,
	status AppStatus,
) (apps.App, error) {
	if err := validateContentID(contentID); err != nil {
		return apps.App{}, err
	}
	normalizedStatus, err := normalizeAppStatus(status, status == "")
	if err != nil {
		return apps.App{}, err
	}

	matches := make([]apps.App, 0, len(records))
	for _, record := range records {
		if record.ContentID != contentID {
			continue
		}
		recordStatus, statusErr := normalizeAppStatus(AppStatus(record.AppStatus), false)
		if statusErr != nil {
			return apps.App{}, fmt.Errorf(
				"contentInfo record for %s: %w",
				contentID,
				statusErr,
			)
		}
		if normalizedStatus == "" || recordStatus == normalizedStatus {
			matches = append(matches, record)
		}
	}

	switch len(matches) {
	case 0:
		if normalizedStatus == "" {
			return apps.App{}, fmt.Errorf(
				"no contentInfo record found for content ID %s",
				contentID,
			)
		}
		return apps.App{}, fmt.Errorf(
			"no %s contentInfo record found for content ID %s",
			normalizedStatus,
			contentID,
		)
	case 1:
		return matches[0], nil
	default:
		if normalizedStatus == "" {
			return apps.App{}, errors.New(
				"multiple contentInfo records found; app status SALE or REGISTRATION is required",
			)
		}
		return apps.App{}, fmt.Errorf(
			"multiple %s contentInfo records found for content ID %s",
			normalizedStatus,
			contentID,
		)
	}
}

// NewBundle creates the canonical three-file bundle for a selected record.
func NewBundle(record apps.App, pulledAt time.Time) (*Bundle, error) {
	status, err := normalizeAppStatus(AppStatus(record.AppStatus), false)
	if err != nil {
		return nil, err
	}
	if err := validateContentID(record.ContentID); err != nil {
		return nil, err
	}

	source := append(json.RawMessage(nil), record.Raw...)
	if len(bytes.TrimSpace(source)) == 0 {
		encoded, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode contentInfo source: %w", marshalErr)
		}
		source = encoded
	}
	sourceFields, err := decodeObject(source, "contentInfo source")
	if err != nil {
		return nil, err
	}
	sourceContentID, err := requiredString(sourceFields, "contentId")
	if err != nil {
		return nil, fmt.Errorf("contentInfo source: %w", err)
	}
	if sourceContentID != record.ContentID {
		return nil, errors.New("contentInfo source contentId does not match selected record")
	}
	sourceStatus, err := requiredString(sourceFields, "appStatus")
	if err != nil {
		return nil, fmt.Errorf("contentInfo source: %w", err)
	}
	if AppStatus(sourceStatus) != status {
		return nil, errors.New("contentInfo source appStatus does not match selected record")
	}

	envelope, err := Compile(source)
	if err != nil {
		return nil, err
	}
	hash, err := CanonicalSHA256(source)
	if err != nil {
		return nil, err
	}

	contentStatus, err := optionalString(sourceFields, "contentStatus")
	if err != nil {
		return nil, fmt.Errorf("contentInfo source: %w", err)
	}
	return &Bundle{
		Manifest: Manifest{
			SchemaVersion: SchemaVersion,
			ContentID:     record.ContentID,
			AppStatus:     status,
			ContentStatus: contentStatus,
			PulledAt:      pulledAt.UTC().Format(time.RFC3339Nano),
			SourceSHA256:  hash,
		},
		Metadata: envelope,
		Source:   source,
	}, nil
}

// Compile creates a deterministic contentUpdate envelope by copying only
// fields explicitly documented for that request. Response-only fields,
// category, binaryList, and unknown future fields remain only in source.json.
func Compile(source json.RawMessage) (json.RawMessage, error) {
	fields, err := decodeObject(source, "contentInfo source")
	if err != nil {
		return nil, err
	}

	envelope := make(map[string]json.RawMessage)
	for field, value := range fields {
		if _, allowed := allowedUpdateFields[field]; allowed {
			envelope[field] = append(json.RawMessage(nil), value...)
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode metadata envelope: %w", err)
	}
	if err := ValidateEnvelope("", encoded); err != nil {
		return nil, fmt.Errorf("compile contentInfo source: %w", err)
	}
	return encoded, nil
}

// ValidateEnvelope validates the safe, editable contentUpdate contract.
// expectedContentID may be empty when only internal consistency is required.
func ValidateEnvelope(expectedContentID string, envelope json.RawMessage) error {
	fields, err := decodeObject(envelope, "metadata envelope")
	if err != nil {
		return err
	}
	for field := range fields {
		if _, allowed := allowedUpdateFields[field]; !allowed {
			if strings.EqualFold(field, "binaryList") {
				return errors.New(
					"metadata envelope must not contain binaryList; use the v2 binary endpoints",
				)
			}
			return fmt.Errorf("metadata envelope contains unsupported field %q", field)
		}
	}

	contentID, err := requiredString(fields, "contentId")
	if err != nil {
		return err
	}
	if err := validateContentID(contentID); err != nil {
		return fmt.Errorf("metadata envelope contentId: %w", err)
	}
	if expectedContentID != "" && contentID != expectedContentID {
		return errors.New("metadata envelope contentId does not match the expected content ID")
	}
	if _, err := requiredString(fields, "defaultLanguageCode"); err != nil {
		return err
	}
	paid, err := requiredString(fields, "paid")
	if err != nil {
		return err
	}
	if paid != "Y" && paid != "N" {
		return errors.New("metadata envelope paid must be Y or N")
	}
	publicationType, err := requiredString(fields, "publicationType")
	if err != nil {
		return err
	}
	switch publicationType {
	case "01", "02", "03":
	default:
		return errors.New("metadata envelope publicationType must be 01, 02, or 03")
	}

	if err := validateObjectArray(
		fields,
		"addLanguage",
		[]string{"languagecode", "description", "appTitle"},
	); err != nil {
		return err
	}
	if err := validateObjectArray(fields, "screenshots", []string{"reuseYn"}); err != nil {
		return err
	}
	if err := validateObjectArray(
		fields,
		"sellCountryList",
		[]string{"countryCode"},
	); err != nil {
		return err
	}
	if err := validateLocalizedScreenshots(fields["addLanguage"]); err != nil {
		return err
	}
	return nil
}

// CanonicalSHA256 hashes JSON after deterministic object-key canonicalization.
// Formatting and object key order therefore do not create false drift.
func CanonicalSHA256(value json.RawMessage) (string, error) {
	canonical, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyDrift compares a freshly fetched contentInfo object with a bundle's
// canonical source hash and selected record identity.
func VerifyDrift(manifest Manifest, currentSource json.RawMessage) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	fields, err := decodeObject(currentSource, "current contentInfo source")
	if err != nil {
		return err
	}
	contentID, err := requiredString(fields, "contentId")
	if err != nil {
		return fmt.Errorf("current contentInfo source: %w", err)
	}
	status, err := requiredString(fields, "appStatus")
	if err != nil {
		return fmt.Errorf("current contentInfo source: %w", err)
	}
	if contentID != manifest.ContentID || AppStatus(status) != manifest.AppStatus {
		return fmt.Errorf(
			"%w: current contentInfo record identity does not match the bundle",
			ErrDrift,
		)
	}
	hash, err := CanonicalSHA256(currentSource)
	if err != nil {
		return err
	}
	if hash != manifest.SourceSHA256 {
		return fmt.Errorf(
			"%w: expected SHA-256 %s, got %s",
			ErrDrift,
			manifest.SourceSHA256,
			hash,
		)
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"unsupported metadata schema version %d; expected %d",
			manifest.SchemaVersion,
			SchemaVersion,
		)
	}
	if err := validateContentID(manifest.ContentID); err != nil {
		return fmt.Errorf("metadata manifest contentId: %w", err)
	}
	status, err := normalizeAppStatus(manifest.AppStatus, false)
	if err != nil {
		return fmt.Errorf("metadata manifest: %w", err)
	}
	if status != manifest.AppStatus {
		return errors.New("metadata manifest appStatus must be uppercase")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.PulledAt); err != nil {
		return errors.New("metadata manifest pulledAt must be RFC3339")
	}
	if len(manifest.SourceSHA256) != sha256.Size*2 {
		return errors.New("metadata manifest sourceSha256 must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(manifest.SourceSHA256); err != nil {
		return errors.New("metadata manifest sourceSha256 must be a SHA-256 hex digest")
	}
	return nil
}

func normalizeAppStatus(status AppStatus, allowEmpty bool) (AppStatus, error) {
	normalized := AppStatus(strings.ToUpper(strings.TrimSpace(string(status))))
	if normalized == "" && allowEmpty {
		return "", nil
	}
	switch normalized {
	case AppStatusSale, AppStatusRegistration:
		return normalized, nil
	default:
		return "", errors.New("app status must be SALE or REGISTRATION")
	}
}

func validateContentID(contentID string) error {
	if len(contentID) != 12 || strings.TrimSpace(contentID) != contentID {
		return errors.New("must contain exactly 12 digits")
	}
	for _, character := range contentID {
		if character < '0' || character > '9' {
			return errors.New("must contain exactly 12 digits")
		}
	}
	return nil
}

func decodeObject(
	value json.RawMessage,
	name string,
) (map[string]json.RawMessage, error) {
	decoded, err := decodeJSON(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", name, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", name)
	}

	fields := make(map[string]json.RawMessage, len(object))
	for field, fieldValue := range object {
		encoded, marshalErr := json.Marshal(fieldValue)
		if marshalErr != nil {
			return nil, fmt.Errorf("%s field %q: %w", name, field, marshalErr)
		}
		fields[field] = encoded
	}
	return fields, nil
}

func decodeJSON(value json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return decoded, nil
}

func canonicalJSON(value json.RawMessage) ([]byte, error) {
	decoded, err := decodeJSON(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	return canonical, nil
}

func requiredString(fields map[string]json.RawMessage, name string) (string, error) {
	value, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", fmt.Errorf("metadata envelope %s is required", name)
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil ||
		strings.TrimSpace(decoded) == "" {
		return "", fmt.Errorf(
			"metadata envelope %s must be a non-empty string",
			name,
		)
	}
	return decoded, nil
}

func optionalString(fields map[string]json.RawMessage, name string) (string, error) {
	value, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", nil
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", fmt.Errorf("%s must be a string or null", name)
	}
	return decoded, nil
}

func validateObjectArray(
	fields map[string]json.RawMessage,
	name string,
	required []string,
) error {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("metadata envelope %s must be null or an array", name)
	}
	for index, item := range items {
		for _, field := range required {
			value, exists := item[field]
			if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf(
					"metadata envelope %s[%d].%s is required",
					name,
					index,
					field,
				)
			}
			if field == "reuseYn" {
				var boolean bool
				if err := json.Unmarshal(value, &boolean); err != nil {
					return fmt.Errorf(
						"metadata envelope %s[%d].reuseYn must be a boolean",
						name,
						index,
					)
				}
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err != nil ||
				strings.TrimSpace(text) == "" {
				return fmt.Errorf(
					"metadata envelope %s[%d].%s must be a non-empty string",
					name,
					index,
					field,
				)
			}
		}
	}
	return nil
}

func validateLocalizedScreenshots(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var localizations []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &localizations); err != nil {
		return nil
	}
	for index, localization := range localizations {
		screenshots, exists := localization["screenshots"]
		if !exists || bytes.Equal(bytes.TrimSpace(screenshots), []byte("null")) {
			continue
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(screenshots, &items); err != nil {
			return fmt.Errorf(
				"metadata envelope addLanguage[%d].screenshots must be null or an array",
				index,
			)
		}
		for screenshotIndex, item := range items {
			reuse, exists := item["reuseYn"]
			if !exists {
				return fmt.Errorf(
					"metadata envelope addLanguage[%d].screenshots[%d].reuseYn is required",
					index,
					screenshotIndex,
				)
			}
			var boolean bool
			if err := json.Unmarshal(reuse, &boolean); err != nil {
				return fmt.Errorf(
					"metadata envelope addLanguage[%d].screenshots[%d].reuseYn must be a boolean",
					index,
					screenshotIndex,
				)
			}
		}
	}
	return nil
}
