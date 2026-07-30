package authcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

type revokeOptions struct {
	Profile string
	Output  string
	Mode    shared.MutationMode
}

type revokeResult struct {
	Profile        string       `json:"profile"`
	ServiceAccount string       `json:"serviceAccountId"`
	Revoked        bool         `json:"revoked"`
	TokenDeleted   bool         `json:"tokenDeleted"`
	DryRun         bool         `json:"dryRun"`
	Plan           *shared.Plan `json:"plan,omitempty"`
}

func (result revokeResult) OutputHeaders() []string {
	return []string{"PROFILE", "SERVICE ACCOUNT", "REVOKED", "LOCAL TOKEN DELETED", "STATUS"}
}

func (result revokeResult) OutputRows() [][]string {
	status := "revoked"
	if result.DryRun {
		status = "planned"
	}
	return [][]string{{
		result.Profile,
		result.ServiceAccount,
		fmt.Sprint(result.Revoked),
		fmt.Sprint(result.TokenDeleted),
		status,
	}}
}

func runRevoke(ctx context.Context, dependencies Dependencies, options revokeOptions) error {
	if err := validateCredentialDependencies(dependencies); err != nil {
		return err
	}
	profile := strings.TrimSpace(options.Profile)
	if err := shared.RequireValue("--profile", profile); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	if err := options.Mode.RequireConfirmation("permanently revoke the Galaxy Store access token"); err != nil {
		return err
	}

	cfg, err := dependencies.LoadConfig()
	if err != nil {
		return err
	}
	store, err := dependencies.OpenTokenStore()
	if err != nil {
		return err
	}
	if store == nil {
		return errors.New("OS token store is unavailable")
	}
	resolved, err := dependencies.ResolveCredentials(
		credentials.Options{Profile: profile},
		cfg,
		store,
	)
	if err != nil {
		return err
	}
	if resolved.Kind != credentials.KindAccessToken || strings.TrimSpace(resolved.AccessToken) == "" {
		return shared.UsageErrorf("auth revoke requires a stored access token for profile %q", profile)
	}

	result := revokeResult{
		Profile:        profile,
		ServiceAccount: resolved.ServiceAccountID,
		DryRun:         options.Mode.DryRun,
	}
	if options.Mode.DryRun {
		result.Plan = &shared.Plan{
			Operations: []shared.Operation{
				{Action: "revoke", Resource: "Samsung access token", Details: "permanently revoke the token for profile " + profile},
				{Action: "delete", Resource: "OS credential manager", Details: "delete the local token only after remote revocation succeeds"},
			},
			Warnings:             []string{"Revocation is permanent and cannot be undone."},
			RequiresConfirmation: true,
			MutationsPerformed:   false,
		}
		return dependencies.Printer.Print(format, result)
	}

	response, err := dependencies.Client.Revoke(ctx, resolved.ServiceAccountID, resolved.AccessToken)
	if err != nil {
		return safeOperationError("revoke access token", err, resolved.AccessToken)
	}
	if response == nil || !response.OK {
		return errors.New("Samsung returned an invalid token revocation response")
	}
	if err := store.Delete(profile); err != nil {
		return safeOperationError("delete revoked token from OS credential manager", err, resolved.AccessToken)
	}

	result.Revoked = true
	result.TokenDeleted = true
	return dependencies.Printer.Print(format, result)
}

var _ output.RowSource = revokeResult{}
