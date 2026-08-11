package runtime

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// TestEmptyStringKeyFramingIsCharged pins the framing term of the key cost
// model: HashKey encodes an empty string as nonempty kind-and-length framing
// that the parent copies and hashes, so an array key holding thousands of
// empty strings must not canonicalize for free.
func TestEmptyStringKeyFramingIsCharged(t *testing.T) {
	t.Parallel()

	elems := make([]Value, 2048)
	for i := range elems {
		elems[i] = NewString("")
	}
	budget := valueKeyCostNodeBudget
	_, charge, enc, ok := valueKeyCanonicalizationCost(NewArray(elems), nil, &budget)
	if !ok {
		t.Fatal("an array of empty strings must be walkable")
	}
	if want := 2048 * 16; charge < want {
		t.Fatalf("charge = %d, want at least the copied framing's %d", charge, want)
	}
	if want := 2048 * 16; enc < want {
		t.Fatalf("enc = %d, want at least the framing's %d", enc, want)
	}
}

// TestFailedKeyCanonicalizationChargesPrefixCopy pins the prefix half of the
// failed-canonicalization cost model. HashKey writes each element's encoding
// into the level's builder as it goes, so a level that later meets an
// unsupported element has already copied its whole prefix. Payload-free prefix
// elements make the gap visible: integers charge nothing of their own, so a
// level that dropped its copy modeled zero bytes for a canonicalization
// proportional to the prefix.
func TestFailedKeyCanonicalizationChargesPrefixCopy(t *testing.T) {
	t.Parallel()

	cost := func(prefix int) int {
		elems := make([]Value, prefix, prefix+1)
		for i := range elems {
			elems[i] = NewInt(int64(i))
		}
		elems = append(elems, NewObject(nil))
		budget := valueKeyCostNodeBudget
		_, charge, enc, ok := valueKeyCanonicalizationCost(NewArray(elems), nil, &budget)
		if ok {
			t.Fatal("an array holding an object must not be walkable")
		}
		if enc != 0 {
			t.Fatalf("enc = %d, want 0: a discarded level hands no encoding to its parent", enc)
		}
		return charge
	}

	const short, long = 16, 4096
	atShort, atLong := cost(short), cost(long)
	if atLong <= atShort {
		t.Fatalf("modeled %d bytes for a %d-element prefix and %d for a %d-element one; "+
			"the prefix copy HashKey performs must scale with the prefix",
			atLong, long, atShort, short)
	}
	if want := long * 8; atLong < want {
		t.Fatalf("modeled %d bytes for a %d-element prefix, want at least %d: every "+
			"element before the unsupported one is encoded and copied into the builder",
			atLong, long, want)
	}
}

// TestEqualSkipsFlagChargeOnSourceMismatch pins the order of the regex
// identity charges: a source-length mismatch answers false without either
// flag string being read, so huge host-provided flags must not turn the
// constant-time answer into a quota error.
func TestEqualSkipsFlagChargeOnSourceMismatch(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited},
		"def run(a, b)\n  a.equal?(b).to_s.length\nend")
	flags := strings.Repeat("f", 1<<20)
	a := NewRegex(value.Regex{Source: "ab", Flags: flags})
	b := NewRegex(value.Regex{Source: "abc", Flags: strings.Clone(flags)})
	if _, err := script.Call(context.Background(), "run", []Value{a, b}, CallOptions{}); err != nil {
		t.Fatalf("a source-length mismatch must answer without reading the flags: %v", err)
	}
}

