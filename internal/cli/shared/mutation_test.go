package shared

import (
	"errors"
	"testing"
)

func TestMutationModeDryRunWinsOverConfirm(t *testing.T) {
	mode := MutationMode{DryRun: true, Confirm: true}

	if mode.ShouldExecute() {
		t.Fatal("ShouldExecute() = true, want false for dry-run")
	}
	if err := mode.RequireConfirmation("submit app"); err != nil {
		t.Fatalf("RequireConfirmation() error = %v, want nil for dry-run", err)
	}
}

func TestMutationModeRequiresExplicitConfirmation(t *testing.T) {
	mode := MutationMode{}

	err := mode.RequireConfirmation("delete binary")

	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("error = %v, want ErrConfirmationRequired", err)
	}
}

func TestMutationModeAllowsConfirmedMutation(t *testing.T) {
	mode := MutationMode{Confirm: true}

	if !mode.ShouldExecute() {
		t.Fatal("ShouldExecute() = false, want true")
	}
	if err := mode.RequireConfirmation("submit app"); err != nil {
		t.Fatalf("RequireConfirmation() error = %v, want nil", err)
	}
}
