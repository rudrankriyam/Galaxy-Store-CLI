package metadatacmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

func runPull(
	ctx context.Context,
	dependencies Dependencies,
	options pullOptions,
) error {
	status, format, err := validateNetworkOptions(options.Network)
	if err != nil {
		return err
	}
	if dependencies.WriteBundle == nil {
		return errors.New("metadata bundle writer is not configured")
	}
	if dependencies.Now == nil {
		return errors.New("metadata clock is not configured")
	}
	if err := requirePrinter(dependencies); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Network.Profile)
	if err != nil {
		return err
	}
	record, err := fetchRecord(
		ctx,
		service,
		options.Network.ContentID,
		status,
	)
	if err != nil {
		return err
	}
	bundle, err := metadata.NewBundle(record, dependencies.Now())
	if err != nil {
		return fmt.Errorf("create metadata bundle: %w", err)
	}
	if err := dependencies.WriteBundle(
		options.Network.Directory,
		*bundle,
		metadata.WriteOptions{Overwrite: options.Force},
	); err != nil {
		return fmt.Errorf("write metadata bundle: %w", err)
	}
	return dependencies.Printer.Print(format, pullResult{
		Directory:     options.Network.Directory,
		ContentID:     bundle.Manifest.ContentID,
		AppStatus:     bundle.Manifest.AppStatus,
		ContentStatus: bundle.Manifest.ContentStatus,
		SourceSHA256:  bundle.Manifest.SourceSHA256,
		Files: []string{
			metadata.ManifestFilename,
			metadata.MetadataFilename,
			metadata.SourceFilename,
		},
	})
}

func runValidate(
	_ context.Context,
	dependencies Dependencies,
	options validateOptions,
) error {
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := shared.RequireValue("--dir", options.Directory); err != nil {
		return err
	}
	if err := requirePrinter(dependencies); err != nil {
		return err
	}
	bundle, err := readBundle(dependencies, options.Directory)
	if err != nil {
		return err
	}
	return dependencies.Printer.Print(format, validationResult{
		Valid:         true,
		Directory:     options.Directory,
		SchemaVersion: bundle.Manifest.SchemaVersion,
		ContentID:     bundle.Manifest.ContentID,
		AppStatus:     bundle.Manifest.AppStatus,
		SourceSHA256:  bundle.Manifest.SourceSHA256,
	})
}

func runDiff(
	ctx context.Context,
	dependencies Dependencies,
	options networkOptions,
) error {
	status, format, err := validateNetworkOptions(options)
	if err != nil {
		return err
	}
	if err := requirePrinter(dependencies); err != nil {
		return err
	}
	bundle, err := readBundle(dependencies, options.Directory)
	if err != nil {
		return err
	}
	if err := validateBundleIdentity(bundle, options.ContentID, status); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	record, err := fetchRecord(ctx, service, options.ContentID, status)
	if err != nil {
		return err
	}
	plan, err := metadata.Diff(record.Raw, bundle.Metadata)
	if err != nil {
		return fmt.Errorf("diff Galaxy Store metadata: %w", err)
	}
	return dependencies.Printer.Print(
		format,
		newPlanResult(options.ContentID, status, plan),
	)
}

func runApply(
	ctx context.Context,
	dependencies Dependencies,
	options applyOptions,
) error {
	status, format, err := validateNetworkOptions(options.Network)
	if err != nil {
		return err
	}
	if err := requirePrinter(dependencies); err != nil {
		return err
	}
	bundle, err := readBundle(dependencies, options.Network.Directory)
	if err != nil {
		return err
	}
	if err := validateBundleIdentity(
		bundle,
		options.Network.ContentID,
		status,
	); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Network.Profile)
	if err != nil {
		return err
	}
	record, err := fetchRecord(
		ctx,
		service,
		options.Network.ContentID,
		status,
	)
	if err != nil {
		return err
	}
	if err := metadata.VerifyDrift(bundle.Manifest, record.Raw); err != nil {
		return fmt.Errorf(
			"refusing to apply metadata because the live source changed: %w; pull again and review the new diff",
			err,
		)
	}
	plan, err := metadata.Diff(record.Raw, bundle.Metadata)
	if err != nil {
		return fmt.Errorf("plan Galaxy Store metadata update: %w", err)
	}
	planOutput := newPlanResult(
		options.Network.ContentID,
		status,
		plan,
	)
	if options.Mode.DryRun || !plan.HasChanges() {
		return dependencies.Printer.Print(format, planOutput)
	}
	if err := options.Mode.RequireConfirmation(
		"apply the planned Galaxy Store metadata update",
	); err != nil {
		return err
	}

	result, err := service.Update(
		ctx,
		options.Network.ContentID,
		bundle.Metadata,
	)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid content update response")
	}

	readback, err := fetchRecord(
		ctx,
		service,
		options.Network.ContentID,
		status,
	)
	if err != nil {
		return fmt.Errorf("verify metadata update readback: %w", err)
	}
	remaining, err := metadata.Diff(readback.Raw, bundle.Metadata)
	if err != nil {
		return fmt.Errorf("verify metadata update readback: %w", err)
	}
	if remaining.HasChanges() {
		return fmt.Errorf(
			"verify metadata update readback: Samsung still differs on %d field(s)",
			len(remaining.Changes),
		)
	}
	return dependencies.Printer.Print(format, applyResult{
		ContentID:          options.Network.ContentID,
		AppStatus:          status,
		Changes:            plan.Changes,
		Destructive:        plan.HasDestructiveChanges(),
		ResultCode:         result.ResultCode,
		ResultMessage:      result.ResultMessage,
		ReadbackVerified:   true,
		MutationsPerformed: true,
	})
}

func validateNetworkOptions(
	options networkOptions,
) (metadata.AppStatus, output.Format, error) {
	status, err := validateIdentity(options.ContentID, options.AppStatus)
	if err != nil {
		return "", "", err
	}
	if err := shared.RequireValue("--dir", options.Directory); err != nil {
		return "", "", err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return "", "", err
	}
	return status, format, nil
}

func requirePrinter(dependencies Dependencies) error {
	if dependencies.Printer == nil {
		return errors.New("metadata command output printer is not configured")
	}
	return nil
}

func readBundle(
	dependencies Dependencies,
	directory string,
) (*metadata.Bundle, error) {
	if dependencies.ReadBundle == nil {
		return nil, errors.New("metadata bundle reader is not configured")
	}
	bundle, err := dependencies.ReadBundle(directory)
	if err != nil {
		return nil, fmt.Errorf("read metadata bundle: %w", err)
	}
	if bundle == nil {
		return nil, errors.New("read metadata bundle: bundle is nil")
	}
	return bundle, nil
}

func openService(dependencies Dependencies, profile string) (Service, error) {
	if dependencies.OpenService == nil {
		return nil, errors.New("metadata command service opener is not configured")
	}
	service, err := dependencies.OpenService(strings.TrimSpace(profile))
	if err != nil {
		return nil, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return nil, errors.New("open Galaxy Store session: metadata service is nil")
	}
	return service, nil
}

func fetchRecord(
	ctx context.Context,
	service Service,
	contentID string,
	status metadata.AppStatus,
) (apps.App, error) {
	records, err := service.View(ctx, contentID)
	if err != nil {
		return apps.App{}, err
	}
	record, err := metadata.SelectRecord(records, contentID, status)
	if err != nil {
		return apps.App{}, fmt.Errorf("select %s contentInfo record: %w", status, err)
	}
	return record, nil
}
