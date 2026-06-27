package ast

import "strings"

// Canonical runtime error type names recognized by both the parser
// (for rescue-clause validation) and the runtime (for classifying
// errors). Kept here so the parser does not need to depend on the
// runtime package.
const (
	RuntimeErrorTypeBase      = "RuntimeError"
	RuntimeErrorTypeStandard  = "StandardError"
	RuntimeErrorTypeAssertion = "AssertionError"
	RuntimeErrorTypeLimit     = "LimitError"
	RuntimeErrorTypeType      = "TypeError"
	RuntimeErrorTypeZeroDiv   = "ZeroDivisionError"
	RuntimeErrorTypeLocalJump = "LocalJumpError"
	RuntimeErrorTypeArgument  = "ArgumentError"
)

// CanonicalRuntimeErrorType returns the canonical spelling of a
// recognized runtime error type name and reports whether it is known.
func CanonicalRuntimeErrorType(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, RuntimeErrorTypeBase), strings.EqualFold(name, "Error"):
		return RuntimeErrorTypeBase, true
	case strings.EqualFold(name, RuntimeErrorTypeStandard):
		return RuntimeErrorTypeStandard, true
	case strings.EqualFold(name, RuntimeErrorTypeAssertion):
		return RuntimeErrorTypeAssertion, true
	case strings.EqualFold(name, RuntimeErrorTypeLimit):
		return RuntimeErrorTypeLimit, true
	case strings.EqualFold(name, RuntimeErrorTypeType):
		return RuntimeErrorTypeType, true
	case strings.EqualFold(name, RuntimeErrorTypeZeroDiv):
		return RuntimeErrorTypeZeroDiv, true
	case strings.EqualFold(name, RuntimeErrorTypeLocalJump):
		return RuntimeErrorTypeLocalJump, true
	case strings.EqualFold(name, RuntimeErrorTypeArgument):
		return RuntimeErrorTypeArgument, true
	default:
		return "", false
	}
}
