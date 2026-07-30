package shared

import "fmt"

// UsageError reports invalid command invocation or local input.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

// UsageErrorf constructs an error that maps to the CLI usage exit code.
func UsageErrorf(format string, args ...any) error {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}
