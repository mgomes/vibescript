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

// TestStringScanManyGroupsPeakMemory exercises the reviewer's P1 scenario: a
// pattern made of many empty () capture groups over a large subject. Calling
// FindAllStringSubmatchIndex(text, -1) here would materialize matches × 2(groups+1)
// index integers as one contiguous allocation, OOMing the host inside that call.
// scan projects the worst-case index footprint -- (runeCount+1) slices of
// 2 + 2*groups ints -- from the rune and group counts alone and rejects before
// invoking the engine when that worst case overflows the memory quota, so the scan
// trips the memory limit cleanly without ever materializing the table. Reaching
// that error (rather than crashing the test process) is the regression guard.
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

// TestStringScanSparseMatchesNotOverRejected guards the worst-case bound against
// over-rejecting low-group patterns. The worst-case projection is
// (runeCount+1) * (2 + 2*groups) * intBytes, so a no-capture pattern -- here a digit
// pattern over a 1000-character all-letter subject -- projects only
// 1001 * 2 * 8 ~= 16 KiB even though it never matches, comfortably under the 64 KiB
// quota. The scan must run and return an empty result rather than trip the memory
// limit: the worst-case rejection is meant to catch many-capture-group footprints,
// not legitimate low-group scans whose index table is small regardless of the match
// count.
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

// TestStringScanSparseManyGroupsRejectedWithoutOOM pins the worst-case guard's
// deliberate, documented over-rejection: a many-group pattern whose worst-case
// index footprint ((runeCount+1) matches of 2 + 2*groups ints) exceeds the quota is
// rejected up front even though its ACTUAL match count would fit. The pattern
// matches only runs of thirty 'x's, so over a long mostly-'a' subject it matches
// just twice -- but resolving that real count would require a counting FindAll whose
// own index slice reintroduces the host-memory spike the guard exists to prevent, so
// the scan conservatively rejects from the rune and group counts alone. This is the
// sandbox-safe trade-off: legitimate low-group scans are never rejected (their index
// table is small regardless of match count, see TestStringScanSparseMatchesNotOverRejected),
// only pathological many-capture-group patterns over large subjects. Reaching the
// memory error rather than exhausting host memory inside FindAll is the guard.
func TestStringScanSparseManyGroupsRejectedWithoutOOM(t *testing.T) {
	t.Parallel()

	const groups = 30
	pattern := strings.Repeat("(x)", groups)
	source := `def run(text) text.scan("` + pattern + `").size end`
	// Worst case ~ (runeCount+1) * (2 + 2*groups) * 8 bytes exceeds 512 KiB for a
	// ~4060-rune subject, so the guard rejects before the engine runs -- never
	// materializing the table that would otherwise spike host memory.
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 512 * 1024}, source)

	run := strings.Repeat("x", groups)
	subject := NewString(strings.Repeat("a", 2000) + run + strings.Repeat("a", 2000) + run)
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
}

// TestStringScanThousandsOfGroupsRejectedBeforeScan is the direct regression for
// the reviewer's P1: a pattern of thousands of empty () capture groups (still well
// under the 16 KiB pattern cap). A literal FindAllStringSubmatchIndex(text, -1)
// would request matches × 2(groups+1) index integers -- many gigabytes -- and OOM
// the host inside that call before any per-element accounting ran. scan rejects this
// from the rune and group counts alone: the worst-case projection ((runeCount+1)
// slices of 2 + 2*groups ints) dwarfs the quota, so the scan errors with the memory
// limit and never calls any FindAll variant. Reaching that error (rather than
// exhausting host memory) is the guard, so this test stays fast and cheap precisely
// because no match is ever run.
func TestStringScanThousandsOfGroupsRejectedBeforeScan(t *testing.T) {
	t.Parallel()

	const groups = 4_000
	pattern := strings.Repeat("()", groups) // ~8 KiB, under the 16 KiB pattern cap.
	source := `def run(text) text.scan("` + pattern + `") end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)

	// 500 characters yield a ~501-match worst case; each submatch slice is
	// 2 + 2*4000 = 8002 ints, so the projected index footprint (~32 MB) dwarfs the
	// 64 KiB quota and the scan rejects up front, before the engine materializes the
	// table -- keeping the test fast while exercising the many-group rejection path.
	subject := NewString(strings.Repeat("a", 500))
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
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
// the [][]int table's per-match slice overhead against the memory quota. It exercises
// guardRegexScanIndexFootprint directly with a no-capture pattern (the shape the
// integer-only projection undercounts most): the slice headers are 24 of every 40
// bytes per match. The quota is set so the integer-only footprint fits but the real
// footprint -- ints plus the per-match slice header -- does not, so the guard must
// reject. A guard that charged only the integers would admit this scan and let the
// engine materialize a table larger than the quota allows.
func TestStringScanGuardChargesSliceOverhead(t *testing.T) {
	t.Parallel()

	const (
		groups   = 0
		runes    = 4000
		maxMatch = runes + 1 // FindAllStringSubmatchIndex returns at most runeCount+1 matches.
	)
	text := strings.Repeat("a", runes)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	base := exec.estimateMemoryUsageBase(exec.memoryEstimatorForCheck())

	intOnlyBytes := maxMatch * (2 + 2*groups) * estimatedIntBytes
	fullBytes := projectedRegexSubmatchIndexBytes(maxMatch, groups)
	if fullBytes != intOnlyBytes+maxMatch*estimatedSliceBaseBytes {
		t.Fatalf("projection %d does not equal int bytes %d plus per-match slice headers %d",
			fullBytes, intOnlyBytes, maxMatch*estimatedSliceBaseBytes)
	}

	// Quota sits strictly between the int-only footprint and the full footprint, so it
	// admits the former and rejects the latter -- the slice overhead is the only thing
	// that pushes the projection past the limit.
	exec.memoryQuota = base + intOnlyBytes + maxMatch*estimatedSliceBaseBytes/2

	if err := guardRegexScanIndexFootprint(exec, text, groups); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("guard at int-only-plus-half-overhead quota = %v, want errMemoryQuotaExceeded", err)
	}

	// Raising the quota to cover the full footprint (ints plus every slice header)
	// admits the scan, confirming the guard rejects only the overhead deficit and not
	// the legitimate footprint.
	exec.memoryQuota = base + fullBytes
	if err := guardRegexScanIndexFootprint(exec, text, groups); err != nil {
		t.Fatalf("guard at full-footprint quota = %v, want success", err)
	}
}
