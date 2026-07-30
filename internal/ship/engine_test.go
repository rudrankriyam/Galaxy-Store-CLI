package ship

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

func TestEngineRunsTypedPipelineAndResumesAsNoOp(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	store := &memoryStore{}
	engine := Engine{Remote: remote, Store: store, Now: func() time.Time { return fixedNow }}

	result, err := engine.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || !result.MutationsPerformed {
		t.Fatalf("result = %#v", result)
	}
	if !slices.Equal(result.Checkpoint.CompletedSteps, orderedSteps) {
		t.Fatalf("completed steps = %v", result.Checkpoint.CompletedSteps)
	}
	assertCalls(t, remote, remoteCalls{
		inspect: 6,
		session: 1,
		upload:  1,
		add:     1,
		apply:   1,
		submit:  1,
	})

	before := remote.calls
	result, err = engine.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.MutationsPerformed || remote.calls != (remoteCalls{
		inspect: before.inspect + 1,
		session: before.session,
		upload:  before.upload,
		add:     before.add,
		apply:   before.apply,
		submit:  before.submit,
	}) {
		t.Fatalf("resume result = %#v, calls = %#v", result, remote.calls)
	}
}

func TestEngineCrashPointsReconcileWithoutRepeatingUnsafeMutations(t *testing.T) {
	tests := []struct {
		name       string
		failSaveAt int
		wantError  error
		check      func(*testing.T, *fakeRemote, *memoryStore, Plan)
	}{
		{
			name:       "session result not checkpointed creates a replacement",
			failSaveAt: 2,
			check: func(t *testing.T, remote *fakeRemote, _ *memoryStore, _ Plan) {
				t.Helper()
				if remote.calls.session != 2 {
					t.Fatalf("session calls = %d", remote.calls.session)
				}
			},
		},
		{
			name:       "upload result not checkpointed uploads again",
			failSaveAt: 3,
			check: func(t *testing.T, remote *fakeRemote, _ *memoryStore, _ Plan) {
				t.Helper()
				if remote.calls.upload != 2 {
					t.Fatalf("upload calls = %d", remote.calls.upload)
				}
			},
		},
		{
			name:       "binary add result not checkpointed halts",
			failSaveAt: 5,
			wantError:  ErrAmbiguousBinaryAdd,
			check: func(t *testing.T, remote *fakeRemote, _ *memoryStore, _ Plan) {
				t.Helper()
				if remote.calls.add != 1 {
					t.Fatalf("add calls = %d", remote.calls.add)
				}
			},
		},
		{
			name:       "metadata result not checkpointed reconciles readback",
			failSaveAt: 7,
			check: func(t *testing.T, remote *fakeRemote, _ *memoryStore, _ Plan) {
				t.Helper()
				if remote.calls.apply != 1 {
					t.Fatalf("apply calls = %d", remote.calls.apply)
				}
			},
		},
		{
			name:       "submission result not checkpointed halts",
			failSaveAt: 10,
			wantError:  ErrAmbiguousSubmission,
			check: func(t *testing.T, remote *fakeRemote, _ *memoryStore, _ Plan) {
				t.Helper()
				if remote.calls.submit != 1 {
					t.Fatalf("submit calls = %d", remote.calls.submit)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			plan := mustPlan(t, fixture)
			remote := newFakeRemote(fixture)
			store := &memoryStore{failSaveAt: test.failSaveAt}
			engine := Engine{
				Remote: remote,
				Store:  store,
				Now:    func() time.Time { return fixedNow },
			}
			if _, err := engine.Run(context.Background(), plan); err == nil ||
				!strings.Contains(err.Error(), "injected checkpoint failure") {
				t.Fatalf("first Run error = %v", err)
			}
			store.failSaveAt = 0
			result, err := engine.Run(context.Background(), plan)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("resume error = %v, want %v", err, test.wantError)
				}
				if result.Complete {
					t.Fatalf("resume unexpectedly complete: %#v", result)
				}
			} else if err != nil {
				t.Fatalf("resume error = %v", err)
			} else if !result.Complete {
				t.Fatalf("resume result = %#v", result)
			}
			test.check(t, remote, store, plan)
		})
	}
}

