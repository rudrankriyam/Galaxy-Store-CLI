package contentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
)

func runUpdate(ctx context.Context, dependencies Dependencies, options updateOptions) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	if err := shared.RequireValue("--file", options.File); err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	if dependencies.LoadFile == nil {
		return errors.New("content command file loader is not configured")
	}
	payload, err := dependencies.LoadFile(options.File)
	if err != nil {
		return fmt.Errorf("load --file: %w", err)
	}
	if !json.Valid(payload) {
		return shared.UsageErrorf("--file must contain one valid JSON object")
	}
	if err := samsungcontent.ValidateUpdatePayload(
		options.ContentID,
		json.RawMessage(payload),
	); err != nil {
		return shared.UsageErrorf("%v", err)
	}

	plan := newPlan(
		"update app metadata",
		"app:"+options.ContentID,
		"send validated contentUpdate JSON from "+options.File,
		[]string{
			"omitted, null, and empty collections have different Samsung semantics",
			"binaryList is forbidden; use gsc binaries commands",
		},
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"update Galaxy Store metadata",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.Update(ctx, options.ContentID, json.RawMessage(payload))
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid content update response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:        "update metadata",
		Resource:      options.ContentID,
		ResultCode:    result.ResultCode,
		ResultMessage: result.ResultMessage,
	})
}

func runSubmit(ctx context.Context, dependencies Dependencies, options submitOptions) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	plan := newPlan(
		"submit app",
		"app:"+options.ContentID,
		"submit the REGISTERING app for Samsung review",
		[]string{"Samsung requires a REGISTERING app; no SALE variant is selected or modified"},
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"submit the app for Galaxy Store review",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.Submit(ctx, options.ContentID)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid submission response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:        "submit",
		Resource:      options.ContentID,
		ResultCode:    result.ResultCode,
		ResultMessage: result.ResultMessage,
	})
}

func runStatusUpdate(ctx context.Context, dependencies Dependencies, options statusOptions) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	status, err := normalizeContentStatus(options.Status)
	if err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	warnings := []string(nil)
	if status == "TERMINATED" {
		warnings = append(warnings, "TERMINATED ends sale and cannot be used unless the app is already SUSPENDED")
	}
	plan := newPlan(
		"update app status",
		"app:"+options.ContentID,
		"set contentStatus to "+status,
		warnings,
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"change Galaxy Store app status",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.ChangeStatus(ctx, options.ContentID, status)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid status update response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:        "update status",
		Resource:      options.ContentID,
		Status:        status,
		ResultCode:    result.ResultCode,
		ResultMessage: result.ResultMessage,
	})
}

func runBinaryAdd(ctx context.Context, dependencies Dependencies, options binaryAddOptions) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	if err := shared.RequireValue("--file-key", options.FileKey); err != nil {
		return err
	}
	if options.FileKey != strings.TrimSpace(options.FileKey) {
		return shared.UsageErrorf("--file-key must not contain surrounding whitespace")
	}
	gms, err := normalizeGMS(options.GMS)
	if err != nil {
		return err
	}
	if options.CopyDeviceConfigurationFrom != "" {
		if err := validateBinarySequence(
			"--copy-device-config-from",
			options.CopyDeviceConfigurationFrom,
		); err != nil {
			return err
		}
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	details := "register uploaded file key with gms=" + gms
	if options.CopyDeviceConfigurationFrom != "" {
		details += " and copy device configuration from binary " +
			options.CopyDeviceConfigurationFrom
	}
	plan := newPlan(
		"add binary",
		"app:"+options.ContentID,
		details,
		[]string{"Samsung requires the target app to be REGISTERING"},
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"register the Galaxy Store binary",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.AddBinary(ctx, samsungcontent.AddBinaryRequest{
		ContentID:                   options.ContentID,
		FileKey:                     options.FileKey,
		GMS:                         gms,
		BinarySequenceForDeviceInfo: options.CopyDeviceConfigurationFrom,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid add binary response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:         "add binary",
		Resource:       options.ContentID,
		BinarySequence: string(result.Data.BinarySequence),
		ResultCode:     result.ResultCode,
		ResultMessage:  result.ResultMessage,
	})
}

func runBinaryUpdate(
	ctx context.Context,
	dependencies Dependencies,
	options binaryUpdateOptions,
) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	if err := validateBinarySequence("--binary-seq", options.BinarySequence); err != nil {
		return err
	}
	gms, err := normalizeGMS(options.GMS)
	if err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	plan := newPlan(
		"update binary",
		"app:"+options.ContentID+"/binary:"+options.BinarySequence,
		"set gms="+gms,
		[]string{"Samsung requires the target app to be REGISTERING"},
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"update the Galaxy Store binary",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.UpdateBinary(ctx, samsungcontent.UpdateBinaryRequest{
		ContentID:      options.ContentID,
		BinarySequence: options.BinarySequence,
		GMS:            gms,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid update binary response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:         "update binary",
		Resource:       options.ContentID,
		BinarySequence: options.BinarySequence,
		ResultCode:     result.ResultCode,
		ResultMessage:  result.ResultMessage,
	})
}

func runBinaryDelete(
	ctx context.Context,
	dependencies Dependencies,
	options binaryDeleteOptions,
) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	if err := validateBinarySequence("--binary-seq", options.BinarySequence); err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	plan := newPlan(
		"delete binary",
		"app:"+options.ContentID+"/binary:"+options.BinarySequence,
		"permanently remove the registered binary",
		[]string{
			"deleted binaries become inaccessible through Galaxy Store",
			"Samsung requires the target app to be REGISTERING",
		},
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"permanently delete the Galaxy Store binary",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.DeleteBinary(ctx, options.ContentID, options.BinarySequence)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid delete binary response")
	}
	return dependencies.Printer.Print(format, mutationResult{
		Action:         "delete binary",
		Resource:       options.ContentID,
		BinarySequence: options.BinarySequence,
		ResultCode:     result.ResultCode,
		ResultMessage:  result.ResultMessage,
	})
}

