package cmd

import (
	"context"
	"errors"
	"net/http"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

const (
	ExitSuccess     = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitValidation  = 6
	ExitHTTPFailure = 7
	ExitInterrupted = 130
)

func exitCodeForError(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if errors.Is(err, context.Canceled) {
		return ExitInterrupted
	}
	if errors.Is(err, errUsage) {
		return ExitUsage
	}
	var usageError *shared.UsageError
	if errors.As(err, &usageError) {
		return ExitUsage
	}
	if errors.Is(err, shared.ErrConfirmationRequired) {
		return ExitValidation
	}
	if errors.Is(err, credentials.ErrNotFound) ||
		errors.Is(err, credentials.ErrIncomplete) ||
		errors.Is(err, credentials.ErrInvalidPrivateKey) ||
		errors.Is(err, credentials.ErrInsecurePrivateKey) {
		return ExitAuth
	}

	var samsungError *samsung.APIError
	if errors.As(err, &samsungError) {
		return exitCodeForHTTPStatus(samsungError.StatusCode)
	}
	var authError *auth.APIError
	if errors.As(err, &authError) {
		return exitCodeForHTTPStatus(authError.StatusCode)
	}
	return ExitError
}

func exitCodeForHTTPStatus(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ExitAuth
	case http.StatusNotFound:
		return ExitNotFound
	case http.StatusConflict:
		return ExitConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ExitValidation
	default:
		return ExitHTTPFailure
	}
}
