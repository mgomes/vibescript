package runtime

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"unsafe"
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

// TestStringScanAdjacentMultiRune pins the adjacent multi-rune cases to concrete,
// Ruby-confirmed results rather than only to engine self-consistency. A prior
// look-back-window advancement returned the leftmost match but failed to resume at
// its end, dropping the second of two abutting matches: "abcd".scan("..") yielded
// ["ab"] instead of ["ab", "cd"], and the multibyte "café".scan("..") yielded
// ["ca"] instead of ["ca", "fé"]. These hardcoded expectations match MRI Ruby.
func TestStringScanAdjacentMultiRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		pattern string
		want    []Value
	}{
		{
			name:    "two byte matches",
			text:    "abcd",
			pattern: "..",
			want:    []Value{NewString("ab"), NewString("cd")},
		},
		{
			name:    "multibyte runes pair up",
			text:    "café",
			pattern: "..",
			want:    []Value{NewString("ca"), NewString("fé")},
		},
		{
			name:    "diaeresis runes pair up",
			text:    "naïve",
			pattern: "..",
			want:    []Value{NewString("na"), NewString("ïv")},
		},
		{
			name:    "repeated ascii rune",
			text:    "wwww",
			pattern: "ww",
			want:    []Value{NewString("ww"), NewString("ww")},
		},
		{
			name:    "two captures abutting",
			text:    "abcd",
			pattern: "(.)(.)",
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("b")}),
				NewArray([]Value{NewString("c"), NewString("d")}),
			},
		},
		{
			name:    "optional second capture trails",
			text:    "abc",
			pattern: "(.)(.)?",
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("b")}),
				NewArray([]Value{NewString("c"), NewNil()}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Cross-check the hardcoded expectation against the engine so a fixture
			// typo cannot silently diverge from Go's own match set.
			re := regexp.MustCompile(tt.pattern)
			engine := scanWantFromRegexp(re, tt.text)
			compareArrays(t, NewArray(engine), tt.want)

			source := `def run(text) text.scan(` + goStringToVibescript(tt.pattern) + `) end`
			script := compileScript(t, source)
			got := callFunc(t, script, "run", []Value{NewString(tt.text)})
			compareArrays(t, got, tt.want)
		})
	}
}

// TestStringScanAnchoredZeroWidth verifies that String#scan evaluates anchors and
// zero-width assertions against the full subject, matching Ruby and Go's own
// FindAllStringSubmatchIndex. A prior streaming implementation that re-ran the
// regex on text[pos:] substrings made these assertions fire at every slice
// boundary and returned wrong results (e.g. "abc".scan("^") yielding four matches
// instead of one). The expected counts below are confirmed against MRI Ruby; the
// test also asserts the scan output equals what regexp.FindAllStringSubmatchIndex
// reports so the regression cannot recur silently.
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

