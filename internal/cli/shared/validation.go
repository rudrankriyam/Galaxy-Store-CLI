package shared

import (
	"fmt"
	"strings"
)

const contentIDLength = 12

// ValidateContentID validates Samsung's 12-digit content identifier.
func ValidateContentID(value string) error {
	if len(value) != contentIDLength {
		return UsageErrorf("content ID must contain exactly %d digits", contentIDLength)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return UsageErrorf("content ID must contain exactly %d digits", contentIDLength)
		}
	}
	return nil
}

// NormalizeAppStatus validates the state selector required when SALE and
// REGISTRATION variants can coexist for the same content ID.
func NormalizeAppStatus(value string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(value))
	switch status {
	case "SALE", "REGISTRATION":
		return status, nil
	case "":
		return "", UsageErrorf("app status is required and must be SALE or REGISTRATION")
	default:
		return "", UsageErrorf("invalid app status %q: must be SALE or REGISTRATION", value)
	}
}

// RequireValue rejects a blank required flag without leaking its value.
func RequireValue(flagName, value string) error {
	if strings.TrimSpace(value) == "" {
		return UsageErrorf("%s is required", flagName)
	}
	return nil
}

// ValidatePercentage validates a staged rollout rate.
func ValidatePercentage(value int) error {
	if value < 0 || value > 100 {
		return UsageErrorf("rollout rate must be between 0 and 100, got %d", value)
	}
	return nil
}

// ValidateMonotonicPercentage prevents a deployed rollout from moving back.
func ValidateMonotonicPercentage(current, requested int) error {
	if err := ValidatePercentage(requested); err != nil {
		return err
	}
	if requested < current {
		return UsageErrorf(
			"rollout rate cannot decrease after deployment: current %d, requested %d",
			current,
			requested,
		)
	}
	return nil
}

// ValidateLimit checks a positive API pagination limit and its documented cap.
func ValidateLimit(value, maximum int) error {
	if value <= 0 || value > maximum {
		return UsageErrorf("limit must be between 1 and %d, got %d", maximum, value)
	}
	return nil
}

// DescribeAmbiguousAppState returns a stable diagnostic for state ambiguity.
func DescribeAmbiguousAppState(contentID string) string {
	return fmt.Sprintf(
		"content %s has distinct SALE and REGISTRATION records; pass --app-status explicitly",
		contentID,
	)
}
