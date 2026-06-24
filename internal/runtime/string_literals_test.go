package runtime

import (
	"context"
	"errors"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestSingleQuotedStringLiteralExecution(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run
  'hello'
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.String() != "hello" {
		t.Fatalf("Call(run) = %q, want %q", got.String(), "hello")
	}
}

func TestSingleQuotedStringEscapes(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def quote
  'don\'t'
end

def newline_text
  'a\nb'
end`)

	quote := callScript(t, context.Background(), script, "quote", nil, CallOptions{})
	if quote.String() != "don't" {
		t.Fatalf("Call(quote) = %q, want %q", quote.String(), "don't")
	}
	newlineText := callScript(t, context.Background(), script, "newline_text", nil, CallOptions{})
	if newlineText.String() != `a\nb` {
		t.Fatalf("Call(newline_text) = %q, want %q", newlineText.String(), `a\nb`)
	}
}

func TestDoubleQuotedStringInterpolationExecution(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def interpolated
  name = "Ada"
  score = 2
  "hi #{name}, score #{score + 1}, active #{true}"
end

def escaped_marker
  name = "Ada"
  "\#{name}"
end

def single_quoted_marker
  name = "Ada"
  'hi #{name}'
end`)

	got := callScript(t, context.Background(), script, "interpolated", nil, CallOptions{})
	if got.String() != "hi Ada, score 3, active true" {
		t.Fatalf("Call(interpolated) = %q, want %q", got.String(), "hi Ada, score 3, active true")
	}
	escaped := callScript(t, context.Background(), script, "escaped_marker", nil, CallOptions{})
	if escaped.String() != "#{name}" {
		t.Fatalf("Call(escaped_marker) = %q, want %q", escaped.String(), "#{name}")
	}
	singleQuoted := callScript(t, context.Background(), script, "single_quoted_marker", nil, CallOptions{})
	if singleQuoted.String() != "hi #{name}" {
		t.Fatalf("Call(single_quoted_marker) = %q, want %q", singleQuoted.String(), "hi #{name}")
	}
}

// newInterpolatedTextLiteral builds an InterpolatedString made entirely of
// literal text parts. Splitting one large string across many parts lets a test
// observe the per-chunk containment checks without depending on parser
// chunking.
func newInterpolatedTextLiteral(parts ...string) *InterpolatedString {
	lit := &InterpolatedString{Parts: make([]StringPart, len(parts))}
	for i, part := range parts {
		lit.Parts[i] = StringText{Text: part}
	}
	return lit
}

func TestEvalInterpolatedStringLiteralBoundsMaterialization(t *testing.T) {
	t.Parallel()

	const chunk = "0123456789abcdef" // 16 bytes
	const chunkCount = 64            // 1 KiB of literal text if fully built

	t.Run("rejects growth past memory quota mid build", func(t *testing.T) {
		t.Parallel()

		parts := make([]string, chunkCount)
		for i := range parts {
			parts[i] = chunk
		}
		lit := newInterpolatedTextLiteral(parts...)

		// A quota far below the fully built result must reject the
		// materialization. With ample steps the failure can only be the
		// projected memory check tripping while the builder grows, not the step
		// quota.
		exec := &Execution{
			ctx:         context.Background(),
			quota:       1 << 20,
			memoryQuota: 256,
		}
		env := newEnv(nil)
		exec.pushEnv(env)

		_, err := exec.evalInterpolatedStringLiteral(lit, env)
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	})

	t.Run("small interpolation stays under an ample quota", func(t *testing.T) {
		t.Parallel()

		lit := newInterpolatedTextLiteral("hi ", chunk)
		exec := &Execution{
			ctx:         context.Background(),
			quota:       1 << 20,
			memoryQuota: 1 << 20,
		}
		env := newEnv(nil)
		exec.pushEnv(env)

		got, err := exec.evalInterpolatedStringLiteral(lit, env)
		if err != nil {
			t.Fatalf("evalInterpolatedStringLiteral: %v", err)
		}
		if want := "hi " + chunk; got.String() != want {
			t.Fatalf("result = %q, want %q", got.String(), want)
		}
	})
}

