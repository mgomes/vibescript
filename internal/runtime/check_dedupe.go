package runtime

// dedupeCheckWarnings drops warnings that repeat an earlier one exactly.
//
// The checker walks a function's body once per call site, so a single mistake
// inside a helper produced one diagnostic per caller: same file, same line,
// same column, same text. A helper called eight times reported the same
// undefined constant eight times, which buried the other genuine findings in
// the run and made "check failed with N issue(s)" count call sites rather than
// problems.
//
// Warnings that differ in any field survive, so a diagnostic that genuinely
// says something different per call site (a different argument type, say) is
// still reported once per distinct message. Two warnings identical in every
// field are indistinguishable to the reader, so collapsing them loses nothing.
func dedupeCheckWarnings(warnings []CheckWarning) []CheckWarning {
	if len(warnings) < 2 {
		return warnings
	}
	seen := make(map[CheckWarning]struct{}, len(warnings))
	deduped := make([]CheckWarning, 0, len(warnings))
	for _, warning := range warnings {
		if _, repeated := seen[warning]; repeated {
			continue
		}
		seen[warning] = struct{}{}
		deduped = append(deduped, warning)
	}
	return deduped
}
