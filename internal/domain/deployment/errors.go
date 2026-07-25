package deployment

import "errors"

// Sentinel errors for the deployment aggregate.
var (
	ErrNotFound           = errors.New("deployment not found")
	ErrPolicyViolation    = errors.New("policy violation")
	ErrTunableLocked      = errors.New("tunable allowlist violation")
	ErrResourceNotAllowed = errors.New("resource policy violation")
	ErrChartNotFound      = errors.New("chart not found")
	ErrChartValidation    = errors.New("chart validation failed")
	ErrChartDependency    = errors.New("chart dependency error")
	ErrAppProjectDenied   = errors.New("app project access denied")
	ErrAuthentication     = errors.New("authentication failed")
)