func TestEvalInterpolatedStringLiteralChargesBuilderGrowthAfterPrefix(t *testing.T) {
	t.Parallel()

	// Builder.Grow does not reserve exactly the requested bytes once the current
	// backing is exhausted: it reallocates to roughly 2*cap+n. Appending a large
	// value after a prior large segment therefore reserves a backing array far
	// larger than the running length plus the payload. A projection keyed on
	// sb.Len()+payload would charge only the smaller final length and let the real
	// reservation escape the memory quota; the projection must charge the backing
	// the builder actually allocates.
	//
	// The two segments are sized so the second append's reserved capacity (the
	// doubled term) exceeds the quota while the running-length-plus-payload sum
	// stays under it. With the fixed projection the second append trips the quota.
	const segmentBytes = 5000

	segment := strings.Repeat("a", segmentBytes)
	lit := newInterpolatedTextLiteral(segment, segment)

	// projectedBuilderCap reproduces Grow's reallocation for the second segment.
	// Run the first segment exactly as the evaluator would, then read the
	// projected reservation and the smaller sum a length-keyed projection would
	// have charged, so the quota can be pinned strictly between them.
	var probe strings.Builder
	probe.Grow(segmentBytes)
	probe.WriteString(segment)
	doubledProjection := projectedBuilderCap(&probe, segmentBytes)
	lengthProjection := probe.Len() + segmentBytes
	if doubledProjection <= lengthProjection {
		t.Fatalf("doubled projection %d must exceed length projection %d for the test to isolate the growth charge",
			doubledProjection, lengthProjection)
	}

	base := func() int {
		exec := &Execution{
			ctx:         context.Background(),
			quota:       1 << 20,
			memoryQuota: 1 << 20,
		}
		env := newEnv(nil)
		exec.pushEnv(env)
		return exec.estimateMemoryUsageBase(exec.memoryEstimatorForCheck())
	}()

	// checkProjectedStringBytes also charges a value header on top of the
	// projection, so fold that into the floor and ceiling the quota sits between.
	const header = estimatedValueBytes + estimatedStringHeaderBytes
	floor := base + header + lengthProjection
	ceiling := base + header + doubledProjection
	quota := (floor + ceiling) / 2
	if quota <= floor || quota >= ceiling {
		t.Fatalf("quota %d must fall strictly between %d and %d", quota, floor, ceiling)
	}

	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: quota,
	}
	env := newEnv(nil)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestInterpolatedStringValueGrowthAfterPrefixTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// End-to-end mirror of the builder-growth charge: a script interpolates a
	// large value after a large prefix. The second append reallocates the builder
	// to roughly 2*cap+payload (Grow's doubling), so the reserved backing exceeds
	// the running length plus the payload. A projection keyed on running length
	// would let that reservation escape the quota; with the fixed projection the
	// second append trips it.
	//
	// Both segments arrive as arguments so they are already counted in the base,
	// isolating the failure to the builder reservation made during interpolation
	// rather than to constructing the segments.
	const segmentBytes = 6000

	source := `def run(prefix, value)
  "#{prefix}#{value}"
end`
	prefix := NewString(strings.Repeat("p", segmentBytes))
	value := NewString(strings.Repeat("v", segmentBytes))

	// Reproduce the projection the second append performs so the quota can be
	// pinned strictly between the length-keyed charge (which a projection ignoring
	// Grow's doubling would make) and the doubled charge the fixed code makes.
	exec := &Execution{ctx: context.Background(), quota: 1 << 20, memoryQuota: 1 << 20}
	env := newEnv(nil)
	env.Define("prefix", prefix)
	env.Define("value", value)
	exec.pushEnv(env)
	base := exec.estimateMemoryUsageBase(exec.memoryEstimatorForCheck())

	var probe strings.Builder
	probe.Grow(segmentBytes)
	probe.WriteString(prefix.String())
	doubledProjection := projectedBuilderCap(&probe, segmentBytes)
	lengthProjection := probe.Len() + segmentBytes

	const header = estimatedValueBytes + estimatedStringHeaderBytes
	floor := base + header + lengthProjection
	ceiling := base + header + doubledProjection
	quota := (floor + ceiling) / 2
	if quota <= floor || quota >= ceiling {
		t.Fatalf("quota %d must fall strictly between %d and %d", quota, floor, ceiling)
	}

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: quota}, source)
	requireRunMemoryQuotaError(t, script, []Value{prefix, value}, CallOptions{})
}

