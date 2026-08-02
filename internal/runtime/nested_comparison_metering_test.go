package runtime

import (
	"context"
	"fmt"
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
		"([[s]] & [[s]]).length",
		"([[s]] - [[s]]).length",
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

// An array hash key canonicalizes recursively — its nested strings are copied
// and hashed in full — and a scalar key a uniq block yields is hashed into
// the set's Go map, so both must charge like a direct string key.
func TestCompositeAndYieldedKeysChargeForTheirPayloads(t *testing.T) {
	t.Parallel()

	exprs := []string{
		"h = {}\n  h[[s]] = 1\n  h.length",
		"h = {}\n  h[[[s], [s]]] = 1\n  h.length",
		"[1, 2].uniq { |x| s }.length",
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 64<<10)
			if atLarge < atSmall*4 {
				t.Errorf("%q cost %d steps over 8 KiB and %d over 64 KiB; key "+
					"canonicalization must charge the payload it reads", expr, atSmall, atLarge)
			}
		})
	}
}

// HashKey canonicalizes a shared subtree once per occurrence — [a, a] copies
// a twice — so the key charge must scale with the occurrence count, not the
// distinct-backing count: a permanent visited set billed a shared DAG once
// while canonicalization copied it exponentially.
func TestSharedDAGHashKeyChargesPerOccurrence(t *testing.T) {
	t.Parallel()

	keyAtDepth := func(depth int) string {
		return "k = [s]\n  j = 0\n  while j < " + fmt.Sprint(depth) + "\n    k = [k, k]\n    j = j + 1\n  end\n  h = {}\n  h[k] = 1\n  h.length"
	}
	atShallow := minStepsForStringOp(t, keyAtDepth(4), 8<<10)
	atDeep := minStepsForStringOp(t, keyAtDepth(6), 8<<10)
	// Depth 6 holds four times the leaf occurrences of depth 4, so the charge
	// must grow by at least 3x; a distinct-backing charge stays flat.
	if atDeep < atShallow*3 {
		t.Errorf("shared-DAG key cost %d steps at depth 4 and %d at depth 6; "+
			"canonicalization copies each occurrence, so the charge must too", atShallow, atDeep)
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