func runUploadSessionCreate(
	ctx context.Context,
	dependencies Dependencies,
	options commonOptions,
) error {
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	plan := newPlan(
		"create upload session",
		"upload-session",
		"create a temporary Samsung upload session valid for 24 hours",
		nil,
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Mode,
		plan,
		"create a Samsung upload session",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	result, err := service.CreateUploadSession(ctx)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid upload session response")
	}
	return dependencies.Printer.Print(format, uploadSessionResult{UploadSession: result})
}

func runUploadFile(
	ctx context.Context,
	dependencies Dependencies,
	options uploadFileOptions,
) error {
	if err := shared.RequireValue("--session-id", options.SessionID); err != nil {
		return err
	}
	if options.SessionID != strings.TrimSpace(options.SessionID) {
		return shared.UsageErrorf("--session-id must not contain surrounding whitespace")
	}
	if err := shared.RequireValue("--file", options.File); err != nil {
		return err
	}
	format, err := parseOutput(options.Common.Output)
	if err != nil {
		return err
	}
	if dependencies.ValidateFile == nil {
		return errors.New("content command file validator is not configured")
	}
	if err := dependencies.ValidateFile(options.File); err != nil {
		return fmt.Errorf("validate --file: %w", err)
	}
	plan := newPlan(
		"upload file",
		"upload-session:"+options.SessionID,
		"stream regular file "+options.File+" to Samsung's fixed upload host",
		nil,
	)
	execute, err := approveMutation(
		dependencies,
		format,
		options.Common.Mode,
		plan,
		"upload the file to Samsung",
	)
	if err != nil || !execute {
		return err
	}

	service, err := openService(dependencies, options.Common.Profile)
	if err != nil {
		return err
	}
	result, err := service.Upload(ctx, options.SessionID, options.File)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid file upload response")
	}
	return dependencies.Printer.Print(format, uploadFileResult{UploadResult: result})
}

func validateContentID(contentID string) error {
	if err := shared.RequireValue("--content-id", contentID); err != nil {
		return err
	}
	return shared.ValidateContentID(contentID)
}

func normalizeContentStatus(value string) (string, error) {
	if err := shared.RequireValue("--status", value); err != nil {
		return "", err
	}
	status := strings.ToUpper(strings.TrimSpace(value))
	switch status {
	case "FOR_SALE", "SUSPENDED", "TERMINATED":
		return status, nil
	default:
		return "", shared.UsageErrorf(
			"--status must be FOR_SALE, SUSPENDED, or TERMINATED",
		)
	}
}

func normalizeGMS(value string) (string, error) {
	if err := shared.RequireValue("--gms", value); err != nil {
		return "", err
	}
	gms := strings.ToUpper(strings.TrimSpace(value))
	if gms != "Y" && gms != "N" {
		return "", shared.UsageErrorf("--gms must be Y or N")
	}
	return gms, nil
}

func validateBinarySequence(flagName string, value string) error {
	if err := shared.RequireValue(flagName, value); err != nil {
		return err
	}
	if value != strings.TrimSpace(value) {
		return shared.UsageErrorf("%s must contain only digits", flagName)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return shared.UsageErrorf("%s must contain only digits", flagName)
		}
	}
	return nil
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func approveMutation(
	dependencies Dependencies,
	format output.Format,
	mode shared.MutationMode,
	plan planResult,
	action string,
) (bool, error) {
	if dependencies.Printer == nil {
		return false, errors.New("content command output printer is not configured")
	}
	if mode.DryRun {
		if err := dependencies.Printer.Print(format, plan); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := mode.RequireConfirmation(action); err != nil {
		return false, err
	}
	return true, nil
}

func openService(dependencies Dependencies, profile string) (Service, error) {
	if dependencies.OpenService == nil {
		return nil, errors.New("content command service opener is not configured")
	}
	service, err := dependencies.OpenService(strings.TrimSpace(profile))
	if err != nil {
		return nil, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return nil, errors.New("open Galaxy Store session: content service is nil")
	}
	return service, nil
}

func newPlan(
	action string,
	resource string,
	details string,
	warnings []string,
) planResult {
	return planResult{Plan: shared.Plan{
		Operations: []shared.Operation{{
			Action:   action,
			Resource: resource,
			Details:  details,
		}},
		Warnings:             append([]string(nil), warnings...),
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}}
}
