package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// RuntimeError describes a script-level error raised during execution.
type RuntimeError = runtime.RuntimeError

// StackFrame describes a single frame in a RuntimeError stack trace.
type StackFrame = runtime.StackFrame

// ParseIssue is one structured parse failure extracted from an
// Engine.Compile error. Pos is the 1-indexed start position; End is the
// exclusive end of the offending token, or the zero Position when the
// parser could not determine a span.
type ParseIssue = runtime.ParseIssue

// ParseIssues extracts the structured parse failures carried by a
// Compile error, in source order. It returns nil when err is nil or
// carries no parse positions, so callers can fall back to err.Error()
// for non-parse compile failures.
func ParseIssues(err error) []ParseIssue {
	return runtime.ParseIssues(err)
}