func TestEvalInterpolatedStringLiteralBoundsAggregateRendering(t *testing.T) {
	t.Parallel()

	// An aggregate whose String rendering expands far beyond its own footprint:
	// a short array holding many references to one large string. The array fits
	// the memory quota, but rendering it repeats the large string once per
	// element, materializing a representation many times the quota. The
	// projected check must reject the interpolation from Value.StringByteLen
	// without first allocating the oversized rendering.
	const elementBytes = 4096
	const elementCount = 64

	large := NewString(strings.Repeat("x", elementBytes))
	elems := make([]Value, elementCount)
	for i := range elems {
		elems[i] = large
	}
	arr := NewArray(elems)

	lit := &InterpolatedString{Parts: []StringPart{StringExpr{Expr: &Identifier{Name: "arr"}}}}

	// The quota comfortably holds the array (one large string plus element
	// headers) but is far below the rendered representation, which repeats the
	// large string in every element.
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: elementBytes * 8,
	}
	env := newEnv(nil)
	env.Define("arr", arr)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestEvalInterpolatedStringLiteralBoundsTemporaryAggregate(t *testing.T) {
	t.Parallel()

	// An inline array literal of distinct large strings produces a temporary
	// aggregate that no environment holds: it lives only on the Go call stack
	// while WriteStringTo copies its rendering. Its own footprint and its rendered
	// output are both ~elementBytes*elementCount, so a quota set above either one
	// alone but below their sum must trip. A projection that charged only the
	// env-reachable base plus the output (omitting the temporary's footprint)
	// would let this stream past the limit.
	const elementBytes = 4096
	const elementCount = 16

	elements := make([]Expression, elementCount)
	for i := range elements {
		elements[i] = &StringLiteral{Value: strings.Repeat("x", elementBytes)}
	}
	lit := &InterpolatedString{Parts: []StringPart{
		StringExpr{Expr: &ArrayLiteral{Elements: elements}},
	}}

	// Size the quota above the rendered output alone (and above the temporary
	// alone) but below base+temporary+output. Each is roughly one full copy of the
	// distinct strings, so their sum is ~2x either one.
	oneCopy := elementBytes * elementCount
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: oneCopy + oneCopy/2,
	}
	env := newEnv(nil)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestEvalInterpolatedStringLiteralBoundsTemporaryHash(t *testing.T) {
	t.Parallel()

	// Same temporary-footprint concern as the array case, but for an inline hash
	// literal whose values are distinct large strings. The hash is never bound to
	// an environment, so only charging the rendered output would let its own
	// footprint slip past the quota during the streamed write.
	const valueBytes = 4096
	const entryCount = 16

	pairs := make([]HashPair, entryCount)
	for i := range pairs {
		pairs[i] = HashPair{
			Key:   &StringLiteral{Value: string(rune('a' + i))},
			Value: &StringLiteral{Value: strings.Repeat("y", valueBytes)},
		}
	}
	lit := &InterpolatedString{Parts: []StringPart{
		StringExpr{Expr: &HashLiteral{Pairs: pairs}},
	}}

	oneCopy := valueBytes * entryCount
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: oneCopy + oneCopy/2,
	}
	env := newEnv(nil)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestInterpolatedStringTemporaryFunctionReturnTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A function returns a large string that is interpolated directly. The return
	// value is a temporary held on the Go call stack, not reachable from any
	// environment, so the env-reachable base does not include it. Both the
	// returned string and the interpolated output are one copy of the large
	// string; the quota holds either alone but not both live at once, so the
	// interpolation must trip the memory quota rather than streaming the temporary
	// past the limit.
	source := `def big(n)
  "".ljust(n, "z")
end

def run(n)
  "#{big(n)}"
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 48 * 1024}, source)
	requireRunMemoryQuotaError(t, script, []Value{NewInt(40 * 1024)}, CallOptions{})
}

func TestEvalInterpolatedStringLiteralAggregateRendersUnderAmpleQuota(t *testing.T) {
	t.Parallel()

	// The projection must not reject a rendering that fits the quota, and the
	// materialized result must match Value.String exactly.
	arr := NewArray([]Value{NewInt(1), NewString("two"), NewArray([]Value{NewBool(true)})})
	lit := &InterpolatedString{Parts: []StringPart{
		StringText{Text: "values: "},
		StringExpr{Expr: &Identifier{Name: "arr"}},
	}}

	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: 1 << 20,
	}
	env := newEnv(nil)
	env.Define("arr", arr)
	exec.pushEnv(env)

	got, err := exec.evalInterpolatedStringLiteral(lit, env)
	if err != nil {
		t.Fatalf("evalInterpolatedStringLiteral: %v", err)
	}
	if want := "values: " + arr.String(); got.String() != want {
		t.Fatalf("result = %q, want %q", got.String(), want)
	}
}

func TestEvalInterpolatedStringLiteralStreamsValueWithoutSecondCopy(t *testing.T) {
	// Not parallel: this reads process-wide allocation counters and must not race
	// other goroutines' allocations.

	// An aggregate whose rendering expands far beyond its own footprint: a short
	// array holding many references to one large string. The value fits the memory
	// quota and its projected rendering (sb.Len + StringByteLen) passes, so
	// execution reaches the materialization. A renderer that first built the full
	// string and then copied it into the builder would transiently hold both the
	// temporary rendering and the builder copy, peaking near twice the rendered
	// size and defeating a quota set close to the final output. Streaming the
	// value directly into the builder keeps the peak to a single rendering.
	const elementBytes = 8192
	const elementCount = 64

	large := NewString(strings.Repeat("x", elementBytes))
	elems := make([]Value, elementCount)
	for i := range elems {
		elems[i] = large
	}
	arr := NewArray(elems)

	lit := &InterpolatedString{Parts: []StringPart{StringExpr{Expr: &Identifier{Name: "arr"}}}}

	rendered := len(arr.String())

	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: rendered + (rendered / 4),
	}
	env := newEnv(nil)
	env.Define("arr", arr)
	exec.pushEnv(env)

	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	got, err := exec.evalInterpolatedStringLiteral(lit, env)
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("evalInterpolatedStringLiteral: %v", err)
	}
	if want := arr.String(); got.String() != want {
		t.Fatalf("result = %q, want %q", got.String(), want)
	}

	// Streaming allocates roughly one rendering (the builder's backing buffer).
	// A second full copy (val.String() then WriteString) would allocate at least
	// the rendered bytes again on top, pushing the total past ~2x. A ceiling of
	// 1.6x rejects the double-copy path while tolerating builder growth slack and
	// small bookkeeping allocations.
	allocated := after.TotalAlloc - before.TotalAlloc
	ceiling := uint64(rendered) + uint64(rendered)*6/10
	if allocated > ceiling {
		t.Fatalf("interpolation allocated %d bytes, want <= %d (rendered %d); a second full copy would roughly double this",
			allocated, ceiling, rendered)
	}
}

func TestEvalInterpolatedStringLiteralCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	parts := make([]string, 8)
	for i := range parts {
		parts[i] = "chunk"
	}
	lit := newInterpolatedTextLiteral(parts...)

	exec := &Execution{
		ctx:         ctx,
		quota:       1 << 20,
		memoryQuota: 1 << 20,
	}
	env := newEnv(nil)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("evalInterpolatedStringLiteral under canceled context = %v, want context.Canceled", err)
	}
}

func TestEvalInterpolatedStringLiteralSharedGraphTripsStepQuota(t *testing.T) {
	t.Parallel()

	// A compact aggregate with an exponentially shared, acyclic graph: each level
	// holds two references to the same child array (the shape of repeatedly
	// evaluating a = [a, a]). Its memory stays small because every level reuses
	// one backing slice, and its rendering is bounded because the cycle marker
	// collapses a reference once it is on the recursion stack. But projecting the
	// rendered length re-walks each shared subtree at every occurrence, so the
	// traversal is exponential in the depth. The projection must charge the step
	// budget during that walk so it trips the step quota instead of burning
	// unbounded CPU before the memory check runs.
	const depth = 40

	cur := NewArray([]Value{NewInt(0)})
	for range depth {
		cur = NewArray([]Value{cur, cur})
	}

	lit := &InterpolatedString{Parts: []StringPart{StringExpr{Expr: &Identifier{Name: "shared"}}}}

	exec := &Execution{
		ctx:   context.Background(),
		quota: 100_000,
		// Ample memory: the failure must be the step quota, not memory. The value
		// itself is tiny (one int plus depth two-element arrays).
		memoryQuota: 64 << 20,
	}
	env := newEnv(nil)
	env.Define("shared", cur)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedStringLiteral(lit, env)
	requireErrorIs(t, err, errStepQuotaExceeded)
}

func TestInterpolatedStringGrowthTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A doubling interpolation builds an exponentially larger string inside a
	// single literal expression. The materialization must trip the memory quota
	// rather than allocating the oversized result that the surrounding evaluator
	// would only observe after it already exists.
	source := `def run(n)
  text = "x"
  i = 0
  while i < n
    text = "#{text}#{text}"
    i = i + 1
  end
  text.length
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 64 * 1024}, source)
	requireRunMemoryQuotaError(t, script, []Value{NewInt(20)}, CallOptions{})
}

func TestInterpolatedStringCanceledContextStops(t *testing.T) {
	t.Parallel()

	// Repeated interpolation must observe a canceled context through the
	// per-chunk step check.
	source := `def run(n)
  text = "x"
  i = 0
  while i < n
    text = "#{text}!"
    i = i + 1
  end
  text.length
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 64 << 20}, source)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := script.Call(ctx, "run", []Value{NewInt(100)}, CallOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Call under canceled context = %v, want context.Canceled", err)
	}
}

func TestInterpolatedStringLargeValueRendersUnderAmpleQuota(t *testing.T) {
	t.Parallel()

	// A doubling interpolation builds a moderately large string when the quota
	// is ample; the containment checks must not corrupt or truncate the result.
	const doublings = 8
	source := `def run(n)
  text = "ab"
  i = 0
  while i < n
    text = "#{text}#{text}"
    i = i + 1
  end
  text
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 64 << 20}, source)
	got := callScript(t, context.Background(), script, "run", []Value{NewInt(doublings)}, CallOptions{})
	if want := strings.Repeat("ab", 1<<doublings); got.String() != want {
		t.Fatalf("Call(run) length = %d, want %d", len(got.String()), len(want))
	}
}