// TestSetOpsReserveTheirBuffers pins the loop-scratch reservation for the
// set helpers' Go-local buffers: the result slice, distinct-composite slice,
// and scalar maps grow with the input yet are invisible to the estimator's
// base walk, so an operation under a quota too small for them must fail its
// reservation instead of allocating unmetered.
func TestSetOpsReserveTheirBuffers(t *testing.T) {
	t.Parallel()

	composite := func() Value { return NewArray([]Value{NewString("payload")}) }
	ops := map[string]func(exec *Execution) error{
		"union": func(exec *Execution) error {
			_, err := unionArrayValues(exec, []Value{composite()}, [][]Value{{composite()}})
			return err
		},
		"difference": func(exec *Execution) error {
			_, err := differenceArrayValues(exec, []Value{composite()}, [][]Value{{composite()}})
			return err
		},
		"intersect": func(exec *Execution) error {
			_, err := intersectArrayValues(exec, []Value{composite()}, []Value{composite()})
			return err
		},
		"subtract": func(exec *Execution) error {
			_, err := subtractArrayValues(exec, []Value{composite()}, []Value{composite()})
			return err
		},
		"unique": func(exec *Execution) error {
			_, err := uniqueValuesMetered([]Value{composite(), composite()}, nil, nil, exec)
			return err
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background(), memoryQuota: 1}
			if err := op(exec); err == nil {
				t.Fatal("a set operation under a 1-byte quota must fail its buffer reservation")
			}
		})
	}
}

// TestSetOpsValidateBuffersWithOperands pins the roots side of the buffer
// reservation: the operands can be host-returned arrays live only in Go
// locals, so a quota that admits the buffers against the execution roots
// alone must still reject the operation when the operands' own graphs push
// the true peak past it.
func TestSetOpsValidateBuffersWithOperands(t *testing.T) {
	t.Parallel()

	heavy := func() []Value {
		return []Value{NewArray([]Value{NewString(strings.Repeat("x", 4096))})}
	}
	// The buffers alone fit comfortably; the two 4 KiB operand graphs do not.
	exec := &Execution{ctx: context.Background(), memoryQuota: 2048}
	if _, err := unionArrayValues(exec, heavy(), [][]Value{heavy()}); err == nil {
		t.Fatal("union must validate its buffers together with the operand graphs")
	}
}

// TestEqualityByteChargeCarriesSubStepRemainder pins the remainder carry in
// the equality byte charge: rounding each invocation down separately let a
// probe loop flush a sub-step tail per candidate and never bill the
// aggregate, so a set operation could compare hundreds of small composites
// in quadratic time for zero steps.
func TestEqualityByteChargeCarriesSubStepRemainder(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1000}
	charge := exec.stringScanChargeFunc()
	for range 8 {
		if err := charge(63); err != nil {
			t.Fatalf("charge failed inside the quota: %v", err)
		}
	}
	// 8 tails of 63 bytes accumulate to 504 bytes: 7 whole steps with 56
	// bytes carried forward. Per-invocation rounding would have billed none.
	if exec.steps != 7 {
		t.Fatalf("steps = %d, want 7 from the carried remainder", exec.steps)
	}
}

// TestSetProbeTailsAccumulateAcrossCandidates pins the end-to-end effect: a
// uniq over distinct one-element arrays holding equal-length sub-step strings
// performs a quadratic scan whose per-probe byte tails each round to free, so
// only the carried remainder makes the aggregate reachable by the step quota.
func TestSetProbeTailsAccumulateAcrossCandidates(t *testing.T) {
	t.Parallel()

	values := make([]Value, 64)
	for i := range values {
		values[i] = NewArray([]Value{NewString(fmt.Sprintf("%063d", i))})
	}
	src := "def run(values)\n  values.uniq.length\nend"
	// 64 distinct composites cost 2016 probe steps; the probes read 63 bytes
	// per candidate pair, which is roughly another 1984 steps only when the
	// sub-step tails accumulate. The low quota covers the probe steps but not
	// the carried byte charges; the high quota comfortably covers both.
	script := compileScriptWithConfig(t, Config{StepQuota: 2800, MemoryQuotaBytes: Unlimited}, src)
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(values)}, CallOptions{}); err == nil {
		t.Fatal("sub-step probe tails must accumulate into billed steps")
	}
	script = compileScriptWithConfig(t, Config{StepQuota: 22400, MemoryQuotaBytes: Unlimited}, src)
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(values)}, CallOptions{}); err != nil {
		t.Fatalf("eight times the quota must cover the accumulated tails: %v", err)
	}
}

