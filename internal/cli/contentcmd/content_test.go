package contentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
)

const validUpdateJSON = `{
	"contentId":"000007654321",
	"defaultLanguageCode":"ENG",
	"paid":"N",
	"publicationType":"03",
	"screenshots":null,
	"addLanguage":[]
}`

type fakeService struct {
	updateCalls   int
	updateID      string
	updatePayload json.RawMessage

	submitCalls int
	submitID    string

	statusCalls int
	statusID    string
	status      string

	addCalls   int
	addRequest samsungcontent.AddBinaryRequest

	updateBinaryCalls   int
	updateBinaryRequest samsungcontent.UpdateBinaryRequest

	deleteCalls    int
	deleteID       string
	deleteSequence string

	sessionCalls  int
	uploadCalls   int
	uploadSession string
	uploadPath    string

	err error
}

func (service *fakeService) Update(
	_ context.Context,
	contentID string,
	payload json.RawMessage,
) (*samsungcontent.Result, error) {
	service.updateCalls++
	service.updateID = contentID
	service.updatePayload = append(json.RawMessage(nil), payload...)
	if service.err != nil {
		return nil, service.err
	}
	return successResult(), nil
}

func (service *fakeService) Submit(
	_ context.Context,
	contentID string,
) (*samsungcontent.Result, error) {
	service.submitCalls++
	service.submitID = contentID
	if service.err != nil {
		return nil, service.err
	}
	return successResult(), nil
}

func (service *fakeService) ChangeStatus(
	_ context.Context,
	contentID string,
	status string,
) (*samsungcontent.Result, error) {
	service.statusCalls++
	service.statusID = contentID
	service.status = status
	if service.err != nil {
		return nil, service.err
	}
	return successResult(), nil
}

func (service *fakeService) AddBinary(
	_ context.Context,
	request samsungcontent.AddBinaryRequest,
) (*samsungcontent.AddBinaryResult, error) {
	service.addCalls++
	service.addRequest = request
	if service.err != nil {
		return nil, service.err
	}
	result := &samsungcontent.AddBinaryResult{
		ResultCode:    "0000",
		ResultMessage: "Ok",
	}
	result.Data.BinarySequence = "3"
	return result, nil
}

func (service *fakeService) UpdateBinary(
	_ context.Context,
	request samsungcontent.UpdateBinaryRequest,
) (*samsungcontent.Result, error) {
	service.updateBinaryCalls++
	service.updateBinaryRequest = request
	if service.err != nil {
		return nil, service.err
	}
	return successResult(), nil
}

func (service *fakeService) DeleteBinary(
	_ context.Context,
	contentID string,
	binarySequence string,
) (*samsungcontent.Result, error) {
	service.deleteCalls++
	service.deleteID = contentID
	service.deleteSequence = binarySequence
	if service.err != nil {
		return nil, service.err
	}
	return successResult(), nil
}

func (service *fakeService) CreateUploadSession(
	context.Context,
) (*samsungcontent.UploadSession, error) {
	service.sessionCalls++
	if service.err != nil {
		return nil, service.err
	}
	return &samsungcontent.UploadSession{
		URL:       "https://seller.samsungapps.com/galaxyapi/fileUpload",
		SessionID: "session-123",
	}, nil
}

func (service *fakeService) Upload(
	_ context.Context,
	sessionID string,
	path string,
) (*samsungcontent.UploadResult, error) {
	service.uploadCalls++
	service.uploadSession = sessionID
	service.uploadPath = path
	if service.err != nil {
		return nil, service.err
	}
	return &samsungcontent.UploadResult{
		FileKey:  "file-key",
		FileName: filepath.Base(path),
		FileSize: "10",
	}, nil
}

func TestCommandShape(t *testing.T) {
	t.Parallel()

	apps := NewAppsCommand(Dependencies{})
	if apps.Name != "apps" || commandNames(apps.Subcommands) != "update,submit,status" {
		t.Fatalf("apps command = %#v", apps)
	}
	if commandNames(apps.Subcommands[2].Subcommands) != "update" {
		t.Fatalf("status commands = %s", commandNames(apps.Subcommands[2].Subcommands))
	}

	binaries := NewBinariesCommand(Dependencies{})
	if binaries.Name != "binaries" ||
		commandNames(binaries.Subcommands) != "add,update,delete" {
		t.Fatalf("binaries command = %#v", binaries)
	}

	uploads := NewUploadsCommand(Dependencies{})
	if uploads.Name != "uploads" || commandNames(uploads.Subcommands) != "sessions,file" {
		t.Fatalf("uploads command = %#v", uploads)
	}
	if commandNames(uploads.Subcommands[0].Subcommands) != "create" {
		t.Fatalf("session commands = %s", commandNames(uploads.Subcommands[0].Subcommands))
	}
}

