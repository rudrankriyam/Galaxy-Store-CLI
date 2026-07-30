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
	samsungauth "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

type loginOptions struct {
	Profile          string
	ServiceAccountID string
	PrivateKeyPath   string
	Scopes           []string
	Output           string
	SetDefault       bool
	DryRun           bool
}

type loginResult struct {
	Profile          string       `json:"profile"`
	ServiceAccountID string       `json:"serviceAccountId"`
	Scopes           []string     `json:"scopes"`
	Default          bool         `json:"default"`
	Authenticated    bool         `json:"authenticated"`
	TokenStored      bool         `json:"tokenStored"`
	ConfigSaved      bool         `json:"configSaved"`
	ReplacedToken    bool         `json:"replacedExistingToken"`
	PreviousRevoked  bool         `json:"previousTokenRevoked"`
	DryRun           bool         `json:"dryRun"`
	Plan             *shared.Plan `json:"plan,omitempty"`
}

func (result loginResult) OutputHeaders() []string {
	return []string{"PROFILE", "SERVICE ACCOUNT", "SCOPES", "DEFAULT", "STATUS"}
}

func (result loginResult) OutputRows() [][]string {
	status := "authenticated"
	if result.DryRun {
		status = "planned"
	}
	return [][]string{{
		result.Profile,
		result.ServiceAccountID,
		strings.Join(result.Scopes, ","),
		fmt.Sprint(result.Default),
		status,
	}}
}

func runLogin(ctx context.Context, dependencies Dependencies, options loginOptions) error {
	if err := validateLoginDependencies(dependencies); err != nil {
		return err
	}
	profile := strings.TrimSpace(options.Profile)
	serviceAccountID := strings.TrimSpace(options.ServiceAccountID)
	if err := shared.RequireValue("--profile", profile); err != nil {
		return err
	}
	if err := shared.RequireValue("--service-account-id", serviceAccountID); err != nil {
		return err
	}
	if err := shared.RequireValue("--private-key", options.PrivateKeyPath); err != nil {
		return err
	}
	format, err := parseOutput(options.Output)
	if err != nil {
		return err
	}
	authScopes, configScopes, err := normalizeScopes(options.Scopes)
	if err != nil {
		return err
	}
	privateKeyPath, err := absolutePath(options.PrivateKeyPath)
	if err != nil {
		return err
	}

	currentConfig, err := dependencies.LoadConfig()
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			return err
		}
		currentConfig = &config.Config{Profiles: make(map[string]config.Profile)}
	}
	nextConfig := cloneConfig(currentConfig)
	if nextConfig.Profiles == nil {
		nextConfig.Profiles = make(map[string]config.Profile)
	}
	nextConfig.Profiles[profile] = config.Profile{
		ServiceAccountID: serviceAccountID,
		PrivateKeyPath:   privateKeyPath,
		Scopes:           append([]string(nil), configScopes...),
	}
	if options.SetDefault || strings.TrimSpace(nextConfig.DefaultProfile) == "" {
		nextConfig.DefaultProfile = profile
	}
	if err := nextConfig.Validate(); err != nil {
		return err
	}

	privateKey, err := dependencies.LoadPrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	privateKeyPEM, err := encodePrivateKey(privateKey)
	if err != nil {
		return err
	}
	now := dependencies.Now
	signedJWT, err := dependencies.SignJWT(samsungauth.JWTConfig{
		ServiceAccountID: serviceAccountID,
		Scopes:           authScopes,
		PrivateKeyPEM:    privateKeyPEM,
		Now:              now,
	})
	clear(privateKeyPEM)
	if err != nil {
		return err
	}

	result := loginResult{
		Profile:          profile,
		ServiceAccountID: serviceAccountID,
		Scopes:           configScopes,
		Default:          nextConfig.DefaultProfile == profile,
		DryRun:           options.DryRun,
	}
	if options.DryRun {
		result.Plan = &shared.Plan{
			Operations: []shared.Operation{
				{Action: "exchange", Resource: "Samsung access token", Details: "exchange one short-lived service-account JWT"},
				{Action: "store", Resource: "OS credential manager", Details: "store access token for profile " + profile},
				{Action: "save", Resource: "gsc profile configuration", Details: "persist non-secret service-account metadata"},
			},
			Warnings:             []string{"Galaxy Store access tokens remain valid until revoked."},
			RequiresConfirmation: false,
			MutationsPerformed:   false,
		}
		return dependencies.Printer.Print(format, result)
	}

	store, err := dependencies.OpenTokenStore()
	if err != nil {
		return err
	}
	if store == nil {
		return errors.New("OS token store is unavailable")
	}
	previousToken, hadPreviousToken, err := existingToken(store, profile)
	if err != nil {
		return err
	}

	response, err := dependencies.Client.Exchange(ctx, signedJWT)
	if err != nil {
		return safeError(err, signedJWT)
	}
	signedJWT = ""
	if response == nil || !response.OK {
		return errors.New("Samsung returned an invalid access-token response")
	}
	accessToken := strings.TrimSpace(response.CreatedItem.AccessToken)
	if accessToken == "" {
		return errors.New("Samsung returned an empty access token")
	}

	if err := store.Set(profile, accessToken); err != nil {
		cleanupErr := cleanupLoginFailure(
			ctx,
			dependencies.Client,
			store,
			profile,
			serviceAccountID,
			accessToken,
			previousToken,
			hadPreviousToken,
		)
		return joinCompensationError(
			safeOperationError("store access token", err, accessToken),
			cleanupErr,
		)
	}
	if err := dependencies.SaveConfig(nextConfig); err != nil {
		cleanupErr := cleanupLoginFailure(
			ctx,
			dependencies.Client,
			store,
			profile,
			serviceAccountID,
			accessToken,
			previousToken,
			hadPreviousToken,
		)
		return joinCompensationError(
			safeOperationError("save profile metadata", err, accessToken),
			cleanupErr,
		)
	}

	if hadPreviousToken && previousToken != accessToken {
		result.ReplacedToken = true
		revokeResponse, revokeErr := dependencies.Client.Revoke(
			ctx,
			serviceAccountID,
			previousToken,
		)
		if revokeErr != nil {
			return fmt.Errorf(
				"new credentials are active, but previous token revocation could not be confirmed: %w",
				safeError(revokeErr, previousToken, accessToken),
			)
		}
		if revokeResponse == nil || !revokeResponse.OK {
			return errors.New(
				"new credentials are active, but Samsung did not confirm previous token revocation",
			)
		}
		result.PreviousRevoked = true
	}

	result.Authenticated = true
	result.TokenStored = true
	result.ConfigSaved = true
	return dependencies.Printer.Print(format, result)
}

