package iapcmd

import (
	"strings"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
)

func validatePackageName(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return shared.UsageErrorf("--package-name is required without surrounding whitespace")
	}
	if len(value) > 255 {
		return shared.UsageErrorf("--package-name cannot exceed 255 characters")
	}
	segments := strings.Split(value, ".")
	if len(segments) < 2 {
		return shared.UsageErrorf("--package-name must contain at least two dot-separated segments")
	}
	for _, segment := range segments {
		if segment == "" {
			return shared.UsageErrorf("--package-name segments cannot be empty")
		}
		for index, character := range segment {
			if isASCIILetter(character) || character == '_' || (index > 0 && isASCIIDigit(character)) {
				continue
			}
			return shared.UsageErrorf("--package-name contains an invalid character")
		}
	}
	return nil
}

func validatePurchaseID(flagName, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return shared.UsageErrorf("%s is required without surrounding whitespace", flagName)
	}
	for _, character := range value {
		if isASCIILetter(character) || isASCIIDigit(character) ||
			character == '-' || character == '_' || character == '.' || character == '~' {
			continue
		}
		return shared.UsageErrorf("%s contains an invalid character", flagName)
	}
	return nil
}

func validateSellerSequence(value string) error {
	if value != strings.TrimSpace(value) || len(value) != 12 {
		return shared.UsageErrorf("--seller-seq must contain exactly 12 digits")
	}
	for _, character := range value {
		if !isASCIIDigit(character) {
			return shared.UsageErrorf("--seller-seq must contain exactly 12 digits")
		}
	}
	return nil
}

func validateRequestDate(value string) error {
	if value == "" {
		return nil
	}
	if len(value) != len("20060102") {
		return shared.UsageErrorf("--request-date must use YYYYMMDD format")
	}
	if _, err := time.Parse("20060102", value); err != nil {
		return shared.UsageErrorf("--request-date must be a valid date in YYYYMMDD format")
	}
	return nil
}

func validateContinuationToken(value string) error {
	if value != "" && value != strings.TrimSpace(value) {
		return shared.UsageErrorf("--continuation-token cannot contain surrounding whitespace")
	}
	return nil
}

func isASCIILetter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func isASCIIDigit(character rune) bool {
	return character >= '0' && character <= '9'
}

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}
