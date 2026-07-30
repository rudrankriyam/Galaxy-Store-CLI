package shipcmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

func runPlan(
	_ context.Context,
	dependencies Dependencies,
	options planOptions,
) error {
	format, plan, err := preparePlan(dependencies, options)
	if err != nil {
		return err
	}
	return dependencies.Printer.Print(format, newPlanResult(plan))
}

func runShip(
	ctx context.Context,
	dependencies Dependencies,
	options runOptions,
) (returnErr error) {
	format, plan, err := preparePlan(dependencies, options.Plan)
	if err != nil {
		return err
	}
	if options.DryRun {
		return dependencies.Printer.Print(format, newPlanResult(plan))
	}
	if err := (shared.MutationMode{
		DryRun:  options.DryRun,
		Confirm: options.Confirm,
	}).RequireConfirmation(
		"upload the binary, register it, apply metadata, and submit REGISTRATION for review",
	); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if dependencies.NewStore == nil {
		return errors.New("shipping checkpoint store factory is not configured")
	}
	if dependencies.AcquireLock == nil {
		return errors.New("shipping checkpoint lock factory is not configured")
	}
	if dependencies.OpenRemote == nil {
		return errors.New("shipping remote factory is not configured")
	}

	checkpointPath := strings.TrimSpace(options.CheckpointPath)
	if checkpointPath == "" {
		checkpointPath = filepath.Join(
			".gsc",
			"ship-"+plan.ContentID+".json",
		)
	}
	store, err := dependencies.NewStore(checkpointPath)
	if err != nil {
		return fmt.Errorf("open shipping checkpoint: %w", err)
	}
	if store == nil {
		return errors.New("open shipping checkpoint: store is nil")
	}
	lock, err := dependencies.AcquireLock(checkpointPath)
	if err != nil {
		return err
	}
	if lock == nil {
		return errors.New("acquire shipping checkpoint lock: lock is nil")
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Release())
	}()

	remote, err := dependencies.OpenRemote(strings.TrimSpace(options.Profile))
	if err != nil {
		return fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if remote == nil {
		return errors.New("open Galaxy Store session: shipping remote is nil")
	}
	result, err := (ship.Engine{
		Remote: remote,
		Store:  store,
	}).Run(ctx, plan)
	if err != nil {
		return err
	}
	return dependencies.Printer.Print(
		format,
		newRunResult(plan, result),
	)
}

func preparePlan(
	dependencies Dependencies,
	options planOptions,
) (outputFormat output.Format, plan ship.Plan, err error) {
	format, err := parseOutput(options.Output)
	if err != nil {
		return "", ship.Plan{}, err
	}
	if err := requirePrinter(dependencies); err != nil {
		return "", ship.Plan{}, err
	}
	plan, err = ship.BuildPlan(ship.Request{
		ContentID:                   options.ContentID,
		AppStatus:                   ship.Registration,
		BinaryPath:                  options.BinaryPath,
		MetadataDirectory:           options.MetadataDirectory,
		GMS:                         options.GMS,
		CopyDeviceConfigurationFrom: options.CopyDeviceConfigurationFrom,
	})
	if err != nil {
		return "", ship.Plan{}, fmt.Errorf("build shipping plan: %w", err)
	}
	return format, plan, nil
}
