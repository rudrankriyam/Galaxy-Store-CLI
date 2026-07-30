package shared

import (
	"errors"
	"fmt"
	"strings"
)

var ErrConfirmationRequired = errors.New("explicit confirmation required")

// MutationMode captures the non-interactive safety flags shared by mutations.
type MutationMode struct {
	DryRun  bool
	Confirm bool
}

// ShouldExecute reports whether a mutation may perform a remote write.
// Dry-run always wins when both flags are supplied.
func (m MutationMode) ShouldExecute() bool {
	return !m.DryRun
}

// RequireConfirmation validates confirmation for an actual mutation.
func (m MutationMode) RequireConfirmation(action string) error {
	if m.DryRun || m.Confirm {
		return nil
	}
	action = strings.TrimSpace(action)
	if action == "" {
		action = "this operation"
	}
	return fmt.Errorf("%w to %s; inspect a dry-run first, then pass --confirm", ErrConfirmationRequired, action)
}

// Plan is the stable machine-readable result of a dry-run.
type Plan struct {
	Operations           []Operation `json:"operations"`
	Warnings             []string    `json:"warnings"`
	RequiresConfirmation bool        `json:"requiresConfirmation"`
	MutationsPerformed   bool        `json:"mutationsPerformed"`
}

// Operation describes one intended remote or local action.
type Operation struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Details  string `json:"details,omitempty"`
}
