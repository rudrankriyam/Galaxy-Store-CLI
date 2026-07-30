// Package ship implements the deterministic, resumable Galaxy Store shipping
// domain. It deliberately stops at review submission: distribution through
// contentStatusUpdate (FOR_SALE) is a separate operation.
package ship

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

const (
	// PlanSchemaVersion is the current deterministic shipping-plan schema.
	PlanSchemaVersion = 1

	// Registration is the only appStatus variant a shipping plan may mutate.
	Registration = "REGISTRATION"
)

// Step is one durable stage in the Galaxy Store shipping pipeline.
type Step string

const (
	StepValidateTarget      Step = "validate_target"
	StepCreateUploadSession Step = "create_upload_session"
	StepUploadBinary        Step = "upload_binary"
	StepAddBinary           Step = "add_binary"
	StepApplyMetadata       Step = "apply_metadata"
	StepVerifyMetadata      Step = "verify_metadata"
	StepSubmitReview        Step = "submit_review"
)

var orderedSteps = []Step{
	StepValidateTarget,
	StepCreateUploadSession,
	StepUploadBinary,
	StepAddBinary,
	StepApplyMetadata,
	StepVerifyMetadata,
	StepSubmitReview,
}

// OrderedSteps returns a copy of the fixed shipping pipeline.
func OrderedSteps() []Step {
	return slices.Clone(orderedSteps)
}

// Request identifies local inputs and explicit Samsung binary attributes.
type Request struct {
	ContentID                   string
	AppStatus                   string
	BinaryPath                  string
	MetadataDirectory           string
	GMS                         string
	CopyDeviceConfigurationFrom string
}

// BinaryIdentity binds a plan to the exact regular file that was inspected.
type BinaryIdentity struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// MetadataIdentity binds a plan to a complete metadata bundle, its source
// baseline, and its desired contentUpdate envelope.
type MetadataIdentity struct {
	Directory          string `json:"directory"`
	BundleSHA256       string `json:"bundleSha256"`
	SourceSHA256       string `json:"sourceSha256"`
	BaseEnvelopeSHA256 string `json:"baseEnvelopeSha256"`
	DesiredSHA256      string `json:"desiredSha256"`
}

// Plan is deterministic: identical local inputs and options produce the same
// plan ID and ordered stages. It contains no wall-clock data.
type Plan struct {
	SchemaVersion               int              `json:"schemaVersion"`
	ID                          string           `json:"id"`
	ContentID                   string           `json:"contentId"`
	AppStatus                   string           `json:"appStatus"`
	Binary                      BinaryIdentity   `json:"binary"`
	Metadata                    MetadataIdentity `json:"metadata"`
	GMS                         string           `json:"gms"`
	CopyDeviceConfigurationFrom string           `json:"copyDeviceConfigurationFrom,omitempty"`
	Steps                       []Step           `json:"steps"`
}

