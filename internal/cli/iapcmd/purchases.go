package iapcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/purchases"
)

type purchaseAction string

const (
	purchaseConsume     purchaseAction = "consume"
	purchaseAcknowledge purchaseAction = "acknowledge"
)

type purchaseOptions struct {
	Profile         string
	PackageName     string
	PurchaseID      string
	PurchasedIDList []string
	Output          string
	Mode            shared.MutationMode
}

func newPurchasesCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	command := &ffcli.Command{
		Name:       "purchases",
		ShortUsage: "gsc iap purchases <command> [flags]",
		ShortHelp:  "Consume purchases or acknowledge subscription entitlement.",
		Subcommands: []*ffcli.Command{
			newPurchaseActionCommand(dependencies, stderr, purchaseConsume),
			newPurchaseActionCommand(dependencies, stderr, purchaseAcknowledge),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("iap purchases requires a command: consume or acknowledge")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newPurchaseActionCommand(
	dependencies Dependencies,
	stderr io.Writer,
	action purchaseAction,
) *ffcli.Command {
	flags := flag.NewFlagSet("iap purchases "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var packageName string
	var purchaseID string
	var purchasedIDs stringList
	var outputValue string
	var dryRun bool
	var confirm bool
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&packageName, "package-name", "", "Required application package name")
	flags.StringVar(&purchaseID, "purchase-id", "", "Required Samsung purchase transaction ID")
	flags.Var(&purchasedIDs, "purchased-id", "Additional purchase ID for batch processing; repeat as needed")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and show the mutation plan without changing state")
	flags.BoolVar(&confirm, "confirm", false, "Confirm purchase processing")

	verb := "Consume"
	help := "Report one or more consumable purchases as consumed."
	if action == purchaseAcknowledge {
		verb = "Acknowledge"
		help = "Confirm that subscription entitlement was granted."
	}
	command := &ffcli.Command{
		Name: string(action),
		ShortUsage: fmt.Sprintf(
			"gsc iap purchases %s --package-name PACKAGE --purchase-id ID [--purchased-id ID ...] [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
			action,
		),
		ShortHelp: help,
		LongHelp: fmt.Sprintf(`%s a completed Samsung IAP purchase.

Use --purchased-id repeatedly to include Samsung's optional purchasedIdList
batch body. A dry-run performs no credential resolution or network request.

Examples:
  gsc iap purchases %s --package-name com.example.app --purchase-id PURCHASE --dry-run
  gsc iap purchases %s --package-name com.example.app --purchase-id PURCHASE --confirm`,
			verb,
			action,
			action,
		),
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap purchases %s does not accept positional arguments", action)
			}
			return runPurchaseAction(ctx, dependencies, action, purchaseOptions{
				Profile:         profile,
				PackageName:     packageName,
				PurchaseID:      purchaseID,
				PurchasedIDList: append([]string(nil), purchasedIDs...),
				Output:          outputValue,
				Mode: shared.MutationMode{
					DryRun:  dryRun,
					Confirm: confirm,
				},
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func runPurchaseAction(
	ctx context.Context,
	dependencies Dependencies,
	action purchaseAction,
	options purchaseOptions,
) error {
	if err := validatePackageName(options.PackageName); err != nil {
		return err
	}
	if err := validatePurchaseID("--purchase-id", options.PurchaseID); err != nil {
		return err
	}
	for _, purchaseID := range options.PurchasedIDList {
		if err := validatePurchaseID("--purchased-id", purchaseID); err != nil {
			return err
		}
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}
	confirmationAction := "consume the Samsung IAP purchase"
	if action == purchaseAcknowledge {
		confirmationAction = "acknowledge Samsung IAP subscription entitlement"
	}
	if err := options.Mode.RequireConfirmation(confirmationAction); err != nil {
		return err
	}

	if options.Mode.DryRun {
		warnings := []string{"Processing a purchase changes its server-side fulfillment state."}
		if action == purchaseConsume {
			warnings = []string{"Consumed items become eligible to be purchased again."}
		}
		return dependencies.Printer.Print(format, mutationPlanOutput{
			Action:          string(action),
			PackageName:     options.PackageName,
			PurchaseID:      options.PurchaseID,
			PurchasedIDList: append([]string(nil), options.PurchasedIDList...),
			DryRun:          true,
			Plan: &shared.Plan{
				Operations: []shared.Operation{{
					Action:   string(action),
					Resource: "Samsung IAP purchase",
					Details:  purchaseTargetDetails(options),
				}},
				Warnings:             warnings,
				RequiresConfirmation: true,
				MutationsPerformed:   false,
			},
		})
	}

	services, err := openServices(dependencies, options.Profile)
	if err != nil {
		return err
	}
	if services.Purchases == nil {
		return errors.New("open Galaxy Store session: purchase service is nil")
	}
	request := purchases.Request{
		PackageName:     options.PackageName,
		PurchaseID:      options.PurchaseID,
		PurchasedIDList: append([]string(nil), options.PurchasedIDList...),
	}
	var result *purchases.Result
	switch action {
	case purchaseConsume:
		result, err = services.Purchases.Consume(ctx, request)
	case purchaseAcknowledge:
		result, err = services.Purchases.Acknowledge(ctx, request)
	default:
		return errors.New("unsupported IAP purchase action")
	}
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid purchase processing response")
	}
	return dependencies.Printer.Print(format, purchaseOutput{result: result})
}

func purchaseTargetDetails(options purchaseOptions) string {
	count := 1 + len(options.PurchasedIDList)
	return fmt.Sprintf(
		"process %d purchase(s) for %s beginning with %s",
		count,
		options.PackageName,
		options.PurchaseID,
	)
}
