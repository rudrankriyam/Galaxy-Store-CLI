package iapcmd

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/orders"
)

type orderOptions struct {
	Profile           string
	SellerSequence    string
	PackageName       string
	RequestDate       string
	ContinuationToken string
	Output            string
}

func newOrdersCommand(dependencies Dependencies, stderr io.Writer) *ffcli.Command {
	flags := flag.NewFlagSet("iap orders list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var profile string
	var sellerSequence string
	var packageName string
	var requestDate string
	var continuationToken string
	var outputValue string
	flags.StringVar(&profile, "profile", "", "Credential profile (defaults to environment or configured profile)")
	flags.StringVar(&sellerSequence, "seller-seq", "", "Required 12-digit Seller Portal deeplink number")
	flags.StringVar(&packageName, "package-name", "", "Optional application package name")
	flags.StringVar(&requestDate, "request-date", "", "Optional payment/refund date in YYYYMMDD; defaults to yesterday")
	flags.StringVar(&continuationToken, "continuation-token", "", "Optional token returned by the previous page")
	flags.StringVar(&outputValue, "output", "auto", "Output format: auto, json, table, or markdown")

	list := &ffcli.Command{
		Name:       "list",
		ShortUsage: "gsc iap orders list --seller-seq ID [--package-name PACKAGE] [--request-date YYYYMMDD] [--continuation-token TOKEN] [--profile NAME] [--output FORMAT]",
		ShortHelp:  "View payments and refunds for a date.",
		LongHelp: `View up to 100 Samsung IAP payment and refund records.

Samsung defines this read-only operation as POST. It never changes order state,
so --confirm and --dry-run are intentionally not part of this command.

Examples:
  gsc iap orders list --seller-seq 000123456789
  gsc iap orders list --seller-seq 000123456789 --request-date 20260730 --output json`,
		FlagSet: flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("iap orders list does not accept positional arguments")
			}
			return runOrdersList(ctx, dependencies, orderOptions{
				Profile:           profile,
				SellerSequence:    sellerSequence,
				PackageName:       packageName,
				RequestDate:       requestDate,
				ContinuationToken: continuationToken,
				Output:            outputValue,
			})
		},
	}
	list.UsageFunc = commandUsage

	command := &ffcli.Command{
		Name:        "orders",
		ShortUsage:  "gsc iap orders <command> [flags]",
		ShortHelp:   "Inspect Samsung IAP payments and refunds.",
		Subcommands: []*ffcli.Command{list},
		Exec: func(context.Context, []string) error {
			return shared.UsageErrorf("iap orders requires the list command")
		},
	}
	command.UsageFunc = commandUsage
	return command
}

func runOrdersList(
	ctx context.Context,
	dependencies Dependencies,
	options orderOptions,
) error {
	if err := validateSellerSequence(options.SellerSequence); err != nil {
		return err
	}
	if options.PackageName != "" {
		if err := validatePackageName(options.PackageName); err != nil {
			return err
		}
	}
	if err := validateRequestDate(options.RequestDate); err != nil {
		return err
	}
	if err := validateContinuationToken(options.ContinuationToken); err != nil {
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
	if services.Orders == nil {
		return errors.New("open Galaxy Store session: order service is nil")
	}
	result, err := services.Orders.List(ctx, orders.ListOptions{
		SellerSequence:    options.SellerSequence,
		PackageName:       options.PackageName,
		RequestDate:       options.RequestDate,
		ContinuationToken: options.ContinuationToken,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("samsung returned an invalid orders response")
	}
	return dependencies.Printer.Print(format, ordersOutput{result: result})
}