// TestSetOpsReserveTheRootWrapperSlice pins the roots-side scratch: the
// wrapper slice newSetOpScratch materializes grows with the operation's
// arity, and a high-arity call over empty sources never reserves anything
// else, so the constructor's own validation is the only check that can see
// the backing before it allocates unmetered.
func TestSetOpsReserveTheRootWrapperSlice(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), memoryQuota: 4096}
	sources := make([][]Value, 1<<14)
	if _, err := unionArrayValues(exec, nil, sources); err == nil {
		t.Fatal("a high-arity union must fail the root wrapper reservation")
	}
	if _, err := differenceArrayValues(exec, nil, sources); err == nil {
		t.Fatal("a high-arity difference must fail the root wrapper reservation")
	}
}

// TestSetOpsReserveTheHintedScalarMap pins the up-front scalar-map
// reservation: make preallocates the whole hinted bucket array, so an input
// whose distinct scalars would individually fit the quota must still fail
// before the buckets are allocated when the hinted capacity does not.
func TestSetOpsReserveTheHintedScalarMap(t *testing.T) {
	t.Parallel()

	// 4096 elements cycling ten distinct values: the old per-entry
	// accounting reserved ten entries while make silently allocated the
	// full 4096-capacity bucket array.
	values := make([]Value, 4096)
	for i := range values {
		values[i] = NewInt(int64(i % 10))
	}
	exec := &Execution{ctx: context.Background(), memoryQuota: 64 << 10}
	if _, err := uniqueValuesMetered(values, nil, nil, exec); err == nil {
		t.Fatal("the hinted scalar map must be reserved before it is allocated")
	}
	exec = &Execution{ctx: context.Background(), memoryQuota: 8 << 20}
	unique, err := uniqueValuesMetered(values, nil, nil, exec)
	if err != nil {
		t.Fatalf("a quota covering the hinted map must pass: %v", err)
	}
	if len(unique) != 10 {
		t.Fatalf("uniq kept %d values, want 10", len(unique))
	}
}

// TestEqualityScanStopsAfterStickyError pins the scan abort: once a probe
// records a sticky charge failure, the remaining candidates must not be
// visited — every later probe answers false in O(1), so finishing the scan
// is arbitrarily much post-quota work.
func TestEqualityScanStopsAfterStickyError(t *testing.T) {
	t.Parallel()

	values := make([]Value, 100)
	for i := range values {
		values[i] = NewArray([]Value{NewString(strings.Repeat("x", 128))})
	}
	var equality EqualityContext
	boom := errors.New("quota spent")
	equality.SetCharge(func(int) error { return boom })
	probes, found := probeEqualValue(values, NewArray([]Value{NewString(strings.Repeat("x", 128))}), &equality)
	if found {
		t.Fatal("a failed charge must not report a match")
	}
	if probes != 1 {
		t.Fatalf("scan performed %d probes after the sticky error, want 1", probes)
	}
	if !errors.Is(equality.Err(), boom) {
		t.Fatalf("Err() = %v, want %v", equality.Err(), boom)
	}
}

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

