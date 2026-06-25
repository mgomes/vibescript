package runtime

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// TestStringScanCaptureShape verifies that String#scan mirrors Ruby's
// capture-aware result shape: no groups yields the full match strings, one or
// more groups yields an array per match holding each captured substring, and
// optional groups that did not participate become nil.
func TestStringScanCaptureShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "no captures returns full matches",
			source: `def run() "a1 b2".scan("[a-z][0-9]") end`,
			want:   []Value{NewString("a1"), NewString("b2")},
		},
		{
			name:   "single capture returns nested single-element arrays",
			source: `def run() "foobar".scan("(o)") end`,
			want: []Value{
				NewArray([]Value{NewString("o")}),
				NewArray([]Value{NewString("o")}),
			},
		},
		{
			name:   "multiple captures return nested arrays",
			source: `def run() "a1 b2".scan("([a-z])([0-9])") end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("1")}),
				NewArray([]Value{NewString("b"), NewString("2")}),
			},
		},
		{
			name:   "optional unmatched capture becomes nil",
			source: `def run() "a-b-c".scan("(\\w)(-)?") end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("-")}),
				NewArray([]Value{NewString("b"), NewString("-")}),
				NewArray([]Value{NewString("c"), NewNil()}),
			},
		},
		{
			name:   "empty capture preserved distinct from nil",
			source: `def run() "x".scan("(x)(y)?(z*)") end`,
			want: []Value{
				NewArray([]Value{NewString("x"), NewNil(), NewString("")}),
			},
		},
		{
			name:   "no match returns empty array",
			source: `def run() "abc".scan("z") end`,
			want:   []Value{},
		},
		{
			name:   "no match with captures returns empty array",
			source: `def run() "abc".scan("(z)(z)") end`,
			want:   []Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			compareArrays(t, got, tt.want)
		})
	}
}

// TestStringScanAnchoredZeroWidth verifies that String#scan evaluates anchors and
// zero-width assertions against the full subject, matching Ruby and Go's own
// FindAllStringSubmatchIndex. A streaming implementation that re-ran the regex on
// text[pos:] substrings made these assertions fire at every slice boundary and
// returned wrong results (e.g. "abc".scan("^") yielding four matches instead of
// one). The expected counts below are confirmed against MRI Ruby; the test also
// asserts the scan output equals what regexp.FindAllStringSubmatchIndex reports so
// the regression cannot recur silently.
func TestStringScanAnchoredZeroWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		pattern string
		// wantRuby is the match count MRI Ruby returns for text.scan(/pattern/).
		wantRuby int
	}{
		{name: "word boundary", text: "a b c", pattern: `\b`, wantRuby: 6},
		{name: "non word boundary", text: "a b c", pattern: `\B`, wantRuby: 0},
		{name: "line start", text: "abc", pattern: `^`, wantRuby: 1},
		{name: "line end", text: "abc", pattern: `$`, wantRuby: 1},
		{name: "string start", text: "abc", pattern: `\A`, wantRuby: 1},
		{name: "boundary with captures", text: "a-b-c", pattern: `\b(\w)`, wantRuby: 3},
		{name: "anchored capture at start", text: "abc", pattern: `^(\w)`, wantRuby: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			re := regexp.MustCompile(tt.pattern)
			want := scanWantFromRegexp(re, tt.text)
			if len(want) != tt.wantRuby {
				t.Fatalf("regexp match count = %d, want Ruby count %d (test fixture out of sync)", len(want), tt.wantRuby)
			}

			source := `def run(text) text.scan(` + goStringToVibescript(tt.pattern) + `) end`
			script := compileScript(t, source)
			got := callFunc(t, script, "run", []Value{NewString(tt.text)})
			compareArrays(t, got, want)
		})
	}
}

// scanWantFromRegexp builds the expected String#scan result for text and re using
// the same capture-shape rules the runtime applies: the full match string when re
// has no capture groups, otherwise a per-match array of captured substrings with
// nil for non-participating groups. It derives the expectation from Go's own
// FindAllStringSubmatchIndex so the test pins scan to that engine's match set.
func scanWantFromRegexp(re *regexp.Regexp, text string) []Value {
	groups := re.NumSubexp()
	locs := re.FindAllStringSubmatchIndex(text, -1)
	want := make([]Value, 0, len(locs))
	for _, loc := range locs {
		if groups == 0 {
			want = append(want, NewString(text[loc[0]:loc[1]]))
			continue
		}
		captures := make([]Value, groups)
		for g := range groups {
			start, end := loc[(g+1)*2], loc[(g+1)*2+1]
			if start < 0 || end < 0 {
				captures[g] = NewNil()
				continue
			}
			captures[g] = NewString(text[start:end])
		}
		want = append(want, NewArray(captures))
	}
	return want
}

