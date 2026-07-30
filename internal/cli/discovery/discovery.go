// Package discovery implements local, deterministic discovery commands backed
// by gsc's embedded Galaxy Store operation catalog.
package discovery

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/catalog"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

// Commands returns the top-level discovery commands in canonical root order.
// A nil terminal detector makes auto output deterministic JSON.
func Commands(stdout io.Writer, isTerminal output.TerminalDetector) []*ffcli.Command {
	return []*ffcli.Command{
		CapabilitiesCommand(stdout, isTerminal),
		SearchCommand(stdout, isTerminal),
		SchemaCommand(stdout, isTerminal),
	}
}

// CapabilitiesCommand returns the local API-capability inventory command.
func CapabilitiesCommand(stdout io.Writer, isTerminal output.TerminalDetector) *ffcli.Command {
	flags := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("output", string(output.FormatAuto), "Output format: auto, json, table, or markdown")

	return &ffcli.Command{
		Name:       "capabilities",
		ShortUsage: "gsc capabilities [--output <format>]",
		ShortHelp:  "Show supported Galaxy Store API operations and Seller Portal boundaries.",
		LongHelp: `Show supported Galaxy Store API operations and Seller Portal boundaries.

The report is generated locally from gsc's embedded, audited operation catalog.
It does not contact Samsung or imply support for Seller Portal-only workflows.

Examples:
  gsc capabilities
  gsc capabilities --output json
  gsc capabilities --output table`,
		FlagSet: flags,
		Exec: func(_ context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("capabilities does not accept positional arguments")
			}
			selectedFormat, err := output.ParseFormat(*format)
			if err != nil {
				return shared.UsageErrorf("%v", err)
			}
			embedded, err := catalog.Load()
			if err != nil {
				return fmt.Errorf("capabilities: %w", err)
			}
			report := buildCapabilitiesReport(embedded)
			if err := output.NewPrinter(stdout, isTerminal).Print(selectedFormat, report); err != nil {
				return fmt.Errorf("capabilities: %w", err)
			}
			return nil
		},
	}
}

// SchemaCommand returns metadata about the curated embedded catalog. Samsung
// does not publish an OpenAPI document, so this command deliberately does not
// synthesize request or response schemas.
func SchemaCommand(stdout io.Writer, isTerminal output.TerminalDetector) *ffcli.Command {
	flags := flag.NewFlagSet("schema", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("output", string(output.FormatAuto), "Output format: auto, json, table, or markdown")

	return &ffcli.Command{
		Name:       "schema",
		ShortUsage: "gsc schema [--output <format>]",
		ShortHelp:  "Describe the embedded curated API catalog and its provenance.",
		LongHelp: `Describe the embedded curated API catalog and its provenance.

Samsung does not publish an OpenAPI document for the Galaxy Store Developer
API. This command reports catalog metadata and field names; it does not invent
request schemas, response schemas, or undocumented endpoints.

Examples:
  gsc schema
  gsc schema --output json
  gsc schema --output markdown`,
		FlagSet: flags,
		Exec: func(_ context.Context, args []string) error {
			if len(args) != 0 {
				return shared.UsageErrorf("schema does not accept positional arguments")
			}
			selectedFormat, err := output.ParseFormat(*format)
			if err != nil {
				return shared.UsageErrorf("%v", err)
			}
			embedded, err := catalog.Load()
			if err != nil {
				return fmt.Errorf("schema: %w", err)
			}
			report := buildSchemaReport(embedded)
			if err := output.NewPrinter(stdout, isTerminal).Print(selectedFormat, report); err != nil {
				return fmt.Errorf("schema: %w", err)
			}
			return nil
		},
	}
}

// SearchCommand returns the local operation-catalog search command.
func SearchCommand(stdout io.Writer, isTerminal output.TerminalDetector) *ffcli.Command {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("output", string(output.FormatAuto), "Output format: auto, json, table, or markdown")

	return &ffcli.Command{
		Name:       "search",
		ShortUsage: "gsc search [--output <format>] <query>",
		ShortHelp:  "Search supported Galaxy Store operations and their gsc commands.",
		LongHelp: `Search supported Galaxy Store operations and their gsc commands.

Search is local, case-insensitive, and deterministic. It matches operation IDs,
names, API families, capabilities, and proposed command strings. It does not
search apps or other live Galaxy Store data.

Examples:
  gsc search rollout
  gsc search --output json "iap subscriptions"
  gsc search --output table destructive`,
		FlagSet: flags,
		Exec: func(_ context.Context, args []string) error {
			query := strings.TrimSpace(strings.Join(args, " "))
			if query == "" {
				return shared.UsageErrorf("search query is required")
			}
			selectedFormat, err := output.ParseFormat(*format)
			if err != nil {
				return shared.UsageErrorf("%v", err)
			}
			embedded, err := catalog.Load()
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			response := searchCatalog(embedded, query)
			if err := output.NewPrinter(stdout, isTerminal).Print(selectedFormat, response); err != nil {
				return fmt.Errorf("search: %w", err)
			}
			return nil
		},
	}
}

type capabilitiesReport struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	LastVerified    string                 `json:"lastVerified"`
	OperationCount  int                    `json:"operationCount"`
	LimitationCount int                    `json:"limitationCount"`
	Operations      []capabilityOperation  `json:"operations"`
	Limitations     []capabilityLimitation `json:"limitations"`
}

type capabilityOperation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Family     string `json:"family"`
	Capability string `json:"capability"`
	Mutation   bool   `json:"mutation"`
	Command    string `json:"command"`
}

type capabilityLimitation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PortalOnly bool   `json:"portalOnly"`
	Reason     string `json:"reason"`
}