func TestEngineExpiredSessionIsDiscarded(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	checkpoint := newCheckpoint(plan)
	checkpoint.complete(StepValidateTarget)
	checkpoint.complete(StepCreateUploadSession)
	checkpoint.complete(StepUploadBinary)
	checkpoint.UploadSessionID = "expired-session"
	checkpoint.UploadSessionExpiresAt = fixedNow.Add(-time.Second).Format(time.RFC3339Nano)
	checkpoint.FileKey = "expired-file-key"
	store := &memoryStore{checkpoint: checkpoint, exists: true}
	remote := newFakeRemote(fixture)

	result, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete {
		t.Fatalf("result = %#v", result)
	}
	if remote.calls.session != 1 || remote.calls.upload != 1 {
		t.Fatalf("calls = %#v", remote.calls)
	}
	if result.Checkpoint.UploadSessionID == "expired-session" {
		t.Fatal("expired upload session was reused")
	}
}

func TestEngineRemoteBinaryMissingHaltsWithoutReadding(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	checkpoint := checkpointThroughUpload(plan)
	checkpoint.BinarySequence = "41"
	checkpoint.complete(StepAddBinary)
	store := &memoryStore{checkpoint: checkpoint, exists: true}
	remote := newFakeRemote(fixture)

	result, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if !errors.Is(err, ErrRemoteBinaryMissing) {
		t.Fatalf("Run error = %v", err)
	}
	if result.Complete || remote.calls.session != 0 ||
		remote.calls.upload != 0 || remote.calls.add != 0 {
		t.Fatalf("result = %#v, calls = %#v", result, remote.calls)
	}
	if result.Checkpoint.BinarySequence != "41" ||
		!result.Checkpoint.has(StepAddBinary) {
		t.Fatalf("binary evidence was discarded: %#v", result.Checkpoint)
	}
}

func TestEngineNewBinaryMissingReadbackStopsBeforeMetadata(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	remote.reflectAddedBinary = false
	store := &memoryStore{}
	engine := Engine{Remote: remote, Store: store, Now: func() time.Time { return fixedNow }}

	_, err := engine.Run(context.Background(), plan)
	if !errors.Is(err, ErrRemoteBinaryMissing) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.apply != 0 || remote.calls.submit != 0 {
		t.Fatalf("unsafe calls = %#v", remote.calls)
	}
	if store.checkpoint.BinarySequence != "100" ||
		!store.checkpoint.has(StepAddBinary) {
		t.Fatalf("checkpoint discarded add result: %#v", store.checkpoint)
	}

	_, err = engine.Run(context.Background(), plan)
	if !errors.Is(err, ErrRemoteBinaryMissing) || remote.calls.add != 1 {
		t.Fatalf("resume error = %v, calls = %#v", err, remote.calls)
	}

	remote.state.Binaries = append(
		remote.state.Binaries,
		RemoteBinary{Sequence: store.checkpoint.BinarySequence},
	)
	result, err := engine.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || remote.calls.add != 1 {
		t.Fatalf("confirmed resume = %#v, calls = %#v", result, remote.calls)
	}
}

func TestEngineRejectsInputMismatchBeforeRemoteInspection(t *testing.T) {
	firstFixture := newFixture(t)
	firstPlan := mustPlan(t, firstFixture)
	secondFixture := newFixture(t)
	if err := osWrite(secondFixture.request.BinaryPath, "different-binary"); err != nil {
		t.Fatal(err)
	}
	secondPlan := mustPlan(t, secondFixture)
	store := &memoryStore{checkpoint: newCheckpoint(firstPlan), exists: true}
	remote := newFakeRemote(secondFixture)

	_, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), secondPlan)
	if !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.inspect != 0 {
		t.Fatalf("inspect calls = %d", remote.calls.inspect)
	}
}

func TestEngineRejectsMetadataDriftBeforeMutation(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	remote.state.Source = desiredSource("An unrelated remote edit")
	store := &memoryStore{}

	_, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if !errors.Is(err, ErrMetadataDrift) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.session+remote.calls.upload+remote.calls.add+
		remote.calls.apply+remote.calls.submit != 0 {
		t.Fatalf("mutation calls = %#v", remote.calls)
	}
}

func TestEngineRechecksMetadataDriftImmediatelyBeforeApply(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	remote.inspectHook = func(remote *fakeRemote) {
		if remote.calls.inspect == 4 {
			remote.state.Source = desiredSource("Concurrent edit")
		}
	}
	store := &memoryStore{}

	_, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if !errors.Is(err, ErrMetadataDrift) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.add != 1 || remote.calls.apply != 0 || remote.calls.submit != 0 {
		t.Fatalf("calls = %#v", remote.calls)
	}
}

func TestEngineRequiresExactRegistrationReadbackIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRemote)
	}{
		{
			name: "typed sale target",
			mutate: func(remote *fakeRemote) {
				remote.state.AppStatus = "SALE"
			},
		},
		{
			name: "raw sale target",
			mutate: func(remote *fakeRemote) {
				remote.state.Source = json.RawMessage(`{
					"contentId":"` + testContentID + `",
					"appStatus":"SALE",
					"defaultLanguageCode":"ENG",
					"paid":"N",
					"publicationType":"01"
				}`)
			},
		},
		{
			name: "raw different content",
			mutate: func(remote *fakeRemote) {
				remote.state.Source = json.RawMessage(`{
					"contentId":"000000000001",
					"appStatus":"REGISTRATION",
					"defaultLanguageCode":"ENG",
					"paid":"N",
					"publicationType":"01"
				}`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			plan := mustPlan(t, fixture)
			remote := newFakeRemote(fixture)
			test.mutate(remote)
			_, err := (Engine{
				Remote: remote,
				Store:  &memoryStore{},
				Now:    func() time.Time { return fixedNow },
			}).Run(context.Background(), plan)
			if err == nil || !strings.Contains(err.Error(), "REGISTRATION") {
				t.Fatalf("Run error = %v", err)
			}
			if remote.calls.session+remote.calls.upload+remote.calls.add+
				remote.calls.apply+remote.calls.submit != 0 {
				t.Fatalf("mutation calls = %#v", remote.calls)
			}
		})
	}
}

func TestEngineAmbiguousSubmitCheckpointAlwaysHalts(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	checkpoint := checkpointThroughUpload(plan)
	checkpoint.BinarySequence = "55"
	checkpoint.complete(StepAddBinary)
	checkpoint.complete(StepApplyMetadata)
	checkpoint.complete(StepVerifyMetadata)
	checkpoint.PendingStep = StepSubmitReview
	checkpoint.AmbiguousSubmission = true
	store := &memoryStore{checkpoint: checkpoint, exists: true}
	remote := newFakeRemote(fixture)

	_, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if !errors.Is(err, ErrAmbiguousSubmission) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.inspect != 0 || remote.calls.submit != 0 {
		t.Fatalf("remote calls = %#v", remote.calls)
	}
}

func TestEngineReconcilesAmbiguousMetadataTransportFromReadback(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	remote.applyErrorAfterMutation = errors.New("connection closed")
	store := &memoryStore{}
	engine := Engine{Remote: remote, Store: store, Now: func() time.Time { return fixedNow }}

	_, err := engine.Run(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("first Run error = %v", err)
	}
	result, err := engine.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || remote.calls.apply != 1 {
		t.Fatalf("result = %#v, calls = %#v", result, remote.calls)
	}
}

func TestEngineRefusesAlreadySubmittedUnownedTarget(t *testing.T) {
	fixture := newFixture(t)
	plan := mustPlan(t, fixture)
	remote := newFakeRemote(fixture)
	remote.state.Source = fixture.desired
	remote.state.ReviewState = ReviewStateSubmitted
	remote.state.Binaries = []RemoteBinary{{Sequence: "71"}}
	checkpoint := checkpointThroughUpload(plan)
	checkpoint.BinarySequence = "71"
	checkpoint.complete(StepAddBinary)
	checkpoint.complete(StepApplyMetadata)
	checkpoint.complete(StepVerifyMetadata)
	store := &memoryStore{checkpoint: checkpoint, exists: true}

	_, err := (Engine{
		Remote: remote,
		Store:  store,
		Now:    func() time.Time { return fixedNow },
	}).Run(context.Background(), plan)
	if !errors.Is(err, ErrUnexpectedSubmission) {
		t.Fatalf("Run error = %v", err)
	}
	if remote.calls.submit != 0 {
		t.Fatalf("submit calls = %d", remote.calls.submit)
	}
}

type memoryStore struct {
	checkpoint Checkpoint
	exists     bool
	saveCalls  int
	failSaveAt int
}

