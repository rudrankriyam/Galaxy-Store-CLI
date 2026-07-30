package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/catalog"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

func TestCommandsHaveCanonicalHelpAndOrder(t *testing.T) {
	t.Parallel()

	commands := Commands(io.Discard, nil)
	gotNames := make([]string, 0, len(commands))
	for _, command := range commands {
		gotNames = append(gotNames, command.Name)
		if !strings.HasPrefix(command.ShortUsage, "gsc "+command.Name) {
			t.Errorf("%s ShortUsage = %q, want canonical gsc usage", command.Name, command.ShortUsage)
		}
		if command.ShortHelp == "" || command.LongHelp == "" {
			t.Errorf("%s has incomplete help", command.Name)
		}
	}
	if want := []string{"capabilities", "search", "schema"}; !slices.Equal(gotNames, want) {
		t.Fatalf("command order = %v, want %v", gotNames, want)
	}
}

func TestCapabilitiesCommandJSONIsDeterministic(t *testing.T) {
	t.Parallel()

	first := runCommand(t, CapabilitiesCommand, []string{"--output", "json"})
	second := runCommand(t, CapabilitiesCommand, []string{"--output", "json"})
	if first != second {
		t.Fatal("capabilities output changed between identical invocations")
	}

	var report capabilitiesReport
	if err := json.Unmarshal([]byte(first), &report); err != nil {
		t.Fatalf("decode capabilities JSON: %v\n%s", err, first)
	}
	if report.OperationCount != 38 || len(report.Operations) != 38 {
		t.Fatalf("operation counts = %d/%d, want 38/38", report.OperationCount, len(report.Operations))
	}
	if report.LimitationCount != 6 || len(report.Limitations) != 6 {
		t.Fatalf("limitation counts = %d/%d, want 6/6", report.LimitationCount, len(report.Limitations))
	}
	for index := 1; index < len(report.Operations); index++ {
		if report.Operations[index-1].ID > report.Operations[index].ID {
			t.Fatalf("operations are not sorted by ID")
		}
	}
	if report.Limitations[0].ID == "" || !report.Limitations[0].PortalOnly {
		t.Fatalf("portal limitation = %#v, want explicit portal-only entry", report.Limitations[0])
	}
}

func TestCapabilitiesCommandSupportsExplicitTable(t *testing.T) {
	t.Parallel()

	got := runCommand(t, CapabilitiesCommand, []string{"--output", "table"})
	if !strings.HasPrefix(got, "ID") {
		t.Fatalf("table output = %q, want header", got)
	}
	if !strings.Contains(got, "portal.app-create") || !strings.Contains(got, "Seller Portal only") {
		t.Fatalf("table output does not show portal boundary:\n%s", got)
	}
}

func TestSchemaCommandReportsCuratedMetadataNotOpenAPI(t *testing.T) {
	t.Parallel()

	got := runCommand(t, SchemaCommand, []string{"--output", "json"})
	var report schemaReport
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("decode schema JSON: %v\n%s", err, got)
	}
	if report.Format != "curated-operation-catalog" || report.OpenAPI {
		t.Fatalf("schema identity = %q openapi=%v, want curated non-OpenAPI catalog", report.Format, report.OpenAPI)
	}
	if report.OperationCount != 38 || report.LimitationCount != 6 {
		t.Fatalf("schema counts = %d/%d, want 38/6", report.OperationCount, report.LimitationCount)
	}
	if !strings.Contains(report.Provenance, "does not publish an OpenAPI") {
		t.Fatalf("provenance = %q, want explicit OpenAPI limitation", report.Provenance)
	}
	if strings.Contains(got, `"requestSchema"`) || strings.Contains(got, `"responseSchema"`) {
		t.Fatalf("schema command invented endpoint schemas: %s", got)
	}
}

func TestSearchMatchesEveryDocumentedFieldCaseInsensitively(t *testing.T) {
	t.Parallel()

	embedded, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog.Load(): %v", err)
	}
	tests := []struct {
		name   string
		query  string
		wantID string
	}{
		{name: "ID", query: "IAP.SUBSCRIPTIONS.STATUS", wantID: "iap.subscriptions.status"},
		{name: "name", query: "VERIFY PURCHASE RECEIPT", wantID: "iap.receipts.verify"},
		{name: "family", query: "IAP-RECEIPTS", wantID: "iap.receipts.verify"},
		{name: "capability", query: "ANALYTICS", wantID: "gss.content.query"},
		{name: "command", query: "GSC STATS SELLER", wantID: "gss.seller.query"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := searchCatalog(embedded, test.query)
			if !containsResult(response.Results, test.wantID) {
				t.Fatalf("search(%q) IDs = %v, want %s", test.query, resultIDs(response.Results), test.wantID)
			}
		})
	}
}

func TestSearchCommandJSONAndStableOrdering(t *testing.T) {
	t.Parallel()

	got := runCommand(t, SearchCommand, []string{"--output", "json", "DESTRUCTIVE"})
	var response searchResponse
	if err := json.Unmarshal([]byte(got), &response); err != nil {
		t.Fatalf("decode search JSON: %v\n%s", err, got)
	}
	if response.Query != "DESTRUCTIVE" || response.Count == 0 {
		t.Fatalf("response = %#v, want retained query and results", response)
	}
	for index := 1; index < len(response.Results); index++ {
		if response.Results[index-1].ID > response.Results[index].ID {
			t.Fatalf("results are not sorted: %v", resultIDs(response.Results))
		}
	}
	for _, result := range response.Results {
		if strings.TrimSpace(result.Command) == "" {
			t.Fatalf("result %s is missing its canonical command", result.ID)
		}
	}
}

func TestSearchCommandRejectsBlankQuery(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"  "}} {
		var stdout bytes.Buffer
		command := SearchCommand(&stdout, nil)
		if err := command.Parse(args); err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		err := command.Run(context.Background())
		var usageError *shared.UsageError
		if !errors.As(err, &usageError) {
			t.Fatalf("Run(%v) error = %T %v, want *shared.UsageError", args, err, err)
		}
		if !strings.Contains(err.Error(), "query is required") {
			t.Fatalf("Run(%v) error = %q", args, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("blank query wrote output %q", stdout.String())
		}
	}
}

func TestCommandsRejectInvalidOutputFormat(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	command := SchemaCommand(&stdout, nil)
	if err := command.Parse([]string{"--output", "yaml"}); err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	err := command.Run(context.Background())
	var usageError *shared.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("Run() error = %T %v, want *shared.UsageError", err, err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("invalid format wrote output %q", stdout.String())
	}
}

func runCommand(
	t *testing.T,
	constructor func(io.Writer, output.TerminalDetector) *ffcli.Command,
	args []string,
) string {
	t.Helper()
	var stdout bytes.Buffer
	command := constructor(&stdout, func(io.Writer) bool { return false })
	if err := command.Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	if err := command.Run(context.Background()); err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return stdout.String()
}

func containsResult(results []searchResult, id string) bool {
	for _, result := range results {
		if result.ID == id {
			return true
		}
	}
	return false
}

func resultIDs(results []searchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}
