package ship

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
)

var (
	// ErrMetadataDrift indicates that Samsung no longer matches either the
	// bundle baseline or its desired metadata.
	ErrMetadataDrift = errors.New("remote REGISTRATION metadata has drifted")

	// ErrRemoteBinaryMissing indicates that a sequence returned by AddBinary is
	// absent from the next contentInfo readback.
	ErrRemoteBinaryMissing = errors.New("registered binary is missing from remote REGISTRATION content")

	// ErrAmbiguousBinaryAdd halts rather than risking a duplicate binary after
	// a crash or transport failure around the non-idempotent AddBinary call.
	ErrAmbiguousBinaryAdd = errors.New("binary registration outcome is ambiguous")

	// ErrAmbiguousSubmission halts rather than retrying the non-idempotent
	// review submission operation.
	ErrAmbiguousSubmission = errors.New("review submission outcome is ambiguous")

	// ErrUnexpectedSubmission indicates that Samsung reports an already
	// submitted target which this checkpoint cannot prove it submitted.
	ErrUnexpectedSubmission = errors.New("remote REGISTRATION target is already submitted")
)

// ReviewState is the minimum remote evidence needed to safely decide whether
// review submission may run.
type ReviewState string

const (
	ReviewStateDraft     ReviewState = "draft"
	ReviewStateSubmitted ReviewState = "submitted"
	ReviewStateUnknown   ReviewState = "unknown"
)

// RemoteBinary is the readback identity available from contentInfo.
type RemoteBinary struct {
	Sequence string
}

// RemoteState is an exact REGISTRATION contentInfo snapshot. Source must be
// the complete raw record so metadata tri-state semantics remain lossless.
type RemoteState struct {
	ContentID     string
	AppStatus     string
	ContentStatus string
	Source        json.RawMessage
	Binaries      []RemoteBinary
	ReviewState   ReviewState
}

// UploadSession is a temporary Samsung file-upload session. The adapter owns
// translating Samsung's 24-hour validity into an explicit expiry.
type UploadSession struct {
	ID        string
	ExpiresAt time.Time
}

// UploadResult identifies the temporary uploaded object.
type UploadResult struct {
	FileKey string
}

// AddBinaryRequest is the current v2 binary registration request.
type AddBinaryRequest struct {
	ContentID                   string
	FileKey                     string
	GMS                         string
	CopyDeviceConfigurationFrom string
}

// Remote is the narrow service surface required by a shipping run.
type Remote interface {
	InspectRegistration(context.Context, string) (RemoteState, error)
	CreateUploadSession(context.Context) (UploadSession, error)
	UploadBinary(context.Context, string, string) (UploadResult, error)
	AddBinary(context.Context, AddBinaryRequest) (string, error)
	ApplyMetadata(context.Context, string, json.RawMessage) error
	SubmitReview(context.Context, string) error
}

// Engine runs and resumes a typed Galaxy Store shipping plan.
type Engine struct {
	Remote Remote
	Store  CheckpointStore
	Now    func() time.Time
}

// Result contains the reconciled durable state after a run.
type Result struct {
	// Checkpoint is available to in-process callers for orchestration, but is
	// intentionally excluded from JSON because session IDs and file keys are
	// private resumability state.
	Checkpoint         Checkpoint `json:"-"`
	Complete           bool       `json:"complete"`
	MutationsPerformed bool       `json:"mutationsPerformed"`
}

