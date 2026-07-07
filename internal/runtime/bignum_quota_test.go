package runtime

import (
	"io"
	"strings"
	"testing"
)

// The sandbox battery for big integers: memory-quota scaling in both
// directions, step charges proportional to operand size, O(1) preflight of
// exponentiation and oversized multiplications, and rendering preflights that
// trip before the superlinear base conversion.

// squaringLoop doubles x's bit length every iteration, so per-statement quota
// checks must trip at a scale set by the quota, not by iteration count.
const squaringLoop = `
def run
  x = 3
  20.times do
    x = x * x
  end
  [x % 9 == 0, x.odd?]
end
`

func TestBignumMemoryQuotaTripsOnSquaringLoop(t *testing.T) {
	t.Parallel()
	// ~1.6M bits after 20 squarings: far past the default 64KB quota even
	// with an effectively unlimited step budget, so the failure must be the
	// memory quota.
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 64 * 1024}, squaringLoop)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
}

func TestBignumMemoryQuotaAdmitsSquaringLoopUnderLargerQuota(t *testing.T) {
	t.Parallel()
	// The same loop under a 64MB quota (and a step budget covering the
	// word-scaled charges) completes: the quota, not the promotion machinery,
	// decides the ceiling.
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 64 * 1024 * 1024}, squaringLoop)
	got := callFunc(t, script, "run", nil)
	if got.String() != "[true, true]" {
		t.Fatalf("squaring loop result = %s; want [true, true] (3^(2^20) is odd and divisible by 9)", got)
	}
}

func TestBignumStepQuotaScalesWithOperandBits(t *testing.T) {
	t.Parallel()
	// Bisection evidence for the word-scaled step charge: the same number of
	// multiplications passes with small big operands and trips the same step
	// quota with operands ~16x larger, so cost tracks bit length rather than
	// operation count. Each `x * 3` charges roughly 1 + words/8 steps.
	const quota = 20_000
	small := `
def run
  x = 2 ** 6_400
  400.times { |i| y = x * 3 }
  "done"
end
`
	large := `
def run
  x = 2 ** 102_400
  400.times { |i| y = x * 3 }
  "done"
end
`
	// small: ~100 words -> ~13 steps per multiply -> ~5.2k + loop overhead.
	passScript := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: 64 * 1024 * 1024}, small)
	if got := callFunc(t, passScript, "run", nil); got.String() != "done" {
		t.Fatalf("small-operand loop = %s; want done", got)
	}
	// large: ~1600 words -> ~200 steps per multiply -> ~80k, over the quota.
	failScript := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: 64 * 1024 * 1024}, large)
	requireCallErrorContains(t, failScript, "run", nil, CallOptions{}, "step quota exceeded")
}

