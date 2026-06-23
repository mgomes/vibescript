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