// BuildPlan validates all local inputs before any remote service can be
// opened, then creates a deterministic shipping plan.
func BuildPlan(request Request) (Plan, error) {
	if err := apps.ValidateContentID(request.ContentID); err != nil {
		return Plan{}, err
	}
	if request.AppStatus != Registration {
		return Plan{}, errors.New("shipping target app status must be exactly REGISTRATION")
	}
	gms := strings.ToUpper(strings.TrimSpace(request.GMS))
	if gms != "Y" && gms != "N" {
		return Plan{}, errors.New("GMS must be Y or N")
	}
	if request.CopyDeviceConfigurationFrom != "" {
		if err := validateDigits(
			"copy device configuration binary sequence",
			request.CopyDeviceConfigurationFrom,
		); err != nil {
			return Plan{}, err
		}
	}

	binary, err := inspectBinary(request.BinaryPath)
	if err != nil {
		return Plan{}, err
	}
	bundle, metadataIdentity, err := inspectMetadata(request.MetadataDirectory)
	if err != nil {
		return Plan{}, err
	}
	if bundle.Manifest.ContentID != request.ContentID {
		return Plan{}, errors.New("metadata bundle content ID does not match shipping target")
	}
	if bundle.Manifest.AppStatus != metadata.AppStatusRegistration {
		return Plan{}, errors.New("metadata bundle must target exactly REGISTRATION")
	}

	plan := Plan{
		SchemaVersion:               PlanSchemaVersion,
		ContentID:                   request.ContentID,
		AppStatus:                   Registration,
		Binary:                      binary,
		Metadata:                    metadataIdentity,
		GMS:                         gms,
		CopyDeviceConfigurationFrom: request.CopyDeviceConfigurationFrom,
		Steps:                       OrderedSteps(),
	}
	id, err := planDigest(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.ID = id
	return plan, nil
}

// ValidateInputs re-opens and hashes both local inputs. This prevents a plan
// from uploading a file or applying metadata that changed after planning.
func (plan Plan) ValidateInputs() error {
	if err := validatePlan(plan); err != nil {
		return err
	}
	binary, err := inspectBinary(plan.Binary.Path)
	if err != nil {
		return err
	}
	if binary.Size != plan.Binary.Size || binary.SHA256 != plan.Binary.SHA256 {
		return errors.New("shipping binary changed after the plan was created")
	}
	_, identity, err := inspectMetadata(plan.Metadata.Directory)
	if err != nil {
		return err
	}
	if identity != plan.Metadata {
		return errors.New("metadata bundle changed after the plan was created")
	}
	return nil
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion {
		return fmt.Errorf("unsupported shipping plan schema version %d", plan.SchemaVersion)
	}
	if err := apps.ValidateContentID(plan.ContentID); err != nil {
		return err
	}
	if plan.AppStatus != Registration {
		return errors.New("shipping plan app status must be exactly REGISTRATION")
	}
	if plan.GMS != "Y" && plan.GMS != "N" {
		return errors.New("shipping plan GMS must be Y or N")
	}
	if plan.Binary.Size <= 0 || !validSHA256(plan.Binary.SHA256) {
		return errors.New("shipping plan contains an invalid binary identity")
	}
	for _, digest := range []string{
		plan.Metadata.BundleSHA256,
		plan.Metadata.SourceSHA256,
		plan.Metadata.BaseEnvelopeSHA256,
		plan.Metadata.DesiredSHA256,
	} {
		if !validSHA256(digest) {
			return errors.New("shipping plan contains an invalid metadata identity")
		}
	}
	if !slices.Equal(plan.Steps, orderedSteps) {
		return errors.New("shipping plan steps do not match the supported pipeline")
	}
	expectedID, err := planDigest(plan)
	if err != nil {
		return err
	}
	if plan.ID != expectedID {
		return errors.New("shipping plan ID does not match its contents")
	}
	return nil
}

func planDigest(plan Plan) (string, error) {
	plan.ID = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode shipping plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func inspectBinary(path string) (BinaryIdentity, error) {
	if strings.TrimSpace(path) == "" {
		return BinaryIdentity{}, errors.New("shipping binary path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return BinaryIdentity{}, fmt.Errorf("resolve shipping binary path: %w", err)
	}
	extension := strings.ToLower(filepath.Ext(absolute))
	if extension != ".apk" && extension != ".aab" {
		return BinaryIdentity{}, errors.New("shipping binary must be an APK or AAB file")
	}
	linkInfo, err := os.Lstat(absolute)
	if err != nil {
		return BinaryIdentity{}, fmt.Errorf("inspect shipping binary: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return BinaryIdentity{}, errors.New("shipping binary must be a regular file, not a symlink")
	}
	if linkInfo.Size() <= 0 {
		return BinaryIdentity{}, errors.New("shipping binary must not be empty")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return BinaryIdentity{}, fmt.Errorf("open shipping binary: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return BinaryIdentity{}, fmt.Errorf("inspect open shipping binary: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) {
		return BinaryIdentity{}, errors.New("shipping binary changed while it was being opened")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return BinaryIdentity{}, fmt.Errorf("hash shipping binary: %w", err)
	}
	currentInfo, err := file.Stat()
	if err != nil {
		return BinaryIdentity{}, fmt.Errorf("recheck shipping binary: %w", err)
	}
	if currentInfo.Size() != linkInfo.Size() || !os.SameFile(linkInfo, currentInfo) {
		return BinaryIdentity{}, errors.New("shipping binary changed while it was being hashed")
	}
	return BinaryIdentity{
		Path:   absolute,
		Size:   linkInfo.Size(),
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func inspectMetadata(directory string) (*metadata.Bundle, MetadataIdentity, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, MetadataIdentity{}, errors.New("metadata bundle directory is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return nil, MetadataIdentity{}, fmt.Errorf("resolve metadata bundle directory: %w", err)
	}
	bundle, err := metadata.ReadBundle(absolute)
	if err != nil {
		return nil, MetadataIdentity{}, err
	}
	baseEnvelope, err := metadata.Compile(bundle.Source)
	if err != nil {
		return nil, MetadataIdentity{}, err
	}
	baseHash, err := metadata.CanonicalSHA256(baseEnvelope)
	if err != nil {
		return nil, MetadataIdentity{}, err
	}
	desiredHash, err := metadata.CanonicalSHA256(bundle.Metadata)
	if err != nil {
		return nil, MetadataIdentity{}, err
	}
	bundleValue := struct {
		Manifest metadata.Manifest `json:"manifest"`
		Metadata json.RawMessage   `json:"metadata"`
		Source   json.RawMessage   `json:"source"`
	}{
		Manifest: bundle.Manifest,
		Metadata: bundle.Metadata,
		Source:   bundle.Source,
	}
	encoded, err := json.Marshal(bundleValue)
	if err != nil {
		return nil, MetadataIdentity{}, fmt.Errorf("encode metadata bundle identity: %w", err)
	}
	bundleHash, err := metadata.CanonicalSHA256(encoded)
	if err != nil {
		return nil, MetadataIdentity{}, err
	}
	return bundle, MetadataIdentity{
		Directory:          absolute,
		BundleSHA256:       bundleHash,
		SourceSHA256:       bundle.Manifest.SourceSHA256,
		BaseEnvelopeSHA256: baseHash,
		DesiredSHA256:      desiredHash,
	}, nil
}

func validateDigits(name string, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must contain only digits", name)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return fmt.Errorf("%s must contain only digits", name)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
