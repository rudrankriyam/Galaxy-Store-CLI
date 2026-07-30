package cmd

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const redactedDiagnosticValue = "[REDACTED]"

var credentialDiagnosticPatterns = []struct {
	expression  *regexp.Regexp
	replacement string
}{
	{
		expression: regexp.MustCompile(
			`(?i)(authorization[ \t]*[:=][ \t]*)(?:(?:bearer|basic)[ \t]+)?[^,;[:space:]]+`,
		),
		replacement: `${1}` + redactedDiagnosticValue,
	},
	{
		expression:  regexp.MustCompile(`(?i)\bbearer[ \t]+[^,;[:space:]]+`),
		replacement: `Bearer ` + redactedDiagnosticValue,
	},
	{
		expression: regexp.MustCompile(
			`(?i)((?:(?:access|refresh)[-_ ]?)?token|service[-_ ]?account(?:[-_ ]?id)?|` +
				`gsc_(?:access_token|service_account_id)|signed[-_ ]?jwt|jwt|private[-_ ]?key)` +
				`[ \t]*[:=][ \t]*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^,;[:space:]]+)`,
		),
		replacement: `${1}=` + redactedDiagnosticValue,
	},
	{
		expression: regexp.MustCompile(
			`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`,
		),
		replacement: redactedDiagnosticValue,
	},
	{
		expression: regexp.MustCompile(
			`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?` +
				`-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`,
		),
		replacement: redactedDiagnosticValue,
	},
}

func printError(stderr io.Writer, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(stderr, "Error: %s\n", sanitizeDiagnostic(err.Error()))
}

func sanitizeDiagnostic(message string) string {
	for _, variable := range []string{"GSC_ACCESS_TOKEN", "GSC_SERVICE_ACCOUNT_ID"} {
		if secret := strings.TrimSpace(os.Getenv(variable)); secret != "" {
			message = strings.ReplaceAll(message, secret, redactedDiagnosticValue)
		}
	}
	for _, pattern := range credentialDiagnosticPatterns {
		message = pattern.expression.ReplaceAllString(message, pattern.replacement)
	}
	return message
}