// TestStringScanMatchesFindAllParity pins String#scan's match set to
// regexp.FindAllStringSubmatchIndex(text, -1) across a broad pattern/text matrix.
// scan delegates advancement to that engine call, so the result must reproduce its
// match set byte for byte; this is the canary that fails if a future change
// reintroduces a hand-rolled advancement that drifts from the engine (the anchor
// regression from matching against a detached suffix, or the dropped-adjacent-
// match regression from a look-back window).
func TestStringScanMatchesFindAllParity(t *testing.T) {
	t.Parallel()

	patterns := []string{
		`a`, `[a-z]`, `[a-z][0-9]`, `\d+`, `\s`, `.`,
		`(a)`, `(\w)(\d)?`, `(x)(y)?(z*)`, `(\w)(-)?`,
		`\b`, `\B`, `^`, `$`, `\A`, `\z`,
		`\b\w+\b`, `\bcat\b`, `^\w`, `\w$`, `a*`, `x?`,
		// Adjacent multi-rune patterns: a look-back-window advancement dropped the
		// second of two abutting matches ("abcd".scan("..") -> ["ab"] not
		// ["ab","cd"]). These exercise that exact shape, including multibyte runes.
		`..`, `(.)(.)?`, `ww`, `\w\w`, `..(.)?`,
		// Zero-width and alternation-with-empty-branch patterns exercise the
		// empty-match suppression at every position.
		``, `b*`, `(ab)*`, `a|`, `^|$`, `\b|x`, `a??`, `(?:)`,
	}
	texts := []string{
		"", "a", "abc", "abcd", "a1 b2 c3", "a-b-c", "the cat sat on a cataract",
		"a b c", "  spaced  ", "line1\nline2\nline3", "über café",
		"café", "naïve", "wwww", "wwwww", "ωωωω",
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
// result would exceed the memory quota fails with the limit error instead of
// materializing an unbounded array. The subject matches at every position, so the
// projected submatch-index footprint (and the accumulated nested-array result that
// follows) far exceeds the tight quota; the scan must trip the memory limit rather
// than build the whole result.
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

// TestStringScanManyGroupsPeakMemory exercises a many-empty-group pattern over a
// large subject whose worst-case index table is host-safe (well under the
// maxRegexScanIndexBytes cap) but whose result and that coexisting table exceed a
// tight memory quota. The host cap admits the scan up front; the engine materializes
// the table host-safely; then the array-build accumulator -- seeded with the table's
// actual footprint and charging each per-match capture array -- trips the memory quota
// as the result accumulates. So the scan errors cleanly with the memory limit rather
// than building an unbounded result. This is the memory-quota counterpart to
// TestStringScanThousandsOfGroupsRejectedBeforeScan, where the worst case instead
// overflows the fixed host cap and the engine is never called at all.
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
// index projection does not reject results that genuinely fit and that scan yields
// the right match count for an all-empty-groups pattern (one empty match per
// position plus one at the end).
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

// TestStringScanSparseMatchesNotOverRejected guards the worst-case host-cap bound
// against over-rejecting low-group patterns. A no-capture digit pattern over a
// 1000-character all-letter subject projects a worst-case index table of only
// 1000 * (2*8 + 24) ~= 40 KiB, far under the 256 MiB host cap, even though it never
// matches. The scan must run and return an empty result: the host-cap rejection
// catches many-capture-group footprints, not legitimate low-group scans whose index
// table is small regardless of match count. The tight memory quota here never
// rejects the scan because the actual (empty) result is charged incrementally and
// fits comfortably.
func TestStringScanSparseMatchesNotOverRejected(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, `def run(text)
  text.scan("\\d+").size
end`)

	subject := NewString(strings.Repeat("a", 1000))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("sparse no-match scan = %v, want success (over-rejection regression)", err)
	}
	if got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("sparse no-match scan size = %v, want int 0", got)
	}
}

// TestStringScanSparseManyGroupsNotOverRejected is the regression for the reviewer's
// P1 on the worst-case match bound. A many-capture-group pattern whose every match
// must consume runes (here thirty "(x)" groups, so each match consumes 30 runes) can
// match at most runeCount/30 times -- a handful over a long mostly-'a' subject, not
// the runeCount+1 a zero-width pattern reaches. Bounding the worst case by the
// pattern's minimum match length (regexScanMaxMatches) keeps this scan's projected
// index footprint small, so the guard admits it and the scan returns its real (tiny)
// match count instead of rejecting on a zero-width worst case the pattern cannot reach.
//
// Before the min-match-length bound this scan's worst case was projected at
// (runeCount+1) * (2 + 2*groups) ints and rejected under this quota even though it
// matches only twice -- the over-rejection the reviewer flagged. The genuinely
// dangerous shape (a ZERO-WIDTH many-group pattern, whose runeCount+1 worst case is
// real) is still rejected; see TestStringScanThousandsOfGroupsRejectedBeforeScan and
// TestStringScanManyGroupsPeakMemory.
func TestStringScanSparseManyGroupsNotOverRejected(t *testing.T) {
	t.Parallel()

	const groups = 30
	pattern := strings.Repeat("(x)", groups)
	source := `def run(text) text.scan("` + pattern + `").size end`
	// The same 512 KiB quota that the pre-fix worst case ((runeCount+1) matches)
	// overflowed: the min-match-length bound (runeCount/30 matches) projects a tiny
	// index footprint, so the guard must admit the scan rather than reject it.
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 512 * 1024}, source)

	run := strings.Repeat("x", groups)
	subject := NewString(strings.Repeat("a", 2000) + run + strings.Repeat("a", 2000) + run)
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("sparse many-group scan = %v, want success (over-rejection regression)", err)
	}
	// The pattern matches each of the two thirty-'x' runs exactly once.
	if got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("sparse many-group scan size = %v, want int 2", got)
	}
}

