package runtime

import (
	"context"
	"strings"
	"testing"
)

// The #1131 charge stopped at the operator boundary: it fired only when both
// top-level operands were string-like, so a string reached through an array or
// hash compared for a flat handful of steps while the direct comparison scaled
// with the payload — `s == s` needed 68 steps at 4 KiB and 260 at 16 KiB while
// `[s] == [s]` needed 6 at either size (#1135). The charge now lands at the
// recursive scalar comparison, so every container path scales like the direct
// one. These tests pin that with the proportionality shape the string-scan
// suite uses: eight times the payload must cost at least four times the steps.
func TestNestedStringComparisonsChargeForTheirPayloads(t *testing.T) {
	t.Parallel()

	exprs := []string{
		"([s] == [s]).to_s.length",
		"([s] != [s]).to_s.length",
		"({k: s} == {k: s}).to_s.length",
		"([s] <=> [s]).to_s.length",
		"([s] === [s]).to_s.length",
		"[s].include?(s).to_s.length",
		"[s, s].index(s).to_s.length",
		"[s, s].rindex(s).to_s.length",
		"[s, s].count(s).to_s.length",
		"[s, s].sort.length",
		"[s, s].max.length",
		"[s, s].min.length",
		"[s, s].uniq.length",
		"[[s], [s]].uniq.length",
		"[s].eql?([s]).to_s.length",
		"[s].union([s]).length",
		"[s].difference([s]).length",
		"([s] & [s]).length",
		"([s] - [s]).length",
		"{a: 1}[s].to_s.length",
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 64 KiB; the nested "+
					"payload must be charged like the top-level comparison", expr, atSmall, atLarge)
			}
		})
	}
}

// case/when matches its clauses with the same equality walk `===` uses, so a
// big payload in a when clause must be charged like the operator form.
func TestCaseWhenChargesForItsComparisons(t *testing.T) {
	t.Parallel()

	expr := "case [s]\n  when [s] then 1\n  else 2\n  end"
	atSmall := minStepsForStringOp(t, expr, 8<<10)
	atLarge := minStepsForStringOp(t, expr, 64<<10)
	if atLarge < atSmall*4 {
		t.Errorf("case/when cost %d steps over 8 KiB and %d over 64 KiB; clause "+
			"matching must charge the compared payload", atSmall, atLarge)
	}
}

// Equality answers from a length mismatch without reading either payload, and
// that exemption holds through a container exactly as it does at the top
// level: the nested leaf sees the same lengths the operator saw.
func TestNestedEqualityOfDifferentLengthsIsNotCharged(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"([s] == [\"ab\"]).to_s.length",
		"({k: s} == {k: \"ab\"}).to_s.length",
		"[s].include?(\"ab\").to_s.length",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 512<<10)
			if atSmall != atLarge {
				t.Errorf("%s cost %d steps over 8 KiB and %d over 512 KiB; a length "+
					"mismatch answers without reading either payload", expr, atSmall, atLarge)
			}
		})
	}
}

// A nested string compared with a nested symbol is rejected on kind before
// either name is read, so the mixed pair stays free through a container.
func TestNestedMixedKindComparisonIsNotCharged(t *testing.T) {
	t.Parallel()

	expr := "([s] == [s.to_sym]).to_s.length"
	atSmall := minStepsForStringOp(t, expr, 8<<10)
	atLarge := minStepsForStringOp(t, expr, 512<<10)
	if atSmall != atLarge {
		t.Errorf("%s cost %d steps over 8 KiB and %d over 512 KiB; a kind mismatch "+
			"answers without reading either name", expr, atSmall, atLarge)
	}
}

// The equality walk's visited set walks each distinct pair once, so a shared
// DAG with big string leaves is charged once per distinct pair rather than
// once per path: metering must not reintroduce the exponential walk the
// ordering memo guards against.
func TestSharedDAGEqualityWithChargingStaysLinear(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: 64 << 20}, sharedDAGSource+`
    def run(s)
      a = [build(24), s]
      b = [build(24), s]
      ((a == b) && ((a <=> b) == 0)).to_s
    end
    `)
	payload := NewString(strings.Repeat("ab", 2048))
	got, err := script.Call(context.Background(), "run", []Value{payload}, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "true" {
		t.Fatalf("run() = %q, want %q", got.String(), "true")
	}
}

// The new byte charge raises the same step-quota error the per-element charge
// does, and the ordering members must keep routing it through their sandbox
// translation rather than relabelling it "values are not comparable".
func TestOrderingMembersPreserveByteChargeErrors(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: 64 << 20}, `
    def run(rows)
      begin
        rows.sort.length
      rescue => e
        "rescued"
      end
    end
    `)
	rows := make([]Value, 8)
	for i := range rows {
		rows[i] = NewArray([]Value{NewString(strings.Repeat("ab", 4096)), NewInt(int64(i))})
	}
	_, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{})
	if err == nil {
		t.Fatal("the byte charge's quota error was caught by rescue: a sandbox limit must stay uncatchable")
	}
	if !strings.Contains(err.Error(), "step quota") {
		t.Fatalf("error = %v, want the step-quota error rather than an incomparability message", err)
	}
}
