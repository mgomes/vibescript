package runtime

// ParseIssue is one structured parse failure extracted from a Compile
// error. Pos is the 1-indexed position where the issue starts; End is
// the exclusive end of the offending token, or the zero Position when
// the parser could not determine a span. Message carries the bare error
// text without the position prefix or rendered code frame.
type ParseIssue struct {
	Pos     Position
	End     Position
	Message string
}

// positionedParseError is the contract parser errors satisfy so issues
// can be extracted without exporting parser types.
type positionedParseError interface {
	error
	Pos() Position
	End() Position
	Message() string
}

// ParseIssues extracts the structured parse failures carried by a
// Compile error, in source order. It returns nil for nil errors and for
// errors that carry no parse positions (such as size-limit or duplicate
// top-level name failures).
func ParseIssues(err error) []ParseIssue {
	var issues []ParseIssue
	collectParseIssues(err, &issues)
	return issues
}

// collectParseIssues walks the full error tree itself rather than using
// errors.As: As stops at the first match, while a combined compile error
// carries one positioned error per parse failure and every node must be
// collected in order.
func collectParseIssues(err error, issues *[]ParseIssue) {
	if err == nil {
		return
	}
	//nolint:errorlint // deliberate per-node check; wrapped errors are reached by the recursion below
	if pe, ok := err.(positionedParseError); ok {
		*issues = append(*issues, ParseIssue{Pos: pe.Pos(), End: pe.End(), Message: pe.Message()})
		return
	}
	//nolint:errorlint // deliberate per-node unwrap traversal, not a terminal type test
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, inner := range unwrapped.Unwrap() {
			collectParseIssues(inner, issues)
		}
	case interface{ Unwrap() error }:
		collectParseIssues(unwrapped.Unwrap(), issues)
	}
}