func TestAppUpdateDryRunLoadsAndValidatesFileWithoutOpeningSession(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var openCalls int
	var loadCalls int
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		LoadFile: func(path string) ([]byte, error) {
			loadCalls++
			if path != "metadata.json" {
				t.Fatalf("path = %q", path)
			}
			return []byte(validUpdateJSON), nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewAppsCommand(dependencies),
		"update",
		"--content-id", "000007654321",
		"--file", "metadata.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("apps update dry-run: %v", err)
	}
	if loadCalls != 1 || openCalls != 0 {
		t.Fatalf("load calls = %d, open calls = %d", loadCalls, openCalls)
	}
	if !strings.Contains(stdout.String(), `"mutationsPerformed":false`) ||
		!strings.Contains(stdout.String(), `"action":"update app metadata"`) ||
		strings.Contains(stdout.String(), validUpdateJSON) {
		t.Fatalf("dry-run output = %s", stdout.String())
	}
}

func TestAppUpdateConfirmedPassesProfileAndExactPayload(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{}
	var profile string
	events := make([]string, 0, 2)
	dependencies := Dependencies{
		Printer: output.NewPrinter(&stdout, nil),
		LoadFile: func(string) ([]byte, error) {
			events = append(events, "load")
			return []byte(validUpdateJSON), nil
		},
		OpenService: func(value string) (Service, error) {
			events = append(events, "open")
			profile = value
			return service, nil
		},
	}
	err := execute(
		NewAppsCommand(dependencies),
		"update",
		"--content-id", "000007654321",
		"--file", "metadata.json",
		"--profile", "production",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("apps update: %v", err)
	}
	if strings.Join(events, ",") != "load,open" {
		t.Fatalf("events = %v, want local load before credentials", events)
	}
	if profile != "production" ||
		service.updateCalls != 1 ||
		service.updateID != "000007654321" {
		t.Fatalf(
			"profile=%q calls=%d ID=%q",
			profile,
			service.updateCalls,
			service.updateID,
		)
	}
	if compact(t, service.updatePayload) != compact(t, json.RawMessage(validUpdateJSON)) {
		t.Fatalf("payload = %s", service.updatePayload)
	}
	if !strings.Contains(stdout.String(), `"action":"update metadata"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestUpdateRejectsLegacyBinaryListBeforeOpeningSession(t *testing.T) {
	t.Parallel()

	var openCalls int
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		LoadFile: func(string) ([]byte, error) {
			return []byte(`{
				"contentId":"000007654321",
				"defaultLanguageCode":"ENG",
				"paid":"N",
				"publicationType":"01",
				"binaryList":[]
			}`), nil
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewAppsCommand(dependencies),
		"update",
		"--content-id", "000007654321",
		"--file", "metadata.json",
		"--confirm",
	)
	if err == nil || !strings.Contains(err.Error(), "binaryList") {
		t.Fatalf("error = %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("open calls = %d, want 0", openCalls)
	}
}

func TestConfirmationIsRequiredBeforeSessionResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command func(Dependencies) *ffcli.Command
		args    []string
	}{
		{
			name:    "submit",
			command: NewAppsCommand,
			args:    []string{"submit", "--content-id", "000007654321"},
		},
		{
			name:    "status",
			command: NewAppsCommand,
			args: []string{
				"status", "update",
				"--content-id", "000007654321",
				"--status", "SUSPENDED",
			},
		},
		{
			name:    "binary add",
			command: NewBinariesCommand,
			args: []string{
				"add",
				"--content-id", "000007654321",
				"--file-key", "file-key",
				"--gms", "N",
			},
		},
		{
			name:    "binary update",
			command: NewBinariesCommand,
			args: []string{
				"update",
				"--content-id", "000007654321",
				"--binary-seq", "1",
				"--gms", "N",
			},
		},
		{
			name:    "binary delete",
			command: NewBinariesCommand,
			args: []string{
				"delete",
				"--content-id", "000007654321",
				"--binary-seq", "1",
			},
		},
		{
			name:    "session create",
			command: NewUploadsCommand,
			args:    []string{"sessions", "create"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(test.command(dependencies), test.args...)
			if !errors.Is(err, shared.ErrConfirmationRequired) {
				t.Fatalf("error = %v, want confirmation required", err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
		})
	}
}

func TestConfirmedAppAndBinaryCommandsUseExactServiceInputs(t *testing.T) {
	t.Parallel()

	service := &fakeService{}
	dependencies := baseDependencies(io.Discard, service)

	if err := execute(
		NewAppsCommand(dependencies),
		"submit",
		"--content-id", "000007654321",
		"--confirm",
	); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := execute(
		NewAppsCommand(dependencies),
		"status", "update",
		"--content-id", "000007654321",
		"--status", " suspended ",
		"--confirm",
	); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := execute(
		NewBinariesCommand(dependencies),
		"add",
		"--content-id", "000007654321",
		"--file-key", "file-key",
		"--gms", "n",
		"--copy-device-config-from", "2",
		"--confirm",
	); err != nil {
		t.Fatalf("binary add: %v", err)
	}
	if err := execute(
		NewBinariesCommand(dependencies),
		"update",
		"--content-id", "000007654321",
		"--binary-seq", "3",
		"--gms", "y",
		"--confirm",
	); err != nil {
		t.Fatalf("binary update: %v", err)
	}
	if err := execute(
		NewBinariesCommand(dependencies),
		"delete",
		"--content-id", "000007654321",
		"--binary-seq", "2",
		"--confirm",
	); err != nil {
		t.Fatalf("binary delete: %v", err)
	}

	if service.submitCalls != 1 || service.submitID != "000007654321" {
		t.Fatalf("submit calls=%d ID=%q", service.submitCalls, service.submitID)
	}
	if service.statusCalls != 1 ||
		service.statusID != "000007654321" ||
		service.status != "SUSPENDED" {
		t.Fatalf(
			"status calls=%d ID=%q status=%q",
			service.statusCalls,
			service.statusID,
			service.status,
		)
	}
	if service.addCalls != 1 ||
		service.addRequest != (samsungcontent.AddBinaryRequest{
			ContentID:                   "000007654321",
			FileKey:                     "file-key",
			GMS:                         "N",
			BinarySequenceForDeviceInfo: "2",
		}) {
		t.Fatalf("add request = %+v", service.addRequest)
	}
	if service.updateBinaryCalls != 1 ||
		service.updateBinaryRequest != (samsungcontent.UpdateBinaryRequest{
			ContentID:      "000007654321",
			BinarySequence: "3",
			GMS:            "Y",
		}) {
		t.Fatalf("update request = %+v", service.updateBinaryRequest)
	}
	if service.deleteCalls != 1 ||
		service.deleteID != "000007654321" ||
		service.deleteSequence != "2" {
		t.Fatalf(
			"delete calls=%d ID=%q sequence=%q",
			service.deleteCalls,
			service.deleteID,
			service.deleteSequence,
		)
	}
}

func TestUploadFileValidatesLocallyBeforeOpeningSession(t *testing.T) {
	t.Parallel()

	service := &fakeService{}
	var events []string
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		ValidateFile: func(path string) error {
			events = append(events, "validate:"+path)
			return nil
		},
		OpenService: func(string) (Service, error) {
			events = append(events, "open")
			return service, nil
		},
	}
	err := execute(
		NewUploadsCommand(dependencies),
		"file",
		"--session-id", "session-123",
		"--file", "app.aab",
		"--confirm",
	)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	if strings.Join(events, ",") != "validate:app.aab,open" {
		t.Fatalf("events = %v", events)
	}
	if service.uploadCalls != 1 ||
		service.uploadSession != "session-123" ||
		service.uploadPath != "app.aab" {
		t.Fatalf(
			"upload calls=%d session=%q path=%q",
			service.uploadCalls,
			service.uploadSession,
			service.uploadPath,
		)
	}
}

func TestUploadFileValidationFailureHasNoCredentialOrNetworkSideEffects(t *testing.T) {
	t.Parallel()

	var openCalls int
	dependencies := Dependencies{
		Printer: output.NewPrinter(io.Discard, nil),
		ValidateFile: func(string) error {
			return errors.New("not a regular file")
		},
		OpenService: func(string) (Service, error) {
			openCalls++
			return &fakeService{}, nil
		},
	}
	err := execute(
		NewUploadsCommand(dependencies),
		"file",
		"--session-id", "session",
		"--file", "bad",
		"--confirm",
	)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error = %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("open calls = %d, want 0", openCalls)
	}
}

func TestUploadSessionAndFileReturnUsefulJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	service := &fakeService{}
	dependencies := baseDependencies(&stdout, service)
	if err := execute(
		NewUploadsCommand(dependencies),
		"sessions", "create",
		"--confirm",
		"--output", "json",
	); err != nil {
		t.Fatalf("session create: %v", err)
	}
	if !strings.Contains(stdout.String(), `"sessionId":"session-123"`) {
		t.Fatalf("session output = %s", stdout.String())
	}

	stdout.Reset()
	if err := execute(
		NewUploadsCommand(dependencies),
		"file",
		"--session-id", "session-123",
		"--file", "app.aab",
		"--confirm",
		"--output", "json",
	); err != nil {
		t.Fatalf("upload file: %v", err)
	}
	if !strings.Contains(stdout.String(), `"fileKey":"file-key"`) {
		t.Fatalf("upload output = %s", stdout.String())
	}
}

func TestInvalidInputsNeverOpenSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command func(Dependencies) *ffcli.Command
		args    []string
	}{
		{
			name:    "bad content ID",
			command: NewAppsCommand,
			args:    []string{"submit", "--content-id", "bad", "--confirm"},
		},
		{
			name:    "bad status",
			command: NewAppsCommand,
			args: []string{
				"status", "update",
				"--content-id", "000007654321",
				"--status", "REGISTERING",
				"--confirm",
			},
		},
		{
			name:    "bad GMS",
			command: NewBinariesCommand,
			args: []string{
				"add",
				"--content-id", "000007654321",
				"--file-key", "key",
				"--gms", "maybe",
				"--confirm",
			},
		},
		{
			name:    "bad binary sequence",
			command: NewBinariesCommand,
			args: []string{
				"delete",
				"--content-id", "000007654321",
				"--binary-seq", "1.5",
				"--confirm",
			},
		},
		{
			name:    "bad output",
			command: NewUploadsCommand,
			args:    []string{"sessions", "create", "--output", "yaml", "--confirm"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var openCalls int
			dependencies := Dependencies{
				Printer: output.NewPrinter(io.Discard, nil),
				OpenService: func(string) (Service, error) {
					openCalls++
					return &fakeService{}, nil
				},
			}
			err := execute(test.command(dependencies), test.args...)
			var usageError *shared.UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("error = %T %v, want usage error", err, err)
			}
			if openCalls != 0 {
				t.Fatalf("open calls = %d, want 0", openCalls)
			}
		})
	}
}

func TestDefaultFileValidationRejectsSymlink(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "metadata.json")
	if err := os.WriteFile(target, []byte(validUpdateJSON), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := loadRegularFile(link); err == nil ||
		!strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v", err)
	}
	if err := validateRegularFile(link); err == nil ||
		!strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceErrorsAreReturned(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("samsung unavailable")
	service := &fakeService{err: sentinel}
	dependencies := baseDependencies(io.Discard, service)
	err := execute(
		NewAppsCommand(dependencies),
		"submit",
		"--content-id", "000007654321",
		"--confirm",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
}

func successResult() *samsungcontent.Result {
	return &samsungcontent.Result{ResultCode: "0000", ResultMessage: "Ok"}
}

func baseDependencies(stdout io.Writer, service Service) Dependencies {
	return Dependencies{
		Printer: output.NewPrinter(stdout, nil),
		OpenService: func(string) (Service, error) {
			return service, nil
		},
		LoadFile: func(string) ([]byte, error) {
			return []byte(validUpdateJSON), nil
		},
		ValidateFile: func(string) error {
			return nil
		},
	}
}

func commandNames(commands []*ffcli.Command) string {
	names := make([]string, len(commands))
	for index, command := range commands {
		names[index] = command.Name
	}
	return strings.Join(names, ",")
}

func compact(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, encoded); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return buffer.String()
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}