// Run validates local inputs, reconciles durable hints against Samsung, and
// advances the fixed pipeline. It never changes content status to FOR_SALE.
func (engine Engine) Run(ctx context.Context, plan Plan) (Result, error) {
	if engine.Remote == nil {
		return Result{}, errors.New("shipping remote service is required")
	}
	if engine.Store == nil {
		return Result{}, errors.New("shipping checkpoint store is required")
	}
	if err := plan.ValidateInputs(); err != nil {
		return Result{}, err
	}
	now := time.Now
	if engine.Now != nil {
		now = engine.Now
	}
	mutationsPerformed := false

	bundle, identity, err := inspectMetadata(plan.Metadata.Directory)
	if err != nil {
		return Result{}, err
	}
	if identity != plan.Metadata {
		return Result{}, errors.New("metadata bundle changed after the plan was created")
	}

	checkpoint, err := engine.Store.Load()
	if errors.Is(err, ErrCheckpointNotFound) {
		checkpoint = newCheckpoint(plan)
	} else if err != nil {
		return Result{}, err
	} else {
		if err := validateCheckpoint(checkpoint); err != nil {
			return Result{}, err
		}
		if !checkpoint.matches(plan) {
			return Result{}, ErrCheckpointMismatch
		}
	}
	if checkpoint.AmbiguousSubmission ||
		checkpoint.PendingStep == StepSubmitReview {
		return Result{Checkpoint: checkpoint}, ErrAmbiguousSubmission
	}

	state, err := engine.Remote.InspectRegistration(ctx, plan.ContentID)
	if err != nil {
		return Result{Checkpoint: checkpoint}, fmt.Errorf(
			"inspect REGISTRATION target: %w",
			err,
		)
	}
	if err := reconcile(&checkpoint, plan, bundle, state, now().UTC()); err != nil {
		return Result{Checkpoint: checkpoint}, err
	}
	if err := engine.Store.Save(checkpoint); err != nil {
		return Result{Checkpoint: checkpoint}, err
	}

	if !checkpoint.has(StepCreateUploadSession) {
		session, err := engine.Remote.CreateUploadSession(ctx)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"create upload session: %w",
				err,
			)
		}
		if strings.TrimSpace(session.ID) == "" ||
			!session.ExpiresAt.After(now().UTC()) {
			return Result{Checkpoint: checkpoint}, errors.New(
				"create upload session: remote returned an invalid session",
			)
		}
		mutationsPerformed = true
		checkpoint.UploadSessionID = session.ID
		checkpoint.UploadSessionExpiresAt = session.ExpiresAt.UTC().
			Format(time.RFC3339Nano)
		checkpoint.complete(StepCreateUploadSession)
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
	}

	if !checkpoint.has(StepUploadBinary) {
		upload, err := engine.Remote.UploadBinary(
			ctx,
			checkpoint.UploadSessionID,
			plan.Binary.Path,
		)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf("upload binary: %w", err)
		}
		if strings.TrimSpace(upload.FileKey) == "" {
			return Result{Checkpoint: checkpoint}, errors.New(
				"upload binary: remote returned no file key",
			)
		}
		mutationsPerformed = true
		checkpoint.FileKey = upload.FileKey
		checkpoint.complete(StepUploadBinary)
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
	}

	if !checkpoint.has(StepAddBinary) {
		state, err = engine.Remote.InspectRegistration(ctx, plan.ContentID)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"inspect target before binary registration: %w",
				err,
			)
		}
		if err := validateRemoteTarget(state, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if matches, err := matchesSourceBaseline(plan, state.Source); err != nil {
			return Result{Checkpoint: checkpoint}, err
		} else if !matches {
			return Result{Checkpoint: checkpoint}, ErrMetadataDrift
		}

		checkpoint.PendingStep = StepAddBinary
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		sequence, err := engine.Remote.AddBinary(ctx, AddBinaryRequest{
			ContentID:                   plan.ContentID,
			FileKey:                     checkpoint.FileKey,
			GMS:                         plan.GMS,
			CopyDeviceConfigurationFrom: plan.CopyDeviceConfigurationFrom,
		})
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"%w: %w",
				ErrAmbiguousBinaryAdd,
				err,
			)
		}
		if err := validateDigits("remote binary sequence", sequence); err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"%w: %w",
				ErrAmbiguousBinaryAdd,
				err,
			)
		}
		mutationsPerformed = true
		checkpoint.BinarySequence = sequence
		checkpoint.PendingStep = ""
		checkpoint.complete(StepAddBinary)
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}

		state, err = engine.Remote.InspectRegistration(ctx, plan.ContentID)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"verify registered binary: %w",
				err,
			)
		}
		if err := validateRemoteTarget(state, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if !hasRemoteBinary(state, checkpoint.BinarySequence) {
			return Result{Checkpoint: checkpoint}, ErrRemoteBinaryMissing
		}
	}

	if !checkpoint.has(StepApplyMetadata) {
		state, err = engine.Remote.InspectRegistration(ctx, plan.ContentID)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"inspect target before metadata apply: %w",
				err,
			)
		}
		if err := validateRemoteTarget(state, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if !hasRemoteBinary(state, checkpoint.BinarySequence) {
			return Result{Checkpoint: checkpoint}, ErrRemoteBinaryMissing
		}
		applied, baseline, err := metadataState(
			plan,
			bundle.Metadata,
			state.Source,
		)
		if err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if applied {
			checkpoint.complete(StepApplyMetadata)
			checkpoint.complete(StepVerifyMetadata)
			if err := engine.Store.Save(checkpoint); err != nil {
				return Result{Checkpoint: checkpoint}, err
			}
		} else if !baseline {
			return Result{Checkpoint: checkpoint}, ErrMetadataDrift
		}
	}

	if !checkpoint.has(StepApplyMetadata) {
		checkpoint.PendingStep = StepApplyMetadata
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if err := engine.Remote.ApplyMetadata(
			ctx,
			plan.ContentID,
			bundle.Metadata,
		); err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf("apply metadata: %w", err)
		}
		mutationsPerformed = true
		checkpoint.PendingStep = ""
		checkpoint.complete(StepApplyMetadata)
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
	}

	if !checkpoint.has(StepVerifyMetadata) {
		state, err = engine.Remote.InspectRegistration(ctx, plan.ContentID)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"verify metadata readback: %w",
				err,
			)
		}
		if err := validateRemoteTarget(state, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		applied, baseline, err := metadataState(plan, bundle.Metadata, state.Source)
		if err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if !applied {
			if baseline {
				checkpoint.invalidateFrom(StepApplyMetadata)
				_ = engine.Store.Save(checkpoint)
				return Result{Checkpoint: checkpoint}, errors.New(
					"metadata readback does not contain the applied changes",
				)
			}
			return Result{Checkpoint: checkpoint}, ErrMetadataDrift
		}
		checkpoint.complete(StepVerifyMetadata)
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
	}

	if !checkpoint.has(StepSubmitReview) {
		state, err = engine.Remote.InspectRegistration(ctx, plan.ContentID)
		if err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"inspect target before review submission: %w",
				err,
			)
		}
		if err := validateRemoteTarget(state, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if !hasRemoteBinary(state, checkpoint.BinarySequence) {
			return Result{Checkpoint: checkpoint}, ErrRemoteBinaryMissing
		}
		applied, _, err := metadataState(plan, bundle.Metadata, state.Source)
		if err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if !applied {
			return Result{Checkpoint: checkpoint}, ErrMetadataDrift
		}
		switch state.ReviewState {
		case ReviewStateSubmitted:
			return Result{Checkpoint: checkpoint}, ErrUnexpectedSubmission
		case ReviewStateDraft:
		default:
			return Result{Checkpoint: checkpoint}, errors.New(
				"remote review state is unknown; refusing to submit",
			)
		}

		checkpoint.PendingStep = StepSubmitReview
		checkpoint.AmbiguousSubmission = true
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
		if err := engine.Remote.SubmitReview(ctx, plan.ContentID); err != nil {
			return Result{Checkpoint: checkpoint}, fmt.Errorf(
				"%w: %w",
				ErrAmbiguousSubmission,
				err,
			)
		}
		mutationsPerformed = true
		checkpoint.complete(StepSubmitReview)
		checkpoint.PendingStep = ""
		checkpoint.AmbiguousSubmission = false
		if err := engine.Store.Save(checkpoint); err != nil {
			return Result{Checkpoint: checkpoint}, err
		}
	}
	return Result{
		Checkpoint:         checkpoint,
		Complete:           checkpoint.has(StepSubmitReview),
		MutationsPerformed: mutationsPerformed,
	}, nil
}