func (store *memoryStore) Load() (Checkpoint, error) {
	if !store.exists {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	return store.checkpoint, nil
}

func (store *memoryStore) Save(checkpoint Checkpoint) error {
	store.saveCalls++
	if store.failSaveAt == store.saveCalls {
		return errors.New("injected checkpoint failure")
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	store.checkpoint = checkpoint
	store.exists = true
	return nil
}

type remoteCalls struct {
	inspect int
	session int
	upload  int
	add     int
	apply   int
	submit  int
}

type fakeRemote struct {
	state                   RemoteState
	desired                 json.RawMessage
	calls                   remoteCalls
	nextSequence            int
	reflectAddedBinary      bool
	applyErrorAfterMutation error
	inspectHook             func(*fakeRemote)
}

func newFakeRemote(fixture fixture) *fakeRemote {
	return &fakeRemote{
		state: RemoteState{
			ContentID:     testContentID,
			AppStatus:     Registration,
			ContentStatus: "REGISTERING",
			Source:        slices.Clone(fixture.base),
			ReviewState:   ReviewStateDraft,
		},
		desired:            slices.Clone(fixture.desired),
		nextSequence:       100,
		reflectAddedBinary: true,
	}
}

func (remote *fakeRemote) InspectRegistration(
	context.Context,
	string,
) (RemoteState, error) {
	remote.calls.inspect++
	if remote.inspectHook != nil {
		remote.inspectHook(remote)
	}
	return RemoteState{
		ContentID:     remote.state.ContentID,
		AppStatus:     remote.state.AppStatus,
		ContentStatus: remote.state.ContentStatus,
		Source:        slices.Clone(remote.state.Source),
		Binaries:      slices.Clone(remote.state.Binaries),
		ReviewState:   remote.state.ReviewState,
	}, nil
}

func (remote *fakeRemote) CreateUploadSession(context.Context) (UploadSession, error) {
	remote.calls.session++
	return UploadSession{
		ID:        fmt.Sprintf("session-%d", remote.calls.session),
		ExpiresAt: fixedNow.Add(24 * time.Hour),
	}, nil
}

func (remote *fakeRemote) UploadBinary(
	context.Context,
	string,
	string,
) (UploadResult, error) {
	remote.calls.upload++
	return UploadResult{FileKey: fmt.Sprintf("file-%d", remote.calls.upload)}, nil
}

func (remote *fakeRemote) AddBinary(
	_ context.Context,
	_ AddBinaryRequest,
) (string, error) {
	remote.calls.add++
	sequence := fmt.Sprintf("%d", remote.nextSequence)
	remote.nextSequence++
	if remote.reflectAddedBinary {
		remote.state.Binaries = append(
			remote.state.Binaries,
			RemoteBinary{Sequence: sequence},
		)
	}
	return sequence, nil
}

func (remote *fakeRemote) ApplyMetadata(
	_ context.Context,
	_ string,
	_ json.RawMessage,
) error {
	remote.calls.apply++
	remote.state.Source = slices.Clone(remote.desired)
	if remote.applyErrorAfterMutation != nil {
		err := remote.applyErrorAfterMutation
		remote.applyErrorAfterMutation = nil
		return err
	}
	return nil
}

func (remote *fakeRemote) SubmitReview(context.Context, string) error {
	remote.calls.submit++
	remote.state.ReviewState = ReviewStateSubmitted
	remote.state.ContentStatus = "UNDER_REVIEW"
	return nil
}

func mustPlan(t *testing.T, fixture fixture) Plan {
	t.Helper()
	plan, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func checkpointThroughUpload(plan Plan) Checkpoint {
	checkpoint := newCheckpoint(plan)
	checkpoint.complete(StepValidateTarget)
	checkpoint.complete(StepCreateUploadSession)
	checkpoint.complete(StepUploadBinary)
	checkpoint.UploadSessionID = "active-session"
	checkpoint.UploadSessionExpiresAt = fixedNow.Add(time.Hour).Format(time.RFC3339Nano)
	checkpoint.FileKey = "active-file"
	return checkpoint
}

func assertCalls(t *testing.T, remote *fakeRemote, want remoteCalls) {
	t.Helper()
	if remote.calls != want {
		t.Fatalf("calls = %#v, want %#v", remote.calls, want)
	}
}

func osWrite(path string, value string) error {
	return os.WriteFile(path, []byte(value), 0o600)
}
