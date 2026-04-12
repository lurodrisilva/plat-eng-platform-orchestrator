package domain

import "fmt"

// NonRetryableError wraps an error to signal Temporal that it should not be retried.
type NonRetryableError struct {
	Type    string
	Message string
	Cause   error
}

func (e *NonRetryableError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

func (e *NonRetryableError) Unwrap() error { return e.Cause }

func NewPolicyViolationError(msg string) *NonRetryableError {
	return &NonRetryableError{Type: "PolicyViolationError", Message: msg}
}

func NewAuthenticationError(msg string) *NonRetryableError {
	return &NonRetryableError{Type: "AuthenticationError", Message: msg}
}

func NewChartValidationError(msg string, cause error) *NonRetryableError {
	return &NonRetryableError{Type: "ChartValidationError", Message: msg, Cause: cause}
}

func NewChartNotFoundError(repo, constraint string) *NonRetryableError {
	return &NonRetryableError{
		Type:    "ChartNotFoundError",
		Message: fmt.Sprintf("no chart tags found in %s matching constraint %q", repo, constraint),
	}
}

func NewChartDependencyError(msg string) *NonRetryableError {
	return &NonRetryableError{Type: "ChartDependencyError", Message: msg}
}

func NewAppProjectMismatchError(field, value string, allowed []string) *NonRetryableError {
	return &NonRetryableError{
		Type:    "AppProjectMismatchError",
		Message: fmt.Sprintf("field %s value %q not in allowed set %v", field, value, allowed),
	}
}

func NewValidationError(msg string) *NonRetryableError {
	return &NonRetryableError{Type: "ValidationError", Message: msg}
}

// NonRetryableErrorTypes returns all error type strings for Temporal retry policy configuration.
func NonRetryableErrorTypes() []string {
	return []string{
		"PolicyViolationError",
		"AuthenticationError",
		"ChartValidationError",
		"ChartNotFoundError",
		"ChartDependencyError",
		"AppProjectMismatchError",
		"ValidationError",
	}
}
