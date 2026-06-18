package runtime

// Input-guard limits for stdlib helpers that operate on script-provided
// data. These caps are fixed (not configurable through Config) so the
// JSON and Regex builtins stay bounded regardless of how an embedder
// tunes the engine. They are documented for embedders in
// docs/stdlib_core_utilities.md and the README's "Runtime Sandbox &
// Limits" section; keep those in sync when changing a value.
const (
	// maxJSONPayloadBytes caps JSON.parse input and JSON.stringify
	// output at 1 MiB. JSON decoding allocates proportionally to the
	// payload, so the cap keeps a hostile document from ballooning
	// host memory before interpreter quotas can account for it.
	maxJSONPayloadBytes = 1 << 20

	// maxJSONNestingDepth caps JSON.parse and JSON.stringify container
	// nesting. It matches the standard library JSON scanner's default
	// guard and prevents recursive descent from exhausting the Go stack.
	maxJSONNestingDepth = 10000

	// maxRegexInputBytes caps the subject text, replacement strings,
	// and produced output of every regex entry point (Regex.match,
	// Regex.replace, Regex.replace_all, and the string match/scan/
	// sub/gsub members including the ! variants) at 1 MiB, bounding
	// both scan time and the size of replacement results.
	maxRegexInputBytes = 1 << 20

	// maxRegexPatternSize caps regex patterns at 16 KiB. Patterns are
	// compiled before any quota accounting happens, and pathological
	// patterns are far smaller than pathological inputs, so the
	// pattern cap is deliberately much tighter than the text cap.
	maxRegexPatternSize = 16 << 10
)
