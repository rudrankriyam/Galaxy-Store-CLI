package authcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

type statusOptions struct {
	Profile string
	Output  string
}

type statusResult struct {
	Profile          string   `json:"profile,omitempty"`
	ServiceAccountID string   `json:"serviceAccountId"`
	Scopes           []string `json:"scopes,omitempty"`
	Valid            bool     `json:"valid"`
}

func (result statusResult) OutputHeaders() []string {
	return []string{"PROFILE", "SERVICE ACCOUNT", "SCOPES", "VALID"}
}

func (result statusResult) OutputRows() [][]string {
	return [][]string{{
		result.Profile,
		result.ServiceAccountID,
		strings.Join(result.Scopes, ","),
		fmt.Sprint(result.Valid),
	}}
}

func runStatus(ctx context.Context, dependencies Dependencies, options statusOptions) error {
	if err := validateCredentialDependencies(dependencies); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	cfg, err := dependencies.LoadConfig()
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			return err
		}
		cfg = &config.Config{Profiles: make(map[string]config.Profile)}
	}
	store, err := dependencies.OpenTokenStore()
	if err != nil {
		return err
	}
	resolved, err := dependencies.ResolveCredentials(
		credentials.Options{Profile: strings.TrimSpace(options.Profile)},
		cfg,
		store,
	)
	if err != nil {
		return err
	}
	if resolved.Kind != credentials.KindAccessToken || strings.TrimSpace(resolved.AccessToken) == "" {
		return shared.UsageErrorf("auth status requires a stored or environment access token")
	}

	response, err := dependencies.Client.Check(ctx, resolved.ServiceAccountID, resolved.AccessToken)
	if err != nil {
		return safeOperationError("check access token", err, resolved.AccessToken)
	}
	if response == nil || !response.OK {
		return errors.New("Samsung returned an invalid token status response")
	}
	return dependencies.Printer.Print(format, statusResult{
		Profile:          resolved.Profile,
		ServiceAccountID: resolved.ServiceAccountID,
		Scopes:           append([]string(nil), resolved.Scopes...),
		Valid:            true,
	})
}

var _ output.RowSource = statusResult{}