func reconcile(
	checkpoint *Checkpoint,
	plan Plan,
	bundle *metadata.Bundle,
	state RemoteState,
	now time.Time,
) error {
	if err := validateRemoteTarget(state, plan.ContentID); err != nil {
		return err
	}
	checkpoint.complete(StepValidateTarget)

	if checkpoint.PendingStep == StepAddBinary {
		return ErrAmbiguousBinaryAdd
	}

	if checkpoint.BinarySequence != "" {
		if hasRemoteBinary(state, checkpoint.BinarySequence) {
			checkpoint.complete(StepAddBinary)
		} else if checkpoint.has(StepAddBinary) {
			// contentInfo can lag AddBinary. Retrying would register a
			// duplicate non-idempotent binary, so preserve the returned
			// sequence and wait for a later readback to confirm it.
			return ErrRemoteBinaryMissing
		}
	}

	if !checkpoint.has(StepAddBinary) && checkpoint.UploadSessionID != "" {
		expiresAt, err := time.Parse(
			time.RFC3339Nano,
			checkpoint.UploadSessionExpiresAt,
		)
		if err != nil {
			return errors.New("shipping checkpoint upload session expiry is invalid")
		}
		if !expiresAt.After(now) {
			checkpoint.invalidateFrom(StepCreateUploadSession)
		} else {
			checkpoint.complete(StepCreateUploadSession)
			if checkpoint.FileKey != "" {
				checkpoint.complete(StepUploadBinary)
			}
		}
	}

	applied, baseline, err := metadataState(plan, bundle.Metadata, state.Source)
	if err != nil {
		return err
	}
	if !checkpoint.has(StepAddBinary) {
		matches, matchErr := matchesSourceBaseline(plan, state.Source)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return ErrMetadataDrift
		}
	}
	switch checkpoint.PendingStep {
	case "":
	case StepApplyMetadata:
		if applied {
			checkpoint.PendingStep = ""
			checkpoint.complete(StepApplyMetadata)
		} else if baseline {
			// The write did not take effect; retrying the exact bound envelope
			// is safe after clearing the write-ahead marker.
			checkpoint.PendingStep = ""
			checkpoint.invalidateFrom(StepApplyMetadata)
		} else {
			return ErrMetadataDrift
		}
	default:
		return fmt.Errorf(
			"shipping checkpoint has unsupported pending step %q",
			checkpoint.PendingStep,
		)
	}
	if applied {
		checkpoint.complete(StepApplyMetadata)
		checkpoint.complete(StepVerifyMetadata)
	} else {
		checkpoint.invalidateFrom(StepApplyMetadata)
		if !baseline {
			return ErrMetadataDrift
		}
	}

	switch state.ReviewState {
	case ReviewStateSubmitted:
		if !checkpoint.has(StepSubmitReview) {
			return ErrUnexpectedSubmission
		}
	case ReviewStateDraft:
		if checkpoint.has(StepSubmitReview) {
			return ErrAmbiguousSubmission
		}
	case ReviewStateUnknown:
		if checkpoint.has(StepSubmitReview) {
			return ErrAmbiguousSubmission
		}
	default:
		return fmt.Errorf("unsupported remote review state %q", state.ReviewState)
	}
	return nil
}

