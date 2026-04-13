package deployment

import "errors"

// Sentinel errors for the deployment aggregate.
var (
	ErrNotFound         = errors.New("deployment not found")
	ErrPolicyViolation  = errors.New("policy violation")
	ErrChartNotFound    = errors.New("chart not found")
	ErrChartValidation  = errors.New("chart validation failed")
	ErrChartDependency  = errors.New("chart dependency error")
	ErrAppProjectDenied = errors.New("app project access denied")
	ErrAuthentication   = errors.New("authentication failed")
)

// NonRetryableTypes returns all error type names for Temporal retry exclusion.
func NonRetryableTypes() []string {
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
