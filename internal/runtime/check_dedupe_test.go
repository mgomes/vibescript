package runtime

import (
	"strings"
	"testing"
)

// compileSnippetForCheck compiles source the way `vibes check` does: top-level
// statements are wrapped into a synthetic entrypoint. The duplication this
// file is about only arises for top-level call sites, so a test that hoists
// the calls into a named function does not exercise it.
func compileSnippetForCheck(t *testing.T, source string) *Script {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(source, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return script
}

// The checker walks a function's body once per call site, so one mistake in a
// helper produced one identical diagnostic per caller. A helper called eight
// times reported the same undefined constant eight times, burying the other
// genuine findings and making the issue count describe call sites rather than
// problems.
func TestCheckWarningsDeduplicateAcrossCallSites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		wantCount int
	}{
		{
			name: "one call site",
			source: `
            def c(n)
              n < LIMIT
            end
            puts c(1)
            `,
			wantCount: 1,
		},
		{
			// The same body, reached three times, is still one mistake.
			name: "three call sites report once",
			source: `
            def c(n)
              n < LIMIT
            end
            puts c(1)
            puts c(2)
            puts c(3)
            `,
			wantCount: 1,
		},
		{
			name: "many call sites report once",
			source: `
            def c(n)
              n < LIMIT
            end
            puts c(1)
            puts c(2)
            puts c(3)
            puts c(4)
            puts c(5)
            puts c(6)
            puts c(7)
            puts c(8)
            `,
			wantCount: 1,
		},
		{
			// Two mistakes in one repeatedly called body stay two.
			name: "two distinct mistakes in one body",
			source: `
            def c(n)
              n < LIMIT
              n > OTHER
            end
            puts c(1)
            puts c(2)
            `,
			wantCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileSnippetForCheck(t, tc.source)
			warnings := script.CheckWarnings()
			if len(warnings) != tc.wantCount {
				t.Fatalf("%s reported %d warnings, want %d: %v", tc.name, len(warnings), tc.wantCount, warnings)
			}
		})
	}
}

// Deduplication must not hide a genuine finding. The repeated diagnostic used
// to bury the others in the same run, which is the cost that made this worth
// fixing, so the other findings have to survive.
func TestDeduplicationKeepsDistinctFindings(t *testing.T) {
	t.Parallel()
	script := compileSnippetForCheck(t, `
    def helper(n)
      n < LIMIT
    end
    def other()
      MISSING_TWO
    end
    def third()
      undefined_three
    end
    puts helper(1)
    puts helper(2)
    puts helper(3)
    puts helper(4)
    puts other()
    puts third()
    `)
	warnings := script.CheckWarnings()
	if len(warnings) != 3 {
		t.Fatalf("reported %d warnings, want 3: %v", len(warnings), warnings)
	}
	for _, want := range []string{"LIMIT", "MISSING_TWO", "undefined_three"} {
		found := false
		for _, warning := range warnings {
			if strings.Contains(warning.Message, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("warning naming %s was dropped: %v", want, warnings)
		}
	}
}

// Warnings differing in any field are distinct problems and must both survive.
func TestDedupeCheckWarningsKeepsDifferingFields(t *testing.T) {
	t.Parallel()

	base := CheckWarning{Function: "f", Pos: Position{Line: 3, Column: 7}, Message: "undefined variable LIMIT"}
	differentLine := base
	differentLine.Pos.Line = 4
	differentColumn := base
	differentColumn.Pos.Column = 9
	differentMessage := base
	differentMessage.Message = "undefined variable OTHER"
	differentFunction := base
	differentFunction.Function = "g"
	differentSource := base
	differentSource.Source = "other.vibe"

	warnings := []CheckWarning{base, base, differentLine, differentColumn, differentMessage, differentFunction, differentSource, base}
	got := dedupeCheckWarnings(warnings)
	if len(got) != 6 {
		t.Fatalf("deduped to %d warnings, want 6: %v", len(got), got)
	}
	if got[0] != base {
		t.Fatalf("first warning = %v, want the original order preserved", got[0])
	}
}

// Deduplication preserves the order the sort established, so the reported
// findings stay in source order.
func TestDedupeCheckWarningsPreservesOrder(t *testing.T) {
	t.Parallel()

	first := CheckWarning{Function: "f", Pos: Position{Line: 1, Column: 1}, Message: "a"}
	second := CheckWarning{Function: "f", Pos: Position{Line: 2, Column: 1}, Message: "b"}
	third := CheckWarning{Function: "f", Pos: Position{Line: 3, Column: 1}, Message: "c"}

	got := dedupeCheckWarnings([]CheckWarning{first, second, first, third, second})
	want := []CheckWarning{first, second, third}
	if len(got) != len(want) {
		t.Fatalf("deduped to %d warnings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("warning %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A checked function keeps reporting its own body's problems.
func TestDeduplicationLeavesSingleWarningsAlone(t *testing.T) {
	t.Parallel()
	script := compileSnippetForCheck(t, `
    def c(n)
      n < LIMIT
    end
    puts c(1)
    `)
	requireCheckWarningContains(t, script, "LIMIT")
}
