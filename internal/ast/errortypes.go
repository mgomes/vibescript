package ast

import "strings"

// Canonical runtime error type names recognized by both the parser
// (for rescue-clause validation) and the runtime (for classifying
// errors). Kept here so the parser does not need to depend on the
// runtime package.
const (
	RuntimeErrorTypeBase      = "RuntimeError"
	RuntimeErrorTypeAssertion = "AssertionError"
	RuntimeErrorTypeLimit     = "LimitError"
)

// CanonicalRuntimeErrorType returns the canonical spelling of a
// recognized runtime error type name and reports whether it is known.
func CanonicalRuntimeErrorType(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, RuntimeErrorTypeBase), strings.EqualFold(name, "Error"):
		return RuntimeErrorTypeBase, true
	case strings.EqualFold(name, RuntimeErrorTypeAssertion):
		return RuntimeErrorTypeAssertion, true
	case strings.EqualFold(name, RuntimeErrorTypeLimit):
		return RuntimeErrorTypeLimit, true
	default:
		return "", false
	}
}
