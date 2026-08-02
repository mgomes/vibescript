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
		"h = {}\n  h[[s]] = 1\n  h.merge({a: 1}).length",
		"h = {}\n  h[[s]] = 1\n  h.merge({a: 1}) { |k, a, b| a }.length",
		"[1, 2].uniq { |x| s }.length",
		"[[s, s], [s]].reduce(:&).length",
		"[[s, s], [s]].reduce(:-).length",
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

// minStepsForKeyOp is minStepsForStringOp with a search ceiling sized for
// key-canonicalization costs, which scale past the payload by the ancestor
// copies each nesting level performs.
func minStepsForKeyOp(t *testing.T, expr string, bytes int) int {
	t.Helper()

	hay := NewString(strings.Repeat("ab", bytes/2))
	src := fmt.Sprintf("def run(s)\n  %s\nend", expr)

	lo, hi := 1, 1<<21
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// HashKey copies the complete child encoding into every ancestor's canonical
// string, so a depth-d single-child chain around one string costs Θ(d·len);
// a leaf-only charge let deep linear keys do unbounded copying under a flat
// budget.
func TestDeepArrayKeyChargesPerLevel(t *testing.T) {
	t.Parallel()

	keyAtDepth := func(depth int) string {
		return "k = [s]\n  j = 0\n  while j < " + fmt.Sprint(depth) + "\n    k = [k]\n    j = j + 1\n  end\n  h = {}\n  h[k] = 1\n  h.length"
	}
	atShallow := minStepsForKeyOp(t, keyAtDepth(6), 8<<10)
	atDeep := minStepsForKeyOp(t, keyAtDepth(18), 8<<10)
	// Tripling the depth roughly triples the ancestor copies; require 2x to
	// track the class with headroom.
	if atDeep < atShallow*2 {
		t.Errorf("deep key cost %d steps at depth 6 and %d at depth 18; every "+
			"ancestor copies the child encoding, so the charge must scale with depth", atShallow, atDeep)
	}
}

// The tally capacity sampler canonicalizes the leading elements of a large
// blockless receiver; its key charge must land before that work and surface
// quota errors as quota errors, not as unsupported-key failures.
func TestTallySamplerChargesBeforeCanonicalizing(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: Unlimited}, `
    def run(s)
      a = [[s]]
      j = 0
      while j < 300
        a << 1
        j = j + 1
      end
      a.tally.length
    end
    `)
	hay := NewString(strings.Repeat("ab", 1<<15))
	_, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{})
	if err == nil {
		t.Fatal("expected the sampling charge to trip the quota")
	}
	requireErrorContains(t, err, "quota exceeded")
	if strings.Contains(err.Error(), "unsupported hash key") {
		t.Fatalf("quota error mislabeled as unsupported key: %v", err)
	}
}

// Canonicalization stops at the first unsupported element, never reading
// what follows, and an argumentless difference only shallow-copies — both
// must stay flat-cost however large the string after them is.
func TestKeyChargeStopsWhereCanonicalizationStops(t *testing.T) {
	t.Parallel()

	exprs := []string{
		"h = {}\n  r = begin\n    h[[{a: 1}, s]] = 1\n  rescue => e\n    1\n  end\n  r",
		"[s].difference.length",
		"[s].difference([]).length",
		"([s] & []).length",
		"([s] - []).length",
		"([s] & [[0]]).length",
		"([s] - [[0]]).length",
		"[s].difference([[0]]).length",
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsForStringOp(t, expr, 8<<10)
			atLarge := minStepsForStringOp(t, expr, 512<<10)
			if atSmall != atLarge {
				t.Errorf("%q cost %d steps over 8 KiB and %d over 512 KiB; work that "+
					"never reads the payload must not be charged for it", expr, atSmall, atLarge)
			}
		})
	}
}

// An unsupported array key must still fail with the canonicalization error,
// not a quota error manufactured by the charge walking past it.
func TestUnsupportedArrayKeyKeepsItsError(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 500, MemoryQuotaBytes: Unlimited}, `
    def run(s)
      h = {}
      h[[{a: 1}, s]] = 1
    end
    `)
	hay := NewString(strings.Repeat("ab", 1<<19))
	_, err := script.Call(context.Background(), "run", []Value{hay}, CallOptions{})
	if err == nil {
		t.Fatal("an unsupported hash key must error")
	}
	requireErrorContains(t, err, "unsupported hash key type")
}

// A nested reslice shares its parent's starting pointer but is a distinct
// key canonicalization copies in full; a pointer-only cycle guard misread it
// as a cycle and stopped charging. The guard keys on the full slice header,
// so the shared payload is billed once per occurrence.
func TestOverlappingResliceKeyIsChargedPerOccurrence(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("ab", 32<<10)
	elems := make([]Value, 2)
	elems[0] = NewString(payload)
	elems[1] = NewArray(elems[:1])
	key := NewArray(elems)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	if err := exec.chargeValueKeySteps(key); err != nil {
		t.Fatalf("chargeValueKeySteps: %v", err)
	}
	// The payload appears once directly and once through the reslice; both
	// occurrences must be billed at the scan rate.
	if want := 2 * len(payload) / 64; exec.steps < want {
		t.Fatalf("charged %d steps, want at least %d (both occurrences of the shared payload)", exec.steps, want)
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
	// The per-level encoding copies push the true cost well past
	// minStepsForStringOp's payload-sized search ceiling, so search wider.
	atShallow := minStepsForKeyOp(t, keyAtDepth(4), 8<<10)
	atDeep := minStepsForKeyOp(t, keyAtDepth(6), 8<<10)
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

// A metered legacy value? scan must be deterministic: under a quota covering
// one long comparison but not two, randomized map iteration alternated
// between a result and a quota error on identical inputs.
func TestLegacyHashValueScanIsDeterministic(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 8192)
	almost := strings.Repeat("x", 8191) + "y"
	receiver := NewHash(map[string]Value{
		"a": NewString(long),
		"b": NewString(almost),
	})

	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run(h, needle)
      h.value?(needle)
    end
    `)

	var firstOutcome string
	for i := range 50 {
		_, err := script.Call(context.Background(), "run", []Value{receiver, NewString(long)}, CallOptions{})
		outcome := "ok"
		if err != nil {
			outcome = "err"
		}
		if i == 0 {
			firstOutcome = outcome
			continue
		}
		if outcome != firstOutcome {
			t.Fatalf("run %d produced %q, run 0 produced %q; identical inputs under one quota must not alternate", i, outcome, firstOutcome)
		}
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
