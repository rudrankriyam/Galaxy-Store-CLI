package iapcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/subscriptions"
)

type subscriptionAction string

const (
	subscriptionCancel subscriptionAction = "cancel"
	subscriptionRefund subscriptionAction = "refund"
	subscriptionRevoke subscriptionAction = "revoke"
)

type subscriptionOptions struct {
	Profile     string
	PackageName string
	PurchaseID  string
	Caller      string
	Output      string
	Mode        shared.MutationMode
}

func newSubscriptionsCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	command := &ffcli.Command{
		Name:       "subscriptions",
		ShortUsage: "gsc iap subscriptions <command> [flags]",
		ShortHelp:  "Inspect, cancel, refund, or revoke IAP subscriptions.",
		Subcommands: []*ffcli.Command{
			newSubscriptionStatusCommand(dependencies, stderr),
			newSubscriptionActionCommand(dependencies, stderr, subscriptionCancel),
			newSubscriptionActionCommand(dependencies, stderr, subscriptionRefund),
			newSubscriptionActionCommand(dependencies, stderr, subscriptionRevoke),
		},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf(
				"iap subscriptions requires a command: status, cancel, refund, or revoke",
			)
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newSubscriptionStatusCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap subscriptions status", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var packageName string
	var purchaseID string
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&packageName, "package-name", "", "Required application package name")
	flags.StringVar(&purchaseID, "purchase-id", "", "Required Samsung subscription purchase ID")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	command := &ffcli.Command{
		Name:       "status",
		ShortUsage: "gsc iap subscriptions status --package-name PACKAGE --purchase-id ID [--profile NAME] [--output FORMAT]",
		ShortHelp:  "View current subscription and purchase status.",
		FlagSet:    flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap subscriptions status does not accept positional arguments")
			}
			return runSubscriptionStatus(ctx, dependencies, subscriptionOptions{
				Profile:     profile,
				PackageName: packageName,
				PurchaseID:  purchaseID,
				Output:      outputValue,
			})
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func newSubscriptionActionCommand(
	dependencies Dependencies,
	stderr io.Writer,
	action subscriptionAction,
) *ffcli.Command {
	flags := flag.NewFlagSet("iap subscriptions "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var packageName string
	var purchaseID string
	var caller string
	var outputValue string
	var dryRun bool
	var confirm bool
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&packageName, "package-name", "", "Required application package name")
	flags.StringVar(&purchaseID, "purchase-id", "", "Required Samsung subscription purchase ID")
	if action == subscriptionCancel {
		flags.StringVar(&caller, "caller", "", "Cancellation caller: admin (default) or user")
	}
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")
	flags.BoolVar(&dryRun, "dry-run", false, "Validate and show the mutation plan without changing state")
	flags.BoolVar(&confirm, "confirm", false, "Confirm the subscription mutation")

	command := &ffcli.Command{
		Name: string(action),
		ShortUsage: fmt.Sprintf(
			"gsc iap subscriptions %s --package-name PACKAGE --purchase-id ID [--dry-run | --confirm] [--profile NAME] [--output FORMAT]",
			action,
		),
		ShortHelp: subscriptionActionHelp(action),
		FlagSet:   flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap subscriptions %s does not accept positional arguments", action)
			}
			return runSubscriptionAction(ctx, dependencies, action, subscriptionOptions{
				Profile:     profile,
				PackageName: packageName,
				PurchaseID:  purchaseID,
				Caller:      caller,
				Output:      outputValue,
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

func runSubscriptionStatus(
	ctx context.Context,
	dependencies Dependencies,
	options subscriptionOptions,
) error {
	if err := validateSubscriptionOptions(options); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}
	services, err := openServices(dependencies, options.Profile)
	if err != nil {
		return err
	}
	if services.Subscriptions == nil {
		return errors.New("open Galaxy Store session: subscription service is nil")
	}
	status, err := services.Subscriptions.GetStatus(ctx, subscriptionReference(options))
	if err != nil {
		return err
	}
	if status == nil {
		return errors.New("samsung returned an invalid subscription status response")
	}
	return dependencies.Printer.Print(format, subscriptionStatusOutput{status: status})
}

func runSubscriptionAction(
	ctx context.Context,
	dependencies Dependencies,
	action subscriptionAction,
	options subscriptionOptions,
) error {
	if err := validateSubscriptionOptions(options); err != nil {
		return err
	}
	caller, err := normalizeCaller(action, options.Caller)
	if err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := validateDependencies(dependencies); err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation(subscriptionConfirmationAction(action)); err != nil {
		return err
	}

	if options.Mode.DryRun {
		return dependencies.Printer.Print(format, mutationPlanOutput{
			Action:      string(action),
			PackageName: options.PackageName,
			PurchaseID:  options.PurchaseID,
			Caller:      string(caller),
			DryRun:      true,
			Plan: &shared.Plan{
				Operations: []shared.Operation{{
					Action:   string(action),
					Resource: "Samsung IAP subscription",
					Details: fmt.Sprintf(
						"%s subscription %s for %s",
						action,
						options.PurchaseID,
						options.PackageName,
					),
				}},
				Warnings:             subscriptionWarnings(action),
				RequiresConfirmation: true,
				MutationsPerformed:   false,
			},
		})
	}

	services, err := openServices(dependencies, options.Profile)
	if err != nil {
		return err
	}
	if services.Subscriptions == nil {
		return errors.New("open Galaxy Store session: subscription service is nil")
	}
	reference := subscriptionReference(options)
	var result *subscriptions.ActionResult
	switch action {
	case subscriptionCancel:
		result, err = services.Subscriptions.Cancel(ctx, reference, caller)
	case subscriptionRefund:
		result, err = services.Subscriptions.Refund(ctx, reference)
	case subscriptionRevoke:
		result, err = services.Subscriptions.Revoke(ctx, reference)
	default:
		return errors.New("unsupported IAP subscription action")
	}
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid subscription action response")
	}
	return dependencies.Printer.Print(format, subscriptionActionOutput{result: result})
}

func validateSubscriptionOptions(options subscriptionOptions) error {
	if err := validatePackageName(options.PackageName); err != nil {
		return err
	}
	return validatePurchaseID("--purchase-id", options.PurchaseID)
}

func normalizeCaller(
	action subscriptionAction,
	value string,
) (subscriptions.Caller, error) {
	if action != subscriptionCancel {
		if value != "" {
			return "", shared.UsageErrorf("--caller is supported only for subscriptions cancel")
		}
		return subscriptions.CallerDefault, nil
	}
	switch value {
	case "":
		return subscriptions.CallerDefault, nil
	case string(subscriptions.CallerAdmin):
		return subscriptions.CallerAdmin, nil
	case string(subscriptions.CallerUser):
		return subscriptions.CallerUser, nil
	default:
		return "", shared.UsageErrorf("--caller must be admin or user")
	}
}

func subscriptionReference(options subscriptionOptions) subscriptions.Reference {
	return subscriptions.Reference{
		PackageName: options.PackageName,
		PurchaseID:  options.PurchaseID,
	}
}

func subscriptionActionHelp(action subscriptionAction) string {
	switch action {
	case subscriptionCancel:
		return "Stop renewal after the current subscription period."
	case subscriptionRefund:
		return "Refund the latest payment while keeping the subscription active."
	case subscriptionRevoke:
		return "Immediately cancel access and refund the latest payment."
	default:
		return "Change an IAP subscription."
	}
}

func subscriptionConfirmationAction(action subscriptionAction) string {
	switch action {
	case subscriptionCancel:
		return "cancel the Samsung IAP subscription"
	case subscriptionRefund:
		return "refund the Samsung IAP subscription's latest payment"
	case subscriptionRevoke:
		return "immediately revoke and refund the Samsung IAP subscription"
	default:
		return "change the Samsung IAP subscription"
	}
}

func subscriptionWarnings(action subscriptionAction) []string {
	switch action {
	case subscriptionCancel:
		return []string{
			"Renewal stops, but access continues until the current subscription period ends.",
		}
	case subscriptionRefund:
		return []string{
			"The latest payment is reimbursed, but the subscription remains active.",
		}
	case subscriptionRevoke:
		return []string{
			"Access ends immediately and the latest payment is reimbursed.",
		}
	default:
		return nil
	}
}
