package ship

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// CheckpointSchemaVersion is the current durable checkpoint schema.
	CheckpointSchemaVersion = 1
	maximumCheckpointSize   = 1 << 20
)

var (
	// ErrCheckpointNotFound indicates that a shipping run has no durable state.
	ErrCheckpointNotFound = errors.New("shipping checkpoint does not exist")

	// ErrCheckpointMismatch indicates that a checkpoint is bound to different
	// local inputs or a different target.
	ErrCheckpointMismatch = errors.New("shipping checkpoint does not match the plan")
)

// Checkpoint is a private, durable hint about a shipping run. The engine
// always reconciles these values with Samsung before relying on them.
type Checkpoint struct {
	SchemaVersion          int    `json:"schemaVersion"`
	PlanID                 string `json:"planId"`
	ContentID              string `json:"contentId"`
	AppStatus              string `json:"appStatus"`
	BinarySHA256           string `json:"binarySha256"`
	BinarySize             int64  `json:"binarySize"`
	MetadataSHA256         string `json:"metadataSha256"`
	UploadSessionID        string `json:"uploadSessionId,omitempty"`
	UploadSessionExpiresAt string `json:"uploadSessionExpiresAt,omitempty"`
	FileKey                string `json:"fileKey,omitempty"`
	BinarySequence         string `json:"binarySequence,omitempty"`
	CompletedSteps         []Step `json:"completedSteps"`
	PendingStep            Step   `json:"pendingStep,omitempty"`
	AmbiguousSubmission    bool   `json:"ambiguousSubmission,omitempty"`
}

// CheckpointStore is the narrow durable-state surface used by Engine.
type CheckpointStore interface {
	Load() (Checkpoint, error)
	Save(Checkpoint) error
}

// FileCheckpointStore atomically stores one private checkpoint file.
type FileCheckpointStore struct {
	path string
}

// NewFileCheckpointStore creates a secure filesystem checkpoint store.
func NewFileCheckpointStore(path string) (*FileCheckpointStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("shipping checkpoint path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve shipping checkpoint path: %w", err)
	}
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, errors.New("shipping checkpoint path must not be a filesystem root")
	}
	return &FileCheckpointStore{path: absolute}, nil
}