// TestStringScanDenseManyGroupsRejected confirms the worst-case guard still rejects a
// non-zero-width many-group pattern whose matches are DENSE: when every position can
// start a match, runeCount/minRunes is large and the projected index footprint really
// does overflow the quota, so the scan must reject before the engine materializes the
// table. This is the counterpart to TestStringScanSparseManyGroupsNotOverRejected --
// the min-match-length bound tightens the worst case for sparse patterns without
// blinding the guard to genuinely large tables.
func TestStringScanDenseManyGroupsRejected(t *testing.T) {
	t.Parallel()

	const groups = 30
	// Thirty "(a)" groups: each match consumes 30 'a's, and an all-'a' subject lets a
	// match start at every 30-rune boundary, so matches ~ runeCount/30 -- large enough
	// that the (2 + 2*groups)-int submatch slices overflow the quota.
	pattern := strings.Repeat("(a)", groups)
	source := `def run(text) text.scan("` + pattern + `").size end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 256 * 1024}, source)

	subject := NewString(strings.Repeat("a", 60_000))
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
}

// TestStringScanThousandsOfGroupsRejectedBeforeScan is the direct regression for the
// host-OOM half of the reviewer's P1: a pattern of thousands of empty () capture
// groups (still under the 16 KiB pattern cap). A literal FindAllStringSubmatchIndex(
// text, -1) would request matches × 2(groups+1) index integers -- hundreds of
// megabytes to gigabytes -- and OOM the host inside that call before any per-element
// accounting ran. scan rejects this from the rune and group counts alone: the
// worst-case projection ((runeCount+1) slices of 2 + 2*groups ints) exceeds the fixed
// maxRegexScanIndexBytes host cap, so the scan errors with the regex-scan limit and
// never calls any FindAll variant. Reaching that error (rather than exhausting host
// memory) is the guard, so this test stays fast and cheap precisely because no match
// is ever run. The error is the fixed host-cap limit, independent of the memory quota.
func TestStringScanThousandsOfGroupsRejectedBeforeScan(t *testing.T) {
	t.Parallel()

	const groups = 4_000
	pattern := strings.Repeat("()", groups) // ~8 KiB, under the 16 KiB pattern cap.
	source := `def run(text) text.scan("` + pattern + `") end`
	// Memory quota is generous: the rejection comes from the fixed host cap, not the
	// quota, proving the guard protects host memory regardless of how the quota is set.
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 1 << 30}, source)

	// The pattern is zero-width, so the worst case is (runeCount+1) matches; each
	// submatch slice is 2 + 2*4000 = 8002 ints (~64 KB with its header). 5000 runes
	// project ~320 MB, past the 256 MiB host cap, so the scan rejects up front -- never
	// materializing the table -- keeping the test fast while exercising the host-cap path.
	subject := NewString(strings.Repeat("a", 5000))
	requireCallErrorContains(t, script, "run", []Value{subject}, CallOptions{}, "string.scan match table exceeds limit")
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

// TestStringScanQuotaCheckedBeforeMaterializing is the regression for charging a
// step BEFORE FindAllStringSubmatchIndex runs. The per-match step charges fire only
// once the match table exists, so a scan whose pattern matches NOTHING would never
// step at all if the only steps were per match: an already-canceled context or an
// exhausted step quota would go unobserved and the (expensive) full materialization
// would run anyway before the empty result was returned. The pre-materialization
// step closes that hole, so even a zero-match scan trips the limit/cancellation
// before paying for the engine call.
func TestStringScanQuotaCheckedBeforeMaterializing(t *testing.T) {
	t.Parallel()

	t.Run("canceled context aborts a zero-match scan", func(t *testing.T) {
		t.Parallel()

		// "z" never matches an all-'a' subject, so the per-match loop body never runs;
		// only the pre-materialization step can observe the canceled context here.
		script := compileScript(t, `def run(text)
  text.scan("z")
end`)
		subject := NewString(strings.Repeat("a", 10_000))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := script.Call(ctx, "run", []Value{subject}, CallOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("zero-match scan under canceled context = %v, want context.Canceled", err)
		}
	})

	t.Run("exhausted step quota aborts a zero-match scan", func(t *testing.T) {
		t.Parallel()

		// A one-step quota is consumed entering run(); the scan's pre-materialization
		// step then trips the limit before the engine ever runs, even though the
		// pattern matches nothing and the per-match loop would never step.
		script := compileScriptWithConfig(t, Config{StepQuota: 1, MemoryQuotaBytes: 64 << 20}, `def run(text)
  text.scan("z")
end`)
		subject := NewString(strings.Repeat("a", 10_000))
		requireCallRuntimeErrorType(t, script, "run", []Value{subject}, CallOptions{}, runtimeErrorTypeLimit)
	})
}

// TestStringScanIndexTableAndResultChargedTogether is the direct regression for
// the reviewer's second P1: the engine's [][]int index table stays live the whole
// time scan materializes per-match result elements from it, so the index table and
// the growing result coexist at peak. scan seeds the array-build accumulator with
// the index table's footprint (projectedRegexSubmatchIndexBytes) so both are charged
// TOGETHER. Without that seed the result alone could fit while result + index table
// together exceeded the quota -- the hole this test pins shut.
//
// The two builds below construct the IDENTICAL result through the array-build
// accumulator and differ only in whether the index footprint is reserved. At a
// quota set to the peak the seeded (correct) build needs, the unseeded (buggy) build
// has slack exactly equal to the seed, so it succeeds: that gap is the bug, and the
// seed reservation is what closes it. Driving the accumulator directly makes the
// seed the only variable, so the assertion cannot drift with unrelated estimator
// constants.
func TestStringScanIndexTableAndResultChargedTogether(t *testing.T) {
	t.Parallel()

	const (
		groups  = 1
		matches = 4000
	)
	re := regexp.MustCompile(strings.Repeat("(.)", groups))
	subject := NewString(strings.Repeat("a", matches))

	// Capture the exact result elements and the index seed scan would charge.
	allMatches := re.FindAllStringSubmatchIndex(subject.String(), -1)
	if len(allMatches) != matches {
		t.Fatalf("match count = %d, want %d (fixture out of sync)", len(allMatches), matches)
	}
	seed := projectedRegexSubmatchIndexBytes(len(allMatches), groups)
	if seed <= 0 {
		t.Fatalf("index seed = %d, want a positive footprint", seed)
	}

	// buildResult replays scan's accumulator loop over the captured matches under the
	// given quota, optionally reserving the index footprint, and reports whether the
	// build stayed within quota.
	buildResult := func(quota int, withSeed bool) error {
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		acc := newArrayBuildAccumulator(exec, subject, []Value{}, nil, NewNil())
		if withSeed {
			if err := acc.reserveScratch(seed); err != nil {
				return err
			}
		}
		out := make([]Value, 0, min(len(allMatches), stringScanInitialCap))
		for _, loc := range allMatches {
			out = append(out, stringScanElement(subject.String(), loc, groups))
			if err := acc.add(out[len(out)-1], cap(out)); err != nil {
				return err
			}
		}
		return nil
	}

	// Find the minimum quota at which the seeded build succeeds: its peak holds the
	// index table and the whole result together.
	lo, hi := 0, seed*64
	for lo < hi {
		mid := (lo + hi) / 2
		if buildResult(mid, true) == nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	seededPeak := lo

	// At one byte below the seeded peak the correct build must reject.
	if err := buildResult(seededPeak-1, true); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("seeded build below peak = %v, want errMemoryQuotaExceeded", err)
	}

	// At that same seeded peak the UNSEEDED build still has the entire seed as unused
	// slack, so it succeeds -- proving the result alone fits well under the quota and
	// that the seed is the only thing pushing the coexisting peak over. This is the
	// exact regression: drop the seed and the index table rides along uncharged.
	if err := buildResult(seededPeak, false); err != nil {
		t.Fatalf("unseeded build at seeded peak = %v, want success (seed should be the only extra charge)", err)
	}
	// The unseeded build also fits with the whole seed shaved off, confirming the gap
	// between the two builds is the seed, not incidental rounding.
	if err := buildResult(seededPeak-seed, false); err != nil {
		t.Fatalf("unseeded build at peak minus seed = %v, want success", err)
	}
}

// TestProjectedRegexSubmatchIndexBytesChargesSliceOverhead pins the per-match slice
// overhead into the index-footprint projection. The [][]int table the engine returns
// is not just its index integers: each match is an inner []int whose slice header
// occupies one slot in the outer backing array (24 bytes, unsafe.Sizeof([]int{})).
// Charging only the integers undercounts that table -- by more than half for a
// no-capture zero-width or one-byte pattern, whose two ints are 16 bytes against the
// 24-byte header -- so the projection must add estimatedSliceBaseBytes per match.
func TestProjectedRegexSubmatchIndexBytesChargesSliceOverhead(t *testing.T) {
	t.Parallel()

	if got, want := estimatedSliceBaseBytes, int(unsafe.Sizeof([]int{})); got != want {
		t.Fatalf("estimatedSliceBaseBytes = %d, want a []int header of %d bytes", got, want)
	}

	tests := []struct {
		name       string
		matchCount int
		groups     int
	}{
		{name: "no-capture matches charge two ints plus the slice header", matchCount: 1000, groups: 0},
		{name: "single-capture matches charge four ints plus the slice header", matchCount: 250, groups: 1},
		{name: "many-capture matches charge the slice header on top of the ints", matchCount: 7, groups: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			intsPerMatch := 2 + 2*tt.groups
			indexBytes := tt.matchCount * intsPerMatch * estimatedIntBytes
			want := indexBytes + tt.matchCount*estimatedSliceBaseBytes

			got := projectedRegexSubmatchIndexBytes(tt.matchCount, tt.groups)
			if got != want {
				t.Fatalf("projectedRegexSubmatchIndexBytes(%d, %d) = %d, want %d (ints %d + slice headers %d)",
					tt.matchCount, tt.groups, got, want, indexBytes, tt.matchCount*estimatedSliceBaseBytes)
			}
			if got <= indexBytes {
				t.Fatalf("projection %d did not charge the per-match slice header on top of the %d index bytes", got, indexBytes)
			}
		})
	}
}

// TestStringScanGuardChargesSliceOverhead is the behavioral regression for charging
// the [][]int table's per-match slice overhead against the fixed host cap. It
// exercises guardRegexScanIndexFootprint directly at a subject size chosen so the
// integer-only worst case fits under maxRegexScanIndexBytes but the real worst case --
// ints PLUS the per-match slice header -- does not. A guard that charged only the
// integers would admit a scan whose table is actually larger than the cap allows, so
// the guard must reject here. A subject one rune shorter, where even the full
// (header-inclusive) footprint fits, must be admitted.
func TestStringScanGuardChargesSliceOverhead(t *testing.T) {
	t.Parallel()

	// Forty empty zero-width groups: maxMatches == runeCount+1, and the (2 + 2*40)*8
	// int bytes per match are large enough that the cap is reached at a runeCount under
	// the 1 MiB subject limit, yet the 24-byte slice header per match still spans a
	// range of runeCounts -- the window this test lands in.
	const groups = 40
	pattern := strings.Repeat("()", groups)
	intBytesPerMatch := (2 + 2*groups) * estimatedIntBytes
	fullBytesPerMatch := intBytesPerMatch + estimatedSliceBaseBytes

	// Pick maxMatches so the int-only footprint fits the cap but the full footprint
	// (with every slice header) exceeds it: maxMatches just above cap/fullBytesPerMatch.
	maxMatches := maxRegexScanIndexBytes/fullBytesPerMatch + 1
	if intOnly := maxMatches * intBytesPerMatch; intOnly > maxRegexScanIndexBytes {
		t.Fatalf("int-only footprint %d already exceeds cap %d; pick fewer groups or matches", intOnly, maxRegexScanIndexBytes)
	}
	if full := projectedRegexSubmatchIndexBytes(maxMatches, groups); full <= maxRegexScanIndexBytes {
		t.Fatalf("full footprint %d does not exceed cap %d; the slice header is not the deciding factor", full, maxRegexScanIndexBytes)
	}

	runeCount := maxMatches - 1 // zero-width pattern: maxMatches == runeCount+1.
	if runeCount > maxRegexInputBytes {
		t.Fatalf("rune count %d exceeds the %d-byte subject cap; pick more groups", runeCount, maxRegexInputBytes)
	}
	text := strings.Repeat("a", runeCount)

	// The slice headers are what push the projection past the cap, so the guard rejects.
	if err := guardRegexScanIndexFootprint(pattern, text, groups); err == nil {
		t.Fatalf("guard admitted a table whose full footprint %d exceeds the cap %d (slice overhead uncharged?)",
			projectedRegexSubmatchIndexBytes(maxMatches, groups), maxRegexScanIndexBytes)
	}

	// One rune shorter, the full (header-inclusive) footprint fits, so the guard admits.
	shorter := strings.Repeat("a", runeCount-1)
	if full := projectedRegexSubmatchIndexBytes(runeCount, groups); full > maxRegexScanIndexBytes {
		t.Fatalf("full footprint %d for the shorter subject still exceeds the cap %d; the window is too narrow", full, maxRegexScanIndexBytes)
	}
	if err := guardRegexScanIndexFootprint(pattern, shorter, groups); err != nil {
		t.Fatalf("guard at one rune below the cap = %v, want success", err)
	}
}

// TestRegexScanMinMatchRunes pins the lower bound the worst-case guard divides the
// subject's rune count by. The value MUST never exceed a pattern's true minimum match
// length: regexScanMaxMatches divides by it, so an over-estimate would under-bound the
// match count and let the engine materialize a table larger than the quota admits.
// Zero-width-capable patterns (and anything not provably consuming input) report 0,
// collapsing the worst case to runeCount+1.
func TestRegexScanMinMatchRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    int
	}{
		{pattern: "z", want: 1},
		{pattern: "abc", want: 3},
		{pattern: "café", want: 4}, // four runes, not five bytes -- the bound is rune-based.
		{pattern: "[a-z]", want: 1},
		{pattern: ".", want: 1},
		{pattern: "([a-z])([0-9])", want: 2},
		{pattern: strings.Repeat("(x)", 30), want: 30},
		{pattern: "a+", want: 1},
		{pattern: "(ab)+", want: 2},
		{pattern: `\d{3}`, want: 3},
		{pattern: "a{2,5}", want: 2},
		{pattern: "abc|de", want: 2}, // alternation takes its shortest branch.
		{pattern: "a*", want: 0},     // zero-width: the empty string matches.
		{pattern: "a?", want: 0},
		{pattern: "", want: 0},
		{pattern: `\b`, want: 0}, // a word boundary consumes nothing.
		{pattern: "^abc$", want: 3},
		{pattern: strings.Repeat("()", 40), want: 0}, // all-empty groups match empty.
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()

			if got := regexScanMinMatchRunes(tt.pattern); got != tt.want {
				t.Fatalf("regexScanMinMatchRunes(%q) = %d, want %d", tt.pattern, got, tt.want)
			}
		})
	}
}

// TestRegexScanMaxMatchesBoundsRealEngine is the safety invariant the whole guard
// rests on: the bound regexScanMaxMatches computes from the pattern and subject alone,
// without running the engine, must never be exceeded by the real match count
// FindAllStringSubmatchIndex produces. If it ever were, the guard could admit a scan
// whose table overflows the quota. Each case checks the analytic bound against the
// engine's actual result.
func TestRegexScanMaxMatchesBoundsRealEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		text    string
	}{
		{name: "no match sparse", pattern: "z", text: strings.Repeat("a", 2000)},
		{name: "dense single char", pattern: "a", text: strings.Repeat("a", 500)},
		{name: "two-char matches", pattern: "..", text: strings.Repeat("ab", 250)},
		{
			name: "many-group sparse", pattern: strings.Repeat("(x)", 30),
			text: strings.Repeat("a", 2000) + strings.Repeat("x", 30) + strings.Repeat("a", 2000) + strings.Repeat("x", 30),
		},
		{name: "zero-width star", pattern: "a*", text: strings.Repeat("a", 300)},
		{name: "all-empty groups", pattern: strings.Repeat("()", 5), text: strings.Repeat("a", 100)},
		{name: "multibyte runes", pattern: ".", text: strings.Repeat("é", 200)},
		{name: "empty subject", pattern: "a", text: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bound := regexScanMaxMatches(tt.pattern, tt.text)
			real := len(regexp.MustCompile(tt.pattern).FindAllStringSubmatchIndex(tt.text, -1))
			if real > bound {
				t.Fatalf("regexScanMaxMatches(%q, len %d) = %d, but engine produced %d matches (bound must not be exceeded)",
					tt.pattern, len(tt.text), bound, real)
			}
		})
	}
}

// TestStringScanSparseNoMatchUnderDefaultQuota is the exact scenario from the
// reviewer's P1: scanning a 2 KB string with a no-capture, non-zero-width pattern that
// never matches must succeed under the default 64 KiB quota NewEngine applies, not be
// rejected on a zero-width worst case the pattern cannot reach. The empty result is
// tiny; the old runeCount+1 projection rejected it before matching.
func TestStringScanSparseNoMatchUnderDefaultQuota(t *testing.T) {
	t.Parallel()

	// 64 KiB matches the default NewEngine applies when MemoryQuotaBytes is unset.
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, `def run(text)
  text.scan("z").size
end`)

	subject := NewString(strings.Repeat("a", 2000))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("sparse no-match scan under default quota = %v, want success", err)
	}
	if got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("sparse no-match scan size = %v, want int 0", got)
	}
}

// tightestScanQuota binary-searches the smallest MemoryQuotaBytes under which
// run(text) still succeeds, so a test can pin a scan's real live peak without
// hand-computing every transient the interpreter holds.
func tightestScanQuota(t *testing.T, source string, text Value) int {
	t.Helper()
	admits := func(quota int) bool {
		script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
		_, err := script.Call(context.Background(), "run", []Value{text}, CallOptions{})
		return err == nil
	}
	lo, hi := 0, 1<<24
	if !admits(hi) {
		t.Fatalf("upper bound quota %d did not admit the scan", hi)
	}
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if admits(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// TestStringScanBlockChargesIndexTable is the regression for the reviewer's P1 on
// the block form of String#scan: the engine's [][]int index table stays live for
// the whole block loop, but the block path early-returned before reserving the
// footprint the non-block path folds into its accumulator baseline. Under a tight
// memory quota a block-form scan could therefore hold the large match table (and
// any matches the block retained) while every per-match memory check, which sees
// only the execution's reachable roots, missed the table -- letting the true peak
// exceed the quota by the table's size.
//
// The block body here is empty, so it charges no per-iteration memory: the index
// table is the only large live allocation while the loop runs. A many-empty-groups
// pattern over a sizable subject makes that table dominate. The test pins the
// tightest quota that still admits the empty-body block scan and asserts it sits at
// or above the table's projected footprint, proving the reservation now charges the
// table for the loop's lifetime. Without the fix the table rode along uncharged and
// the tightest quota would be far below that footprint.
func TestStringScanBlockChargesIndexTable(t *testing.T) {
	t.Parallel()

	const groups = 40
	pattern := strings.Repeat("()", groups) // ~80 bytes, well under the pattern cap.
	const count = 4_000
	subject := NewString(strings.Repeat("a", count))

	// The empty-groups pattern matches once per position plus once at the end, so the
	// engine returns count+1 matches; project that table's footprint.
	matches := len(regexp.MustCompile(pattern).FindAllStringSubmatchIndex(subject.String(), -1))
	if matches != count+1 {
		t.Fatalf("match count = %d, want %d (fixture out of sync)", matches, count+1)
	}
	tableBytes := projectedRegexSubmatchIndexBytes(matches, groups)
	if tableBytes <= 0 {
		t.Fatalf("projected index footprint = %d, want a positive table", tableBytes)
	}

	// An empty block body charges no per-iteration memory, so the held index table is
	// the loop's only large allocation. The result is the receiver, never an array.
	emptyBlockScan := `def run(text)
  text.scan("` + pattern + `") do |m| end
end`
	tightest := tightestScanQuota(t, emptyBlockScan, subject)

	// The reservation folds the whole table into the baseline, so the tightest quota
	// that still admits the scan must cover at least that table. Before the fix the
	// table was uncharged and the tightest quota sat far below tableBytes.
	if tightest < tableBytes {
		t.Fatalf("tightest block-scan quota = %d, want >= index table footprint %d (table must be charged)", tightest, tableBytes)
	}

	// Sanity: a quota one byte below the tightest peak rejects, and a generous quota
	// admits, proving the bound comes from the held table and not an unrelated limit.
	reject := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: tightest - 1}, emptyBlockScan)
	requireCallRuntimeErrorType(t, reject, "run", []Value{subject}, CallOptions{}, runtimeErrorTypeLimit)

	roomy := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: tightest + 64*1024}, emptyBlockScan)
	if _, err := roomy.Call(context.Background(), "run", []Value{subject}, CallOptions{}); err != nil {
		t.Fatalf("block scan under a roomy quota = %v, want success", err)
	}
}

// TestStringScanBlockReleasesIndexTableReservation confirms the block-form scan's
// index-table reservation is balanced: it is released once the loop finishes, so a
// scan that fits its own peak does not leave reserved scratch behind to wrongly
// reject later allocations. A sparse no-match scan reserves an empty table and must
// succeed under a tight quota, and code after the scan must still see the full quota.
func TestStringScanBlockReleasesIndexTableReservation(t *testing.T) {
	t.Parallel()

	// The pattern never matches, so the index table is empty and the block never runs;
	// the post-scan array must allocate against the full quota with no leftover charge.
	source := `def run(text)
  text.scan("z") do |m| end
  [1, 2, 3]
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)

	subject := NewString(strings.Repeat("a", 2000))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("sparse block scan then allocate = %v, want success (reservation must be released)", err)
	}
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(3)})
}
