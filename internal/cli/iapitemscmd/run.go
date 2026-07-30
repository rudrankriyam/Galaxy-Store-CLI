package iapitemscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/items"
)

const maxInputFileBytes = 4 << 20

type listOptions struct {
	PackageName string
	Page        int
	Size        int
	Profile     string
	Output      string
}

type itemOptions struct {
	PackageName string
	ItemID      string
	Profile     string
	Output      string
}

type fileMutationOptions struct {
	PackageName string
	File        string
	Profile     string
	Output      string
	Mode        shared.MutationMode
}

type deleteOptions struct {
	PackageName string
	ItemID      string
	Profile     string
	Output      string
	Mode        shared.MutationMode
}

func runList(ctx context.Context, dependencies Dependencies, options listOptions) error {
	packageName, format, err := validatePackageAndOutput(options.PackageName, options.Output)
	if err != nil {
		return err
	}
	if options.Page < 1 {
		return shared.UsageErrorf("--page must be at least 1")
	}
	if options.Size < 1 {
		return shared.UsageErrorf("--size must be at least 1")
	}
	if err := validateDependencies(dependencies, false, true); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	result, err := service.List(ctx, packageName, items.ListOptions{
		Page: options.Page,
		Size: options.Size,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid IAP item list response")
	}
	return dependencies.Printer.Print(format, listOutput{ListResult: result})
}

func runView(ctx context.Context, dependencies Dependencies, options itemOptions) error {
	packageName, itemID, format, err := validateItemAndOutput(
		options.PackageName,
		options.ItemID,
		options.Output,
	)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies, false, true); err != nil {
		return err
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	item, err := service.View(ctx, packageName, itemID)
	if err != nil {
		return err
	}
	if item == nil {
		return errors.New("samsung returned an invalid IAP item response")
	}
	return dependencies.Printer.Print(format, itemOutput{Item: item})
}

func runFileMutation(
	ctx context.Context,
	action string,
	dependencies Dependencies,
	options fileMutationOptions,
) error {
	packageName, format, err := validatePackageAndOutput(options.PackageName, options.Output)
	if err != nil {
		return err
	}
	file := strings.TrimSpace(options.File)
	if err := shared.RequireValue("--file", file); err != nil {
		return err
	}
	if options.File != file {
		return shared.UsageErrorf("--file must not have surrounding whitespace")
	}
	if err := options.Mode.RequireConfirmation(action + " the Samsung IAP item"); err != nil {
		return err
	}
	if err := validateDependencies(dependencies, true, !options.Mode.DryRun); err != nil {
		return err
	}

	data, err := dependencies.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read --file %q: %w", file, err)
	}
	if len(data) > maxInputFileBytes {
		return shared.UsageErrorf("--file exceeds the %d-byte input limit", maxInputFileBytes)
	}

	var itemID string
	var execute func(Service) (*items.Item, error)
	switch action {
	case "create", "replace":
		request, decodeErr := decodeJSONFile[items.FullRequest](data)
		if decodeErr != nil {
			return decodeErr
		}
		if presenceErr := validatePaymentMethodPresence(data); presenceErr != nil {
			return presenceErr
		}
		if validateErr := request.Validate(); validateErr != nil {
			return shared.UsageErrorf("invalid --file: %v", validateErr)
		}
		itemID = request.ID
		if action == "create" {
			execute = func(service Service) (*items.Item, error) {
				return service.Create(ctx, packageName, request)
			}
		} else {
			execute = func(service Service) (*items.Item, error) {
				return service.Replace(ctx, packageName, request)
			}
		}
	case "update":
		request, decodeErr := decodeJSONFile[items.UpdateRequest](data)
		if decodeErr != nil {
			return decodeErr
		}
		if validateErr := request.Validate(); validateErr != nil {
			return shared.UsageErrorf("invalid --file: %v", validateErr)
		}
		itemID = request.ID
		execute = func(service Service) (*items.Item, error) {
			return service.Update(ctx, packageName, request)
		}
	default:
		return errors.New("unsupported IAP item mutation")
	}

	if options.Mode.DryRun {
		return dependencies.Printer.Print(
			format,
			newMutationPlan(action, packageName, itemID),
		)
	}
	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	result, err := execute(service)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid IAP item mutation response")
	}
	return dependencies.Printer.Print(format, itemOutput{Item: result})
}