// goStringToVibescript renders s as a double-quoted Vibescript string literal,
// escaping backslashes and quotes so a regex pattern survives parsing intact.
func goStringToVibescript(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestStringScanMatchesFindAllParity pins String#scan's streaming match set to
// regexp.FindAllStringSubmatchIndex(text, -1) across a broad pattern/text matrix.
// The streaming advancement and its one-rune look-back window must reproduce the
// all-submatch API's match set byte for byte; this is the canary that fails if a
// future change to either reintroduces the anchor regression (matching against a
// detached suffix) or otherwise drifts from the engine's own iteration.
func TestStringScanMatchesFindAllParity(t *testing.T) {
	t.Parallel()

	patterns := []string{
		`a`, `[a-z]`, `[a-z][0-9]`, `\d+`, `\s`, `.`,
		`(a)`, `(\w)(\d)?`, `(x)(y)?(z*)`, `(\w)(-)?`,
		`\b`, `\B`, `^`, `$`, `\A`, `\z`,
		`\b\w+\b`, `\bcat\b`, `^\w`, `\w$`, `a*`, `x?`,
		// Zero-width and alternation-with-empty-branch patterns exercise the
		// empty-match suppression and the look-back window's boundary-artifact skip.
		``, `b*`, `(ab)*`, `a|`, `^|$`, `\b|x`, `a??`, `(?:)`,
	}
	texts := []string{
		"", "a", "abc", "a1 b2 c3", "a-b-c", "the cat sat on a cataract",
		"a b c", "  spaced  ", "line1\nline2\nline3", "über café",
		"fΩobar", "aaa", "x", "baaab", "abababc", ",,,",
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		source := `def run(text) text.scan(` + goStringToVibescript(pattern) + `) end`
		script := compileScript(t, source)
		for _, text := range texts {
			t.Run(pattern+"/"+text, func(t *testing.T) {
				t.Parallel()
				want := scanWantFromRegexp(re, text)
				got := callFunc(t, script, "run", []Value{NewString(text)})
				compareArrays(t, got, want)
			})
		}
	}
}

// TestStringScanArgumentRejection covers the misuse cases String#scan must
// reject before attempting to match.
func TestStringScanArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "missing pattern",
			source: `def run() "abc".scan() end`,
			want:   "string.scan expects exactly one pattern",
		},
		{
			name:   "extra positional argument",
			source: `def run() "abc".scan("a", "b") end`,
			want:   "string.scan expects exactly one pattern",
		},
		{
			name:   "non-string pattern",
			source: `def run() "abc".scan(1) end`,
			want:   "string.scan pattern must be string",
		},
		{
			name:   "invalid regex",
			source: `def run() "abc".scan("(") end`,
			want:   "string.scan invalid regex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

// TestStringScanCaptureMemoryQuota verifies that a capture-aware scan whose
// accumulated nested-array result would exceed the memory quota fails with the
// limit error instead of materializing an unbounded array. Each match builds a
// fresh nested array, so a subject that matches at every position produces one
// nested array per character; under a tight quota the running accumulator must
// trip before the whole result is built.
func TestStringScanCaptureMemoryQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, `def run(text)
  text.scan("(a)")
end`)

	subject := NewString(strings.Repeat("a", 50_000))
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
}

// TestStringScanCaptureMemoryQuotaUnderAmpleMemory confirms the same large scan
// completes when the memory quota is generous, proving the incremental bound is
// not rejecting results the post-call check would accept.
func TestStringScanCaptureMemoryQuotaUnderAmpleMemory(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}, `def run(text)
  text.scan("(a)").size
end`)

	const count = 50_000
	subject := NewString(strings.Repeat("a", count))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("scan under ample memory = %v, want success", err)
	}
	if got.Kind() != KindInt || got.Int() != count {
		t.Fatalf("scan size = %v, want int %d", got, count)
	}
}

// TestStringScanManyGroupsPeakMemory exercises the reviewer's P1 scenario: a
// pattern made of many empty () capture groups over a large subject. The old
// implementation collected every match with FindAllStringSubmatchIndex(text, -1),
// which materialized matches × 2(groups+1) index integers as one contiguous
// allocation before any quota check ran. Streaming holds only the current match's
// O(groups) index slice and charges each match's result against the quota as the
// result accumulates, so the scan trips the memory limit cleanly. Reaching that
// error (rather than crashing the test process) is the regression guard.
func TestStringScanManyGroupsPeakMemory(t *testing.T) {
	t.Parallel()

	const groups = 40
	pattern := strings.Repeat("()", groups) // ~80 bytes, well under the pattern cap.
	source := `def run(text) text.scan("` + pattern + `") end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 256 * 1024}, source)

	subject := NewString(strings.Repeat("a", 30_000))
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
}

// TestStringScanManyGroupsUnderAmpleMemory confirms the same many-groups pattern
// scans correctly when the memory quota comfortably fits the result, proving the
// streaming bound does not reject results that genuinely fit and that the
// look-back streaming yields the right match count for an all-empty-groups
// pattern (one empty match per position plus one at the end).
func TestStringScanManyGroupsUnderAmpleMemory(t *testing.T) {
	t.Parallel()

	const groups = 40
	pattern := strings.Repeat("()", groups)
	source := `def run(text) text.scan("` + pattern + `").size end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}, source)

	const count = 5_000
	subject := NewString(strings.Repeat("a", count))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("many-groups scan under ample memory = %v, want success", err)
	}
	// Each position yields one (non-suppressed) empty match, plus one at the end.
	if got.Kind() != KindInt || got.Int() != count+1 {
		t.Fatalf("many-groups scan size = %v, want int %d", got, count+1)
	}
}

// TestStringScanStepQuota verifies that scan charges a step per match attempt, so
// a subject yielding far more matches than the step quota allows trips the step
// limit even when the memory quota is ample.
func TestStringScanStepQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, `def run(text)
  text.scan("a")
end`)

	subject := NewString(strings.Repeat("a", 100_000))
	requireCallRuntimeErrorType(t, script, "run", []Value{subject}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestStringScanContextCancellation confirms a canceled context aborts the scan:
// step() polls cancellation on its first invocation, so even a tiny subject is
// enough to observe it.
func TestStringScanContextCancellation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  "aaa".scan("a")
end`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan under canceled context = %v, want context.Canceled", err)
	}
}