func validateRemoteTarget(state RemoteState, contentID string) error {
	if state.ContentID != contentID {
		return errors.New("remote target content ID does not match the shipping plan")
	}
	if state.AppStatus != Registration {
		return errors.New("remote shipping target must be exactly REGISTRATION")
	}
	if len(state.Source) == 0 || !json.Valid(state.Source) {
		return errors.New("remote REGISTRATION source must be valid JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(state.Source, &fields); err != nil || fields == nil {
		return errors.New("remote REGISTRATION source must be one JSON object")
	}
	var rawContentID string
	if err := json.Unmarshal(fields["contentId"], &rawContentID); err != nil ||
		rawContentID != contentID {
		return errors.New(
			"remote REGISTRATION source content ID does not match the shipping plan",
		)
	}
	var rawAppStatus string
	if err := json.Unmarshal(fields["appStatus"], &rawAppStatus); err != nil ||
		rawAppStatus != Registration {
		return errors.New(
			"remote REGISTRATION source app status must be exactly REGISTRATION",
		)
	}
	for _, binary := range state.Binaries {
		if err := validateDigits("remote binary sequence", binary.Sequence); err != nil {
			return err
		}
	}
	return nil
}

func metadataState(
	plan Plan,
	desired json.RawMessage,
	source json.RawMessage,
) (applied bool, baseline bool, err error) {
	diff, err := metadata.Diff(source, desired)
	if err != nil {
		return false, false, fmt.Errorf("compare remote REGISTRATION metadata: %w", err)
	}
	if !diff.HasChanges() {
		return true, false, nil
	}
	currentEnvelope, err := metadata.Compile(source)
	if err != nil {
		return false, false, err
	}
	currentHash, err := metadata.CanonicalSHA256(currentEnvelope)
	if err != nil {
		return false, false, err
	}
	return false, currentHash == plan.Metadata.BaseEnvelopeSHA256, nil
}

func matchesSourceBaseline(plan Plan, source json.RawMessage) (bool, error) {
	hash, err := metadata.CanonicalSHA256(source)
	if err != nil {
		return false, fmt.Errorf("hash remote REGISTRATION source: %w", err)
	}
	return hash == plan.Metadata.SourceSHA256, nil
}

func hasRemoteBinary(state RemoteState, sequence string) bool {
	if sequence == "" {
		return false
	}
	return slices.ContainsFunc(state.Binaries, func(binary RemoteBinary) bool {
		return binary.Sequence == sequence
	})
}
