package cmd

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

func TestExitCodeForError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", want: ExitSuccess},
		{name: "generic", err: errors.New("failed"), want: ExitError},
		{name: "usage sentinel", err: errUsage, want: ExitUsage},
		{name: "typed usage", err: shared.UsageErrorf("bad flag"), want: ExitUsage},
		{name: "confirmation", err: shared.ErrConfirmationRequired, want: ExitValidation},
		{name: "missing credentials", err: credentials.ErrNotFound, want: ExitAuth},
		{name: "incomplete credentials", err: credentials.ErrIncomplete, want: ExitAuth},
		{name: "interrupted", err: context.Canceled, want: ExitInterrupted},
		{
			name: "Samsung unauthorized",
			err:  &samsung.APIError{StatusCode: http.StatusUnauthorized},
			want: ExitAuth,
		},
		{
			name: "auth forbidden",
			err:  &auth.APIError{StatusCode: http.StatusForbidden},
			want: ExitAuth,
		},
		{
			name: "not found",
			err:  &samsung.APIError{StatusCode: http.StatusNotFound},
			want: ExitNotFound,
		},
		{
			name: "conflict",
			err:  &samsung.APIError{StatusCode: http.StatusConflict},
			want: ExitConflict,
		},
		{
			name: "validation",
			err:  &samsung.APIError{StatusCode: http.StatusUnprocessableEntity},
			want: ExitValidation,
		},
		{
			name: "other HTTP",
			err:  &samsung.APIError{StatusCode: http.StatusServiceUnavailable},
			want: ExitHTTPFailure,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := exitCodeForError(test.err); got != test.want {
				t.Fatalf("exitCodeForError() = %d, want %d", got, test.want)
			}
		})
	}
}