func runDelete(ctx context.Context, dependencies Dependencies, options deleteOptions) error {
	packageName, itemID, format, err := validateItemAndOutput(
		options.PackageName,
		options.ItemID,
		options.Output,
	)
	if err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation("delete the Samsung IAP item"); err != nil {
		return err
	}
	if err := validateDependencies(dependencies, false, !options.Mode.DryRun); err != nil {
		return err
	}
	if options.Mode.DryRun {
		return dependencies.Printer.Print(
			format,
			newMutationPlan("delete", packageName, itemID),
		)
	}

	service, err := openService(dependencies, options.Profile)
	if err != nil {
		return err
	}
	result, err := service.Delete(ctx, packageName, itemID)
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid IAP item delete response")
	}
	return dependencies.Printer.Print(format, itemOutput{Item: result})
}

func validatePackageAndOutput(
	packageValue string,
	outputValue string,
) (string, output.Format, error) {
	packageName := strings.TrimSpace(packageValue)
	if err := shared.RequireValue("--package-name", packageName); err != nil {
		return "", "", err
	}
	if packageName != packageValue {
		return "", "", shared.UsageErrorf("--package-name must not have surrounding whitespace")
	}
	if err := items.ValidatePackageName(packageName); err != nil {
		return "", "", shared.UsageErrorf("invalid --package-name: %v", err)
	}
	format, err := parseOutput(outputValue)
	if err != nil {
		return "", "", err
	}
	return packageName, format, nil
}

func validateItemAndOutput(
	packageValue string,
	itemValue string,
	outputValue string,
) (string, string, output.Format, error) {
	packageName, format, err := validatePackageAndOutput(packageValue, outputValue)
	if err != nil {
		return "", "", "", err
	}
	itemID := strings.TrimSpace(itemValue)
	if err := shared.RequireValue("--item-id", itemID); err != nil {
		return "", "", "", err
	}
	if itemID != itemValue {
		return "", "", "", shared.UsageErrorf("--item-id must not have surrounding whitespace")
	}
	if err := items.ValidateItemID(itemID); err != nil {
		return "", "", "", shared.UsageErrorf("invalid --item-id: %v", err)
	}
	return packageName, itemID, format, nil
}

func parseOutput(value string) (output.Format, error) {
	format, err := output.ParseFormat(value)
	if err != nil {
		return "", shared.UsageErrorf("%v", err)
	}
	return format, nil
}

func decodeJSONFile[T any](data []byte) (T, error) {
	var result T
	if len(bytes.TrimSpace(data)) == 0 {
		return result, shared.UsageErrorf("--file must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, shared.UsageErrorf("decode --file: %v", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == nil {
		return result, shared.UsageErrorf("--file must contain exactly one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return result, shared.UsageErrorf("decode --file: %v", err)
	}
	return result, nil
}

func validatePaymentMethodPresence(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return shared.UsageErrorf("decode --file: %v", err)
	}
	rawMethod, exists := object["itemPaymentMethod"]
	if !exists || bytes.Equal(bytes.TrimSpace(rawMethod), []byte("null")) {
		return shared.UsageErrorf(
			"invalid --file: itemPaymentMethod.phoneBillStatus is required",
		)
	}
	var method map[string]json.RawMessage
	if err := json.Unmarshal(rawMethod, &method); err != nil {
		return shared.UsageErrorf("decode --file itemPaymentMethod: %v", err)
	}
	if _, exists := method["phoneBillStatus"]; !exists {
		return shared.UsageErrorf(
			"invalid --file: itemPaymentMethod.phoneBillStatus is required",
		)
	}
	return nil
}

func openService(dependencies Dependencies, profile string) (Service, error) {
	service, err := dependencies.OpenService(strings.TrimSpace(profile))
	if err != nil {
		return nil, fmt.Errorf("open Galaxy Store session: %w", err)
	}
	if service == nil {
		return nil, errors.New("open Galaxy Store session: IAP item service is nil")
	}
	return service, nil
}
