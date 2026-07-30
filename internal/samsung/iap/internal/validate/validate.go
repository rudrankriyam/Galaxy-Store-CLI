// Package validate contains the common identifier validation used by Samsung
// IAP API packages.
package validate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PackageName validates an Android application ID before it is placed in an API
// path or request body.
func PackageName(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return errors.New("package name is required without surrounding whitespace")
	}
	if len(value) > 255 {
		return errors.New("package name cannot exceed 255 characters")
	}

	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return errors.New("package name must contain at least two dot-separated segments")
	}
	for _, part := range parts {
		if part == "" {
			return errors.New("package name segments cannot be empty")
		}
		for index, character := range part {
			if isASCIILetter(character) || character == '_' || (index > 0 && isASCIIDigit(character)) {
				continue
			}
			return errors.New("package name contains an invalid character")
		}
	}
	return nil
}

// PurchaseID validates an opaque Samsung transaction identifier used as one
// URL path segment.
func PurchaseID(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return errors.New("purchase ID is required without surrounding whitespace")
	}
	for _, character := range value {
		if isASCIILetter(character) || isASCIIDigit(character) ||
			character == '-' || character == '_' || character == '.' || character == '~' {
			continue
		}
		return errors.New("purchase ID contains an invalid character")
	}
	return nil
}

// SellerSequence validates Samsung's documented 12-digit seller deeplink.
func SellerSequence(value string) error {
	if value != strings.TrimSpace(value) || len(value) != 12 {
		return errors.New("seller sequence must contain exactly 12 digits")
	}
	for _, character := range value {
		if !isASCIIDigit(character) {
			return errors.New("seller sequence must contain exactly 12 digits")
		}
	}
	return nil
}

// RequestDate validates the optional yyyyMMdd date used by the Orders API.
func RequestDate(value string) error {
	if value == "" {
		return nil
	}
	if len(value) != len("20060102") {
		return errors.New("request date must use YYYYMMDD format")
	}
	if _, err := time.Parse("20060102", value); err != nil {
		return fmt.Errorf("request date must be a valid date in YYYYMMDD format: %w", err)
	}
	return nil
}

// ContinuationToken validates an optional opaque pagination token.
func ContinuationToken(value string) error {
	if value != "" && value != strings.TrimSpace(value) {
		return errors.New("continuation token cannot contain surrounding whitespace")
	}
	return nil
}

func isASCIILetter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func isASCIIDigit(character rune) bool {
	return character >= '0' && character <= '9'
}