func buildCapabilitiesReport(embedded *catalog.Catalog) capabilitiesReport {
	operations := make([]capabilityOperation, 0, len(embedded.Operations))
	for _, operation := range embedded.Operations {
		operations = append(operations, capabilityOperation{
			ID:         operation.ID,
			Name:       operation.Name,
			Family:     operation.Family,
			Capability: operation.Capability,
			Mutation:   operation.Mutation,
			Command:    operation.ProposedCommand,
		})
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })

	limitations := make([]capabilityLimitation, 0, len(embedded.Limitations))
	for _, limitation := range embedded.Limitations {
		limitations = append(limitations, capabilityLimitation{
			ID:         limitation.ID,
			Name:       limitation.Name,
			PortalOnly: limitation.PortalOnly,
			Reason:     limitation.Reason,
		})
	}
	sort.Slice(limitations, func(i, j int) bool { return limitations[i].ID < limitations[j].ID })

	return capabilitiesReport{
		SchemaVersion:   embedded.SchemaVersion,
		LastVerified:    embedded.LastVerified,
		OperationCount:  len(operations),
		LimitationCount: len(limitations),
		Operations:      operations,
		Limitations:     limitations,
	}
}

func (r capabilitiesReport) OutputHeaders() []string {
	return []string{"ID", "FAMILY", "CAPABILITY", "MUTATION", "COMMAND"}
}

func (r capabilitiesReport) OutputRows() [][]string {
	rows := make([][]string, 0, len(r.Operations)+len(r.Limitations))
	for _, operation := range r.Operations {
		rows = append(rows, []string{
			operation.ID,
			operation.Family,
			operation.Capability,
			strconv.FormatBool(operation.Mutation),
			operation.Command,
		})
	}
	for _, limitation := range r.Limitations {
		rows = append(rows, []string{
			limitation.ID,
			"portal",
			"portal-only",
			"false",
			"Seller Portal only",
		})
	}
	return rows
}

type schemaReport struct {
	Format           string   `json:"format"`
	OpenAPI          bool     `json:"openapi"`
	SchemaVersion    int      `json:"schemaVersion"`
	LastVerified     string   `json:"lastVerified"`
	DefaultHost      string   `json:"defaultHost"`
	OperationCount   int      `json:"operationCount"`
	LimitationCount  int      `json:"limitationCount"`
	Families         []string `json:"families"`
	OperationFields  []string `json:"operationFields"`
	LimitationFields []string `json:"limitationFields"`
	Provenance       string   `json:"provenance"`
}

func buildSchemaReport(embedded *catalog.Catalog) schemaReport {
	return schemaReport{
		Format:          "curated-operation-catalog",
		OpenAPI:         false,
		SchemaVersion:   embedded.SchemaVersion,
		LastVerified:    embedded.LastVerified,
		DefaultHost:     embedded.DefaultHost,
		OperationCount:  len(embedded.Operations),
		LimitationCount: len(embedded.Limitations),
		Families:        embedded.Families(),
		OperationFields: []string{
			"id", "name", "method", "host", "path", "family", "scope", "auth",
			"retry", "mutation", "capability", "proposedCommand", "sourceUrl",
			"lastVerified", "notes",
		},
		LimitationFields: []string{
			"id", "name", "reason", "portalOnly", "sourceUrl", "lastVerified",
		},
		Provenance: "Curated from official Samsung documentation; Samsung does not publish an OpenAPI document for this API.",
	}
}

func (r schemaReport) OutputHeaders() []string {
	return []string{"FORMAT", "OPENAPI", "VERSION", "VERIFIED", "OPERATIONS", "LIMITATIONS", "FAMILIES"}
}

func (r schemaReport) OutputRows() [][]string {
	return [][]string{{
		r.Format,
		strconv.FormatBool(r.OpenAPI),
		strconv.Itoa(r.SchemaVersion),
		r.LastVerified,
		strconv.Itoa(r.OperationCount),
		strconv.Itoa(r.LimitationCount),
		strings.Join(r.Families, ","),
	}}
}

type searchResponse struct {
	Query   string         `json:"query"`
	Count   int            `json:"count"`
	Results []searchResult `json:"results"`
}

type searchResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Family     string `json:"family"`
	Capability string `json:"capability"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Command    string `json:"command"`
}

func searchCatalog(embedded *catalog.Catalog, query string) searchResponse {
	normalized := strings.ToLower(strings.TrimSpace(query))
	results := make([]searchResult, 0)
	for _, operation := range embedded.Operations {
		searchable := []string{
			operation.ID,
			operation.Name,
			operation.Family,
			operation.Capability,
			operation.ProposedCommand,
		}
		matched := false
		for _, candidate := range searchable {
			if strings.Contains(strings.ToLower(candidate), normalized) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		results = append(results, searchResult{
			ID:         operation.ID,
			Name:       operation.Name,
			Family:     operation.Family,
			Capability: operation.Capability,
			Method:     operation.Method,
			Path:       operation.Path,
			Command:    operation.ProposedCommand,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return searchResponse{
		Query:   strings.TrimSpace(query),
		Count:   len(results),
		Results: results,
	}
}

func (r searchResponse) OutputHeaders() []string {
	return []string{"ID", "FAMILY", "CAPABILITY", "METHOD", "PATH", "COMMAND"}
}

func (r searchResponse) OutputRows() [][]string {
	rows := make([][]string, 0, len(r.Results))
	for _, result := range r.Results {
		rows = append(rows, []string{
			result.ID,
			result.Family,
			result.Capability,
			result.Method,
			result.Path,
			result.Command,
		})
	}
	return rows
}
