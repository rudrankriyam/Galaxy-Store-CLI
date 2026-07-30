package shared

import (
	"errors"
	"testing"
)

func TestValidateContentID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid leading zeros", value: "000007654321"},
		{name: "too short", value: "7654321", wantErr: true},
		{name: "too long", value: "0000007654321", wantErr: true},
		{name: "contains letter", value: "00000765A321", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateContentID(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateContentID(%q) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
			if err != nil {
				var usageErr *UsageError
				if !errors.As(err, &usageErr) {
					t.Fatalf("error type = %T, want *UsageError", err)
				}
			}
		})
	}
}

func TestNormalizeAppStatus(t *testing.T) {
	status, err := NormalizeAppStatus(" registration ")
	if err != nil {
		t.Fatalf("NormalizeAppStatus() error = %v", err)
	}
	if status != "REGISTRATION" {
		t.Fatalf("status = %q, want REGISTRATION", status)
	}
}

func TestValidateMonotonicPercentage(t *testing.T) {
	if err := ValidateMonotonicPercentage(25, 50); err != nil {
		t.Fatalf("advance error = %v", err)
	}
	if err := ValidateMonotonicPercentage(25, 10); err == nil {
		t.Fatal("decrease error = nil, want error")
	}
}

func TestValidateLimit(t *testing.T) {
	if err := ValidateLimit(1000, 1000); err != nil {
		t.Fatalf("limit error = %v", err)
	}
	if err := ValidateLimit(1001, 1000); err == nil {
		t.Fatal("limit error = nil, want error")
	}
}