func existingToken(store credentials.TokenStore, profile string) (string, bool, error) {
	token, err := store.Get(profile)
	switch {
	case err == nil:
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false, errors.New("stored access token is empty")
		}
		return token, true, nil
	case errors.Is(err, credentials.ErrTokenNotFound):
		return "", false, nil
	default:
		return "", false, err
	}
}

func cleanupLoginFailure(
	ctx context.Context,
	client Client,
	store credentials.TokenStore,
	profile string,
	serviceAccountID string,
	newToken string,
	previousToken string,
	hadPreviousToken bool,
) error {
	var cleanupErrors []error
	if hadPreviousToken {
		if err := store.Set(profile, previousToken); err != nil {
			cleanupErrors = append(
				cleanupErrors,
				safeOperationError("restore previous local token", err, previousToken, newToken),
			)
		}
	} else {
		if err := store.Delete(profile); err != nil {
			cleanupErrors = append(
				cleanupErrors,
				safeOperationError("remove new local token", err, newToken),
			)
		}
	}
	response, err := client.Revoke(ctx, serviceAccountID, newToken)
	if err != nil {
		cleanupErrors = append(
			cleanupErrors,
			safeOperationError("revoke new remote token", err, newToken),
		)
	} else if response == nil || !response.OK {
		cleanupErrors = append(
			cleanupErrors,
			errors.New("revoke new remote token: Samsung did not confirm revocation"),
		)
	}
	return errors.Join(cleanupErrors...)
}

func joinCompensationError(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("login compensation incomplete: %w", cleanup))
}

func cloneConfig(source *config.Config) *config.Config {
	if source == nil {
		return &config.Config{Profiles: make(map[string]config.Profile)}
	}
	clone := &config.Config{
		DefaultProfile: source.DefaultProfile,
		Profiles:       make(map[string]config.Profile, len(source.Profiles)),
	}
	for name, profile := range source.Profiles {
		profile.Scopes = append([]string(nil), profile.Scopes...)
		clone.Profiles[name] = profile
	}
	return clone
}

func safeError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "<redacted>")
		}
	}
	return &redactedError{err: err, message: message}
}

func safeOperationError(operation string, err error, secrets ...string) error {
	return fmt.Errorf("%s: %w", operation, safeError(err, secrets...))
}

type redactedError struct {
	err     error
	message string
}

func (err *redactedError) Error() string {
	return err.message
}

func (err *redactedError) Unwrap() error {
	return err.err
}

var _ output.RowSource = loginResult{}