func TestBignumPowerPreflightRejectsInConstantSpace(t *testing.T) {
	t.Parallel()
	// The projected result of 2 ** 10_000_000_000 is ~1.25GB; the preflight
	// must reject it against the default 64KB quota before any allocation.
	// (If the preflight regressed, this test would OOM or hang rather than
	// fail an assertion.)
	script := compileScriptWithConfig(t, Config{}, `
    def run
      2 ** 10_000_000_000
    end
  `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")

	// The same guard covers a compact exponent whose product with the base's
	// bit length overflows any projection arithmetic.
	saturated := compileScriptWithConfig(t, Config{}, `
    def run
      3 ** 9_223_372_036_854_775_807
    end
  `)
	requireCallErrorContains(t, saturated, "run", nil, CallOptions{}, "memory quota exceeded")

	// With an enormous memory quota the step quota takes over, still in O(1).
	steps := compileScriptWithConfig(t, Config{StepQuota: 50_000, MemoryQuotaBytes: 1 << 40}, `
    def run
      2 ** 10_000_000_000
    end
  `)
	requireCallErrorContains(t, steps, "run", nil, CallOptions{}, "step quota exceeded")
}

func TestBignumMultiplicationPreflightRejectsOversizedProduct(t *testing.T) {
	t.Parallel()
	// Two ~19KB operands fit the 64KB quota, but their ~37KB product pushes
	// the projected peak past it; the preflight rejects before computing.
	script := compileScriptWithConfig(t, Config{StepQuota: 10_000_000, MemoryQuotaBytes: 64 * 1024}, `
    def run
      a = 2 ** 150_000
      b = 2 ** 150_000
      a * b
    end
  `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
}

func TestBignumRenderStepPreflightTripsBeforeConversion(t *testing.T) {
	t.Parallel()
	// A ~100k-bit value costs ~200 steps to build, but its ~30k-digit decimal
	// rendering charges ~3.8k steps in the projection before big.Int.Text
	// runs, so a 2k step budget trips on the projection.
	script := compileScriptWithConfig(t, Config{StepQuota: 2_000, MemoryQuotaBytes: 64 * 1024 * 1024}, `
    def run
      x = 2 ** 100_000
      x.to_s
    end
  `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
}

func TestBignumInterpolationMemoryPreflight(t *testing.T) {
	t.Parallel()
	// The value itself (~37KB) fits the 64KB quota, but interpolating it
	// projects ~90KB of rendered digits on top; the projected-rendering check
	// rejects before the string materializes.
	script := compileScriptWithConfig(t, Config{StepQuota: 10_000_000, MemoryQuotaBytes: 64 * 1024}, `
    def run
      x = 2 ** 300_000
      "#{x}"
    end
  `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "memory quota exceeded")
}

func TestBignumInspectAndPutsPreflight(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 10_000_000, MemoryQuotaBytes: 64 * 1024, OutputWriter: io.Discard}, `
    def show(x)
      puts x
    end
    def debug(x)
      x.inspect
    end
    def build
      2 ** 300_000
    end
  `)
	big := callFunc(t, script, "build", nil)
	requireCallErrorContains(t, script, "show", []Value{big}, CallOptions{}, "memory quota exceeded")
	requireCallErrorContains(t, script, "debug", []Value{big}, CallOptions{}, "memory quota exceeded")
}

func TestBignumRenderingSucceedsUnderAdequateQuotas(t *testing.T) {
	t.Parallel()
	// Sanity for the other direction: a modest big value renders fine, so the
	// preflights bound rather than break rendering.
	script := compileScriptWithConfig(t, Config{StepQuota: 1_000_000, MemoryQuotaBytes: 8 * 1024 * 1024}, `
    def run
      x = 2 ** 10_000
      s = "#{x}"
      [s.length, s.start_with?("19950631")]
    end
  `)
	if got := callFunc(t, script, "run", nil).String(); got != "[3011, true]" {
		t.Fatalf("rendered 2**10000 summary = %s; want [3011, true]", got)
	}
}

func TestBignumJSONRoundTrip(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run
      x = 2 ** 100
      payload = JSON.stringify({ big: x, arr: [x + 1] })
      parsed = JSON.parse(payload)
      [payload, parsed["big"] == x, parsed["arr"][0] == x + 1, JSON.parse(JSON.stringify(x)) == x]
    end
  `)
	got := callFunc(t, script, "run", nil)
	want := `[{"big":1267650600228229401496703205376,"arr":[1267650600228229401496703205377]}, true, true, true]`
	if got.String() != want {
		t.Fatalf("JSON round trip = %s\nwant %s", got, want)
	}
}

func TestBignumJSONStringifyRejectsOversizedOutputEarly(t *testing.T) {
	t.Parallel()
	// ~1.2M digits exceeds the 1MiB JSON payload cap; the digit lower bound
	// rejects before the base conversion runs.
	script := compileScriptWithConfig(t, Config{StepQuota: 10_000_000, MemoryQuotaBytes: 8 * 1024 * 1024}, `
    def run
      JSON.stringify(2 ** 4_000_000)
    end
  `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "JSON.stringify output exceeds limit")
}

func TestBignumJSONParseChargesAndParsesBigTokens(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run
      v = JSON.parse("[340282366920938463463374607431768211456, 1.5e2, -9223372036854775809]")
      [v[0], v[1], v[2]]
    end
  `)
	got := callFunc(t, script, "run", nil)
	if got.String() != "[340282366920938463463374607431768211456, 150, -9223372036854775809]" {
		t.Fatalf("JSON.parse big tokens = %s", got)
	}
	arr := got.Array()
	if !arr[0].IsBigInt() || arr[1].Kind() != KindFloat || !arr[2].IsBigInt() {
		t.Fatalf("JSON.parse kinds = big:%v float:%v big:%v", arr[0].IsBigInt(), arr[1].Kind() == KindFloat, arr[2].IsBigInt())
	}
}

func TestBignumFormatVerbs(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run
      x = 2 ** 100
      [
        "%d" % x,
        "%d" % (0 - x),
        "%x" % x,
        "%b" % (2 ** 65),
        "%s" % x,
        "%.1f" % (10 ** 20),
        format("%d!", x)
      ]
    end
  `)
	got := callFunc(t, script, "run", nil)
	want := `[1267650600228229401496703205376, -1267650600228229401496703205376, 10000000000000000000000000, 100000000000000000000000000000000000000000000000000000000000000000, 1267650600228229401496703205376, 100000000000000000000.0, 1267650600228229401496703205376!]`
	if got.String() != want {
		t.Fatalf("format verbs = %s\nwant %s", got, want)
	}

	// Code-point verbs genuinely need an int64 and stay loud.
	cScript := compileScript(t, `
    def run
      "%c" % (2 ** 100)
    end
  `)
	requireCallErrorContains(t, cScript, "run", nil, CallOptions{}, "format %c expects integer operand")
}

func TestBignumRangeSumPromotes(t *testing.T) {
	t.Parallel()
	// Gauss over 1..n with n large enough that the int64 accumulator
	// overflows mid-fold: the sum spills into arbitrary precision instead of
	// erroring (Array#sum and reduce already promote through addValues).
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 64 * 1024 * 1024}, `
    def run
      (9223372036854775707..9223372036854775807).sum
    end
  `)
	got := callFunc(t, script, "run", nil)
	if !got.IsBigInt() || got.String() != "931560575722332351457" {
		t.Fatalf("range sum spill = %s (big=%v), want 931560575722332351457", got, got.IsBigInt())
	}
}

func TestBignumStringBoundedTruncationPathIsSane(t *testing.T) {
	t.Parallel()
	// ErrStringRenderTruncated behavior for big values: the CLI-facing bounded
	// renderer refuses fast and returns an empty partial rather than digits.
	v := NewInt(0)
	_ = v
	script := compileScript(t, `
    def build
      2 ** 200_000
    end
  `)
	big := callFunc(t, script, "build", nil)
	out, err := big.StringBounded(64)
	if err == nil || !strings.Contains(err.Error(), "exceeded byte limit") {
		t.Fatalf("StringBounded error = %v", err)
	}
	if out != "" {
		t.Fatalf("StringBounded partial = %d bytes; want empty fast rejection", len(out))
	}
}
