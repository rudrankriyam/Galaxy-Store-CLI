// Package catalog exposes the curated Galaxy Store API surface used by gsc.
//
// Samsung does not publish an OpenAPI document for the Galaxy Store Developer
// API. Keeping the audited operation inventory embedded in the binary gives
// command discovery, capability reporting, and tests a single source of truth.
package catalog

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

//go:embed operations.json
var files embed.FS

// Catalog is the embedded Galaxy Store API inventory.
type Catalog struct {
	SchemaVersion int          `json:"schemaVersion"`
	LastVerified  string       `json:"lastVerified"`
	DefaultHost   string       `json:"defaultHost"`
	Operations    []Operation  `json:"operations"`
	Limitations   []Limitation `json:"limitations"`
}

// Operation describes one supported API operation. Operations that share an
// HTTP endpoint remain separate when Samsung selects behavior through a body
// action, such as subscription cancel, refund, and revoke.
type Operation struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Method          string `json:"method"`
	Host            string `json:"host"`
	Path            string `json:"path"`
	Family          string `json:"family"`
	Scope           string `json:"scope"`
	Auth            string `json:"auth"`
	Retry           string `json:"retry"`
	Mutation        bool   `json:"mutation"`
	Capability      string `json:"capability"`
	ProposedCommand string `json:"proposedCommand"`
	SourceURL       string `json:"sourceUrl"`
	LastVerified    string `json:"lastVerified"`
	Notes           string `json:"notes,omitempty"`
}

// Limitation records functionality that Samsung only exposes in Seller Portal
// or explicitly excludes from the public API.
type Limitation struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Reason       string `json:"reason"`
	PortalOnly   bool   `json:"portalOnly"`
	SourceURL    string `json:"sourceUrl"`
	LastVerified string `json:"lastVerified"`
}

// Load parses and validates the embedded catalog.
func Load() (*Catalog, error) {
	data, err := files.ReadFile("operations.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded operation catalog: %w", err)
	}

	var result Catalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedded operation catalog: %w", err)
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("validate embedded operation catalog: %w", err)
	}
	return &result, nil
}

// OperationByID resolves an operation by its stable catalog identifier.
func (c *Catalog) OperationByID(id string) (Operation, bool) {
	for _, operation := range c.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return Operation{}, false
}

// OperationsByFamily returns operations in source order for a family.
func (c *Catalog) OperationsByFamily(family string) []Operation {
	var matches []Operation
	for _, operation := range c.Operations {
		if operation.Family == family {
			matches = append(matches, operation)
		}
	}
	return matches
}

// Families returns a sorted list of API families represented in the catalog.
func (c *Catalog) Families() []string {
	families := make([]string, 0)
	for _, operation := range c.Operations {
		if !slices.Contains(families, operation.Family) {
			families = append(families, operation.Family)
		}
	}
	slices.Sort(families)
	return families
}

// Validate checks catalog invariants relied on by command discovery.
func (c *Catalog) Validate() error {
	if c.SchemaVersion < 1 {
		return errors.New("schemaVersion must be at least 1")
	}
	if err := validDate("lastVerified", c.LastVerified); err != nil {
		return err
	}
	if err := validHTTPSURL("defaultHost", c.DefaultHost); err != nil {
		return err
	}
	if len(c.Operations) == 0 {
		return errors.New("operations must not be empty")
	}

	allowedMethods := map[string]bool{
		http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
		http.MethodPatch: true, http.MethodDelete: true,
	}
	allowedRetry := map[string]bool{"safe": true, "conditional": true, "never": true}
	seenOperations := make(map[string]bool, len(c.Operations))
	for index, operation := range c.Operations {
		prefix := fmt.Sprintf("operations[%d]", index)
		if operation.ID == "" || operation.Name == "" || operation.Family == "" ||
			operation.Scope == "" || operation.Auth == "" || operation.Capability == "" {
			return fmt.Errorf("%s has an empty required field", prefix)
		}
		if seenOperations[operation.ID] {
			return fmt.Errorf("duplicate operation id %q", operation.ID)
		}
		seenOperations[operation.ID] = true
		if !allowedMethods[operation.Method] {
			return fmt.Errorf("%s has invalid method %q", prefix, operation.Method)
		}
		if !strings.HasPrefix(operation.Path, "/") {
			return fmt.Errorf("%s path must begin with /", prefix)
		}
		if err := validHTTPSURL(prefix+".host", operation.Host); err != nil {
			return err
		}
		if !allowedRetry[operation.Retry] {
			return fmt.Errorf("%s has invalid retry policy %q", prefix, operation.Retry)
		}
		if operation.ProposedCommand == "" || !strings.HasPrefix(operation.ProposedCommand, "gsc ") {
			return fmt.Errorf("%s must map to a gsc command", prefix)
		}
		if hasGetLeaf(operation.ProposedCommand) {
			return fmt.Errorf("%s uses non-canonical get leaf in %q", prefix, operation.ProposedCommand)
		}
		if err := validHTTPSURL(prefix+".sourceUrl", operation.SourceURL); err != nil {
			return err
		}
		if err := validDate(prefix+".lastVerified", operation.LastVerified); err != nil {
			return err
		}
	}

	if len(c.Limitations) == 0 {
		return errors.New("limitations must not be empty")
	}
	seenLimitations := make(map[string]bool, len(c.Limitations))
	for index, limitation := range c.Limitations {
		prefix := fmt.Sprintf("limitations[%d]", index)
		if limitation.ID == "" || limitation.Name == "" || limitation.Reason == "" {
			return fmt.Errorf("%s has an empty required field", prefix)
		}
		if seenLimitations[limitation.ID] {
			return fmt.Errorf("duplicate limitation id %q", limitation.ID)
		}
		seenLimitations[limitation.ID] = true
		if !limitation.PortalOnly {
			return fmt.Errorf("%s must be marked portalOnly", prefix)
		}
		if err := validHTTPSURL(prefix+".sourceUrl", limitation.SourceURL); err != nil {
			return err
		}
		if err := validDate(prefix+".lastVerified", limitation.LastVerified); err != nil {
			return err
		}
	}
	return nil
}

func hasGetLeaf(command string) bool {
	for _, token := range strings.Fields(command) {
		if strings.HasPrefix(token, "-") {
			break
		}
		if token == "get" {
			return true
		}
	}
	return false
}

func validHTTPSURL(field, raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute HTTPS URL", field)
	}
	return nil
}

func validDate(field, value string) error {
	if _, err := time.Parse(time.DateOnly, value); err != nil {
		return fmt.Errorf("%s must use YYYY-MM-DD: %w", field, err)
	}
	return nil
}
