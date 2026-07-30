package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPrintErrorSanitizesCredentialCanariesAtCommandBoundary(t *testing.T) {
	const (
		authorizationCanary = "authorization-canary"
		bearerCanary        = "bearer-canary"
		tokenCanary         = "token-canary"
		accountCanary       = "service-account-canary"
		jwtCanary           = "eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJqd3QtY2FuYXJ5In0.c2lnbmF0dXJlLWNhbmFyeQ"
		privateKeyCanary    = "private-key-canary"
	)
	wrapped := fmt.Errorf(
		"open session: %w",
		errors.New(
			"transport rejected "+
				"Authorization: Basic "+authorizationCanary+"; "+
				"Bearer "+bearerCanary+"; "+
				"access_token="+tokenCanary+"; "+
				"service-account-id="+accountCanary+"; "+
				"signed credential "+jwtCanary+"; "+
				"-----BEGIN PRIVATE KEY-----\n"+privateKeyCanary+
				"\n-----END PRIVATE KEY-----",
		),
	)
	var stderr bytes.Buffer

	printError(&stderr, wrapped)

	diagnostic := stderr.String()
	for _, canary := range []string{
		authorizationCanary,
		bearerCanary,
		tokenCanary,
		accountCanary,
		jwtCanary,
		privateKeyCanary,
	} {
		if strings.Contains(diagnostic, canary) {
			t.Fatalf("stderr leaked %q: %s", canary, diagnostic)
		}
	}
	for _, safeContext := range []string{"Error:", "open session", "transport rejected"} {
		if !strings.Contains(diagnostic, safeContext) {
			t.Fatalf("stderr = %q, want safe context %q", diagnostic, safeContext)
		}
	}
}

func TestPrintErrorSanitizesBareEnvironmentCredentials(t *testing.T) {
	const (
		tokenCanary   = "environment-token-canary"
		accountCanary = "environment-account-canary"
	)
	t.Setenv("GSC_ACCESS_TOKEN", tokenCanary)
	t.Setenv("GSC_SERVICE_ACCOUNT_ID", accountCanary)
	var stderr bytes.Buffer

	printError(
		&stderr,
		fmt.Errorf("open session: transport echoed %s and %s", tokenCanary, accountCanary),
	)

	for _, canary := range []string{tokenCanary, accountCanary} {
		if strings.Contains(stderr.String(), canary) {
			t.Fatalf("stderr leaked %q: %s", canary, stderr.String())
		}
	}
}

func TestSanitizeDiagnosticPreservesSafeCredentialGuidance(t *testing.T) {
	message := `no stored Galaxy Store access token is available; ` +
		`resolved credentials have no service account ID; run "gsc auth login"`

	if got := sanitizeDiagnostic(message); got != message {
		t.Fatalf("sanitizeDiagnostic() = %q, want safe guidance %q", got, message)
	}
}
