package ship

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

const testContentID = "000007654321"

func TestBuildPlanIsDeterministicAndBoundToInputs(t *testing.T) {
	fixture := newFixture(t)
	first, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("plan IDs differ: %q != %q", first.ID, second.ID)
	}
	if len(first.MetadataChanges) != 1 ||
		first.MetadataChanges[0].Path != "/appTitle" ||
		first.MetadataDestructive {
		t.Fatalf("metadata plan = %#v", first)
	}
	if first.AppStatus != Registration {
		t.Fatalf("app status = %q", first.AppStatus)
	}
	if first.Binary.Size != int64(len("binary-one")) {
		t.Fatalf("binary size = %d", first.Binary.Size)
	}
	if !validSHA256(first.Binary.SHA256) ||
		!validSHA256(first.Metadata.BundleSHA256) {
		t.Fatal("plan identities are not SHA-256 values")
	}
	if err := first.ValidateInputs(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlanRequiresExactRegistrationTarget(t *testing.T) {
	fixture := newFixture(t)
	for _, status := range []string{"", "registration", "SALE"} {
		request := fixture.request
		request.AppStatus = status
		if _, err := BuildPlan(request); err == nil ||
			!strings.Contains(err.Error(), "exactly REGISTRATION") {
			t.Fatalf("BuildPlan status %q error = %v", status, err)
		}
	}
}

func TestBuildPlanRejectsSaleMetadataBundle(t *testing.T) {
	fixture := newFixtureWithStatus(t, metadata.AppStatusSale)
	_, err := BuildPlan(fixture.request)
	if err == nil || !strings.Contains(err.Error(), "exactly REGISTRATION") {
		t.Fatalf("BuildPlan error = %v", err)
	}
}

func TestPlanValidateInputsDetectsBinaryAndMetadataChanges(t *testing.T) {
	fixture := newFixture(t)
	plan, err := BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.request.BinaryPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateInputs(); err == nil ||
		!strings.Contains(err.Error(), "binary changed") {
		t.Fatalf("ValidateInputs binary error = %v", err)
	}

	fixture = newFixture(t)
	plan, err = BuildPlan(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := metadata.ReadBundle(fixture.request.MetadataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Metadata = desiredEnvelope("changed again")
	if err := metadata.WriteBundle(
		fixture.request.MetadataDirectory,
		*bundle,
		metadata.WriteOptions{Overwrite: true},
	); err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateInputs(); err == nil ||
		!strings.Contains(err.Error(), "metadata bundle changed") {
		t.Fatalf("ValidateInputs metadata error = %v", err)
	}
}

func TestBuildPlanRejectsSymlinkAndUnsupportedBinary(t *testing.T) {
	fixture := newFixture(t)
	link := filepath.Join(t.TempDir(), "app.aab")
	if err := os.Symlink(fixture.request.BinaryPath, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	request := fixture.request
	request.BinaryPath = link
	if _, err := BuildPlan(request); err == nil ||
		!strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("BuildPlan symlink error = %v", err)
	}

	unsupported := filepath.Join(t.TempDir(), "app.zip")
	if err := os.WriteFile(unsupported, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.BinaryPath = unsupported
	if _, err := BuildPlan(request); err == nil ||
		!strings.Contains(err.Error(), "APK or AAB") {
		t.Fatalf("BuildPlan extension error = %v", err)
	}
}

type fixture struct {
	request Request
	base    json.RawMessage
	desired json.RawMessage
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWithStatus(t, metadata.AppStatusRegistration)
}

func newFixtureWithStatus(t *testing.T, status metadata.AppStatus) fixture {
	t.Helper()
	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "app.aab")
	if err := os.WriteFile(binaryPath, []byte("binary-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	appStatus := string(status)
	contentStatus := "REGISTERING"
	if status == metadata.AppStatusSale {
		contentStatus = "FOR_SALE"
	}
	base := json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"appStatus":"` + appStatus + `",
		"contentStatus":"` + contentStatus + `",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"Before",
		"binaryList":[]
	}`)
	record := apps.App{
		ContentID:     testContentID,
		AppStatus:     appStatus,
		ContentStatus: contentStatus,
		Raw:           base,
	}
	bundle, err := metadata.NewBundle(record, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	desired := desiredEnvelope("After")
	bundle.Metadata = desired
	metadataDirectory := filepath.Join(directory, "metadata")
	if err := metadata.WriteBundle(
		metadataDirectory,
		*bundle,
		metadata.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	return fixture{
		request: Request{
			ContentID:         testContentID,
			AppStatus:         Registration,
			BinaryPath:        binaryPath,
			MetadataDirectory: metadataDirectory,
			GMS:               "N",
		},
		base:    base,
		desired: desiredSource("After"),
	}
}

func desiredEnvelope(title string) json.RawMessage {
	return json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"` + title + `"
	}`)
}

func desiredSource(title string) json.RawMessage {
	return json.RawMessage(`{
		"contentId":"` + testContentID + `",
		"appStatus":"REGISTRATION",
		"contentStatus":"REGISTERING",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"01",
		"appTitle":"` + title + `",
		"binaryList":[]
	}`)
}