// Path returns the absolute checkpoint path.
func (store *FileCheckpointStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Load securely reads and validates the checkpoint.
func (store *FileCheckpointStore) Load() (Checkpoint, error) {
	if store == nil || store.path == "" {
		return Checkpoint{}, errors.New("shipping checkpoint store is not configured")
	}
	info, err := os.Lstat(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	if err != nil {
		return Checkpoint{}, fmt.Errorf("inspect shipping checkpoint: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Checkpoint{}, errors.New("shipping checkpoint must be a regular file, not a symlink")
	}
	if checkpointPermissionsTooPermissive(info.Mode()) {
		return Checkpoint{}, errors.New("shipping checkpoint permissions are too permissive")
	}
	if info.Size() > maximumCheckpointSize {
		return Checkpoint{}, fmt.Errorf(
			"shipping checkpoint exceeds %d bytes",
			maximumCheckpointSize,
		)
	}

	file, err := os.Open(store.path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("open shipping checkpoint: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("inspect open shipping checkpoint: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return Checkpoint{}, errors.New("shipping checkpoint changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumCheckpointSize+1))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read shipping checkpoint: %w", err)
	}
	if len(data) > maximumCheckpointSize {
		return Checkpoint{}, fmt.Errorf(
			"shipping checkpoint exceeds %d bytes",
			maximumCheckpointSize,
		)
	}
	var checkpoint Checkpoint
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode shipping checkpoint: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Checkpoint{}, err
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return Checkpoint{}, err
	}
	currentInfo, err := os.Lstat(store.path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("recheck shipping checkpoint: %w", err)
	}
	if currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, currentInfo) {
		return Checkpoint{}, errors.New("shipping checkpoint changed while it was being read")
	}
	return checkpoint, nil
}

// Save validates and atomically replaces a 0600 checkpoint.
func (store *FileCheckpointStore) Save(checkpoint Checkpoint) error {
	if store == nil || store.path == "" {
		return errors.New("shipping checkpoint store is not configured")
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("encode shipping checkpoint: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maximumCheckpointSize {
		return fmt.Errorf("shipping checkpoint exceeds %d bytes", maximumCheckpointSize)
	}
	return writeCheckpointAtomic(store.path, data)
}

func newCheckpoint(plan Plan) Checkpoint {
	return Checkpoint{
		SchemaVersion:  CheckpointSchemaVersion,
		PlanID:         plan.ID,
		ContentID:      plan.ContentID,
		AppStatus:      plan.AppStatus,
		BinarySHA256:   plan.Binary.SHA256,
		BinarySize:     plan.Binary.Size,
		MetadataSHA256: plan.Metadata.BundleSHA256,
		CompletedSteps: []Step{},
	}
}

func (checkpoint Checkpoint) matches(plan Plan) bool {
	return checkpoint.PlanID == plan.ID &&
		checkpoint.ContentID == plan.ContentID &&
		checkpoint.AppStatus == plan.AppStatus &&
		checkpoint.BinarySHA256 == plan.Binary.SHA256 &&
		checkpoint.BinarySize == plan.Binary.Size &&
		checkpoint.MetadataSHA256 == plan.Metadata.BundleSHA256
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if checkpoint.SchemaVersion != CheckpointSchemaVersion {
		return fmt.Errorf(
			"unsupported shipping checkpoint schema version %d",
			checkpoint.SchemaVersion,
		)
	}
	if checkpoint.PlanID == "" || !validSHA256(checkpoint.PlanID) {
		return errors.New("shipping checkpoint contains an invalid plan ID")
	}
	if checkpoint.AppStatus != Registration {
		return errors.New("shipping checkpoint app status must be exactly REGISTRATION")
	}
	if err := validateDigits("shipping checkpoint content ID", checkpoint.ContentID); err != nil ||
		len(checkpoint.ContentID) != 12 {
		return errors.New("shipping checkpoint content ID must contain exactly 12 digits")
	}
	if checkpoint.BinarySize <= 0 || !validSHA256(checkpoint.BinarySHA256) {
		return errors.New("shipping checkpoint contains an invalid binary identity")
	}
	if !validSHA256(checkpoint.MetadataSHA256) {
		return errors.New("shipping checkpoint contains an invalid metadata identity")
	}
	if (checkpoint.UploadSessionID == "") !=
		(checkpoint.UploadSessionExpiresAt == "") {
		return errors.New("shipping checkpoint upload session identity is incomplete")
	}
	if checkpoint.UploadSessionExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, checkpoint.UploadSessionExpiresAt); err != nil {
			return errors.New("shipping checkpoint upload session expiry is invalid")
		}
	}
	if checkpoint.UploadSessionID != "" &&
		checkpoint.UploadSessionID != strings.TrimSpace(checkpoint.UploadSessionID) {
		return errors.New("shipping checkpoint upload session ID has surrounding whitespace")
	}
	if checkpoint.FileKey != "" && checkpoint.UploadSessionID == "" {
		return errors.New("shipping checkpoint file key has no upload session")
	}
	if checkpoint.FileKey != "" &&
		checkpoint.FileKey != strings.TrimSpace(checkpoint.FileKey) {
		return errors.New("shipping checkpoint file key has surrounding whitespace")
	}
	if checkpoint.BinarySequence != "" {
		if err := validateDigits(
			"shipping checkpoint binary sequence",
			checkpoint.BinarySequence,
		); err != nil {
			return err
		}
	}

	known := make(map[Step]int, len(orderedSteps))
	for index, step := range orderedSteps {
		known[step] = index
	}
	seen := make(map[Step]struct{}, len(checkpoint.CompletedSteps))
	lastIndex := -1
	for _, step := range checkpoint.CompletedSteps {
		index, ok := known[step]
		if !ok {
			return fmt.Errorf("shipping checkpoint contains unknown completed step %q", step)
		}
		if _, duplicate := seen[step]; duplicate {
			return fmt.Errorf("shipping checkpoint contains duplicate completed step %q", step)
		}
		if index <= lastIndex {
			return errors.New("shipping checkpoint completed steps are out of order")
		}
		seen[step] = struct{}{}
		lastIndex = index
	}
	if checkpoint.PendingStep != "" {
		if _, ok := known[checkpoint.PendingStep]; !ok {
			return fmt.Errorf(
				"shipping checkpoint contains unknown pending step %q",
				checkpoint.PendingStep,
			)
		}
	}
	if checkpoint.AmbiguousSubmission &&
		checkpoint.PendingStep != StepSubmitReview {
		return errors.New("ambiguous submission checkpoint must be pending submit_review")
	}
	if slices.Contains(checkpoint.CompletedSteps, StepCreateUploadSession) &&
		checkpoint.UploadSessionID == "" {
		return errors.New("completed upload-session step has no session identity")
	}
	if slices.Contains(checkpoint.CompletedSteps, StepUploadBinary) &&
		checkpoint.FileKey == "" {
		return errors.New("completed upload step has no file key")
	}
	if slices.Contains(checkpoint.CompletedSteps, StepAddBinary) &&
		checkpoint.BinarySequence == "" {
		return errors.New("completed add-binary step has no binary sequence")
	}
	if slices.Contains(checkpoint.CompletedSteps, StepVerifyMetadata) &&
		!slices.Contains(checkpoint.CompletedSteps, StepApplyMetadata) {
		return errors.New("completed metadata verification has no metadata apply step")
	}
	if slices.Contains(checkpoint.CompletedSteps, StepSubmitReview) &&
		(!slices.Contains(checkpoint.CompletedSteps, StepAddBinary) ||
			!slices.Contains(checkpoint.CompletedSteps, StepVerifyMetadata)) {
		return errors.New("completed review submission is missing prerequisites")
	}
	switch checkpoint.PendingStep {
	case StepAddBinary:
		if !slices.Contains(checkpoint.CompletedSteps, StepUploadBinary) ||
			slices.Contains(checkpoint.CompletedSteps, StepAddBinary) {
			return errors.New("pending add-binary step has invalid prerequisites")
		}
	case StepApplyMetadata:
		if !slices.Contains(checkpoint.CompletedSteps, StepAddBinary) ||
			slices.Contains(checkpoint.CompletedSteps, StepApplyMetadata) {
			return errors.New("pending metadata apply step has invalid prerequisites")
		}
	case StepSubmitReview:
		if !slices.Contains(checkpoint.CompletedSteps, StepAddBinary) ||
			!slices.Contains(checkpoint.CompletedSteps, StepVerifyMetadata) ||
			slices.Contains(checkpoint.CompletedSteps, StepSubmitReview) ||
			!checkpoint.AmbiguousSubmission {
			return errors.New("pending review submission has invalid prerequisites")
		}
	case StepValidateTarget, StepCreateUploadSession, StepUploadBinary, StepVerifyMetadata:
		return fmt.Errorf("shipping checkpoint cannot persist pending step %q", checkpoint.PendingStep)
	}
	return nil
}

func (checkpoint *Checkpoint) has(step Step) bool {
	return slices.Contains(checkpoint.CompletedSteps, step)
}

func (checkpoint *Checkpoint) complete(step Step) {
	if checkpoint.has(step) {
		return
	}
	checkpoint.CompletedSteps = append(checkpoint.CompletedSteps, step)
	slices.SortFunc(checkpoint.CompletedSteps, func(left Step, right Step) int {
		return stepIndex(left) - stepIndex(right)
	})
}

func (checkpoint *Checkpoint) invalidateFrom(step Step) {
	index := stepIndex(step)
	filtered := checkpoint.CompletedSteps[:0]
	for _, completed := range checkpoint.CompletedSteps {
		if stepIndex(completed) < index {
			filtered = append(filtered, completed)
		}
	}
	checkpoint.CompletedSteps = filtered
	if step == StepCreateUploadSession || step == StepUploadBinary {
		checkpoint.UploadSessionID = ""
		checkpoint.UploadSessionExpiresAt = ""
		checkpoint.FileKey = ""
	}
	if index <= stepIndex(StepAddBinary) {
		checkpoint.BinarySequence = ""
	}
	if checkpoint.PendingStep != "" && stepIndex(checkpoint.PendingStep) >= index {
		checkpoint.PendingStep = ""
		checkpoint.AmbiguousSubmission = false
	}
}

func stepIndex(step Step) int {
	return slices.Index(orderedSteps, step)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode shipping checkpoint: %w", err)
	}
	return errors.New("decode shipping checkpoint: multiple JSON values")
}