// A typed receiver copy — except's retained entries, remap_keys' unmapped
// keys, and the keep-everything transforms — canonicalizes each receiver key
// into the result hash, so the key payload must be charged like a direct
// insertion. The insertion that builds the hash charges the payload too, so
// each operation's own cost is isolated by differencing against the
// build-only baseline; uncharged, `h.except` copied arbitrarily large array
// keys under a flat per-entry step.
func TestTypedReceiverCopiesChargeKeyPayloads(t *testing.T) {
	t.Parallel()

	const build = "h = {}\n  h[[s]] = 1\n  "
	ops := []string{
		"h.except.length",
		"h.remap_keys({}).length",
		"h.select { |k, v| true }.length",
		"h.reject { |k, v| false }.length",
		"h.transform_values { |v| v }.length",
		"h.compact.length",
		"x = {}\n  x.replace(h).length",
	}
	baseSmall := minStepsForKeyOp(t, build+"h.length", 8<<10)
	baseLarge := minStepsForKeyOp(t, build+"h.length", 64<<10)
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			deltaSmall := minStepsForKeyOp(t, build+op, 8<<10) - baseSmall
			deltaLarge := minStepsForKeyOp(t, build+op, 64<<10) - baseLarge
			if deltaLarge < deltaSmall*4 {
				t.Errorf("%q cost %d extra steps over 8 KiB and %d over 64 KiB beyond "+
					"the build baseline; the receiver copy must charge each key's "+
					"canonicalization", op, deltaSmall, deltaLarge)
			}
		})
	}
}

// A key that fails canonicalization partway — a long string followed by an
// unsupported element — stops HashKey at the failure, so no ancestor copies
// the partial child encoding. The charge for the completed prefix must not
// grow with nesting depth, or a quota sized for the actual work would
// replace the expected miss with a quota error. slice treats the failing
// candidate as a miss, so the probe completes and isolates the charge: the
// fixed-depth ratio pins that the prefix is charged at all, and the depth
// ratio pins that the failed encoding never reaches an ancestor.
func TestFailedKeyCanonicalizationChargesOnlyThePrefix(t *testing.T) {
	t.Parallel()

	keyAtDepth := func(depth int) string {
		return "k = [s, {}]\n  j = 0\n  while j < " + fmt.Sprint(depth) + "\n    k = [k]\n    j = j + 1\n  end\n  h = {a: 1}\n  h.slice(k).length"
	}
	atSmall := minStepsForKeyOp(t, keyAtDepth(6), 8<<10)
	atLarge := minStepsForKeyOp(t, keyAtDepth(6), 64<<10)
	if atLarge < atSmall*4 {
		t.Errorf("failing candidate cost %d steps over 8 KiB and %d over 64 KiB; the "+
			"prefix HashKey reads must be charged", atSmall, atLarge)
	}
	atDeep := minStepsForKeyOp(t, keyAtDepth(18), 64<<10)
	if atDeep >= atLarge*2 {
		t.Errorf("failing candidate cost %d steps at depth 6 and %d at depth 18; "+
			"ancestors never copy a failed child encoding, so the charge must not "+
			"scale with depth", atLarge, atDeep)
	}
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

// A composite-only lookup side means no scalar probe can ever match, so a
// big-integer receiver element must not be canonicalized — its hexadecimal
// conversion is the work the charge exists to bound — and the operation
// completes under a tiny quota.
func TestCompositeOnlyLookupSkipsBigIntCanonicalization(t *testing.T) {
	t.Parallel()

	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(20000), nil)
	arr := NewArray([]Value{newBigIntValue(huge)})
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: Unlimited}, `
    def run(a)
      (a & [[0]]).length
    end
    `)
	got, err := script.Call(context.Background(), "run", []Value{arr}, CallOptions{})
	if err != nil {
		t.Fatalf("a composite-only intersection canonicalized the untouched bigint: %v", err)
	}
	if got.Int() != 0 {
		t.Fatalf("intersection = %d, want 0", got.Int())
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
// TestAbortableKeySortStopsOnFailedCharge pins the sort abort: once the
// comparator reports a failed charge, slices.SortFunc must not keep
// comparing — finishing the sort would be O(n log n) post-quota work whose
// order the caller discards with the error.
func TestAbortableKeySortStopsOnFailedCharge(t *testing.T) {
	t.Parallel()

	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("%08d", i)
	}
	calls := 0
	abortableKeySort(keys, func(a, b string) (int, bool) {
		calls++
		if calls >= 5 {
			return 0, false
		}
		return strings.Compare(a, b), true
	})
	if calls != 5 {
		t.Fatalf("comparator ran %d times, want the abort to stop it at 5", calls)
	}
}

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
