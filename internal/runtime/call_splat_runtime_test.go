package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestCallSplatExpansion pins Ruby-style call argument expansion: positional
// splats, keyword splats, combined forms, and mixing with regular arguments,
// blocks, and rest parameters.
func TestCallSplatExpansion(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def add(a, b)
  a + b
end

def issue_repro
  args = [1, 2]
  add(*args)
end

def five(a, b, c, d, e)
  [a, b, c, d, e]
end

def mixed
  five(1, *[2, 3], 4, *[5])
end

def rest_sink(first, *rest)
  [first, rest]
end

def into_rest
  rest_sink(*[1, 2, 3])
end

def kw(a, x: 0, y: 0)
  [a, x, y]
end

def keyword_splat
  opts = {x: 5, y: 9}
  kw(1, **opts)
end

def combined
  args = [1]
  opts = {y: 9}
  kw(*args, x: 5, **opts)
end

def later_wins
  kw(1, x: 2, **{x: 3})
end

def named_after_splat_wins
  kw(1, **{x: 3}, x: 2)
end

def with_block(a, b)
  yield a + b
end

def splat_with_block
  with_block(*[20, 22]) do |n|
    n
  end
end

def splat_with_block_arg
  with_block(*[20, 22], &->(n) { n * 2 })
end

def builtin_splat
  positions = [0, 2]
  ["a", "b", "c"].values_at(*positions)
end

def parenless_splat
  args = [3, 4]
  add *args
end

def empty_splat
  add(1, *[], 2)
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "issue_repro", nil, CallOptions{}); got.Int() != 3 {
		t.Fatalf("issue_repro = %v, want 3", got)
	}
	compareArrays(t, callScript(t, ctx, script, "mixed", nil, CallOptions{}),
		[]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)})
	rest := callScript(t, ctx, script, "into_rest", nil, CallOptions{})
	compareArrays(t, rest, []Value{NewInt(1), NewArray([]Value{NewInt(2), NewInt(3)})})
	compareArrays(t, callScript(t, ctx, script, "keyword_splat", nil, CallOptions{}),
		[]Value{NewInt(1), NewInt(5), NewInt(9)})
	compareArrays(t, callScript(t, ctx, script, "combined", nil, CallOptions{}),
		[]Value{NewInt(1), NewInt(5), NewInt(9)})
	compareArrays(t, callScript(t, ctx, script, "later_wins", nil, CallOptions{}),
		[]Value{NewInt(1), NewInt(3), NewInt(0)})
	compareArrays(t, callScript(t, ctx, script, "named_after_splat_wins", nil, CallOptions{}),
		[]Value{NewInt(1), NewInt(2), NewInt(0)})
	if got := callScript(t, ctx, script, "splat_with_block", nil, CallOptions{}); got.Int() != 42 {
		t.Fatalf("splat_with_block = %v, want 42", got)
	}
	if got := callScript(t, ctx, script, "splat_with_block_arg", nil, CallOptions{}); got.Int() != 84 {
		t.Fatalf("splat_with_block_arg = %v, want 84", got)
	}
	compareArrays(t, callScript(t, ctx, script, "builtin_splat", nil, CallOptions{}),
		[]Value{NewString("a"), NewString("c")})
	if got := callScript(t, ctx, script, "parenless_splat", nil, CallOptions{}); got.Int() != 7 {
		t.Fatalf("parenless_splat = %v, want 7", got)
	}
	if got := callScript(t, ctx, script, "empty_splat", nil, CallOptions{}); got.Int() != 3 {
		t.Fatalf("empty_splat = %v, want 3", got)
	}
}

// TestCallSplatErrors pins misuse: splatting non-arrays, keyword-splatting
// non-hashes, invalid keyword keys, and arity/keyword mismatches that must
// match the errors of the equivalent literal spelling.
func TestCallSplatErrors(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def add(a, b)
  a + b
end

def kw(a, x: 0)
  [a, x]
end

def splat_non_array
  add(*1)
end

def splat_nil
  add(*nil)
end

def keyword_splat_non_hash
  kw(1, **[1, 2])
end

def keyword_splat_bad_key
  kw(1, **{2 => "x"})
end

def missing_after_expansion
  add(*[1])
end

def surplus_after_expansion
  add(*[1, 2, 3])
end

def unknown_keyword
  kw(1, **{nope: 2})
end`)

	ctx := context.Background()
	cases := []struct {
		fn   string
		want string
	}{
		{fn: "splat_non_array", want: "splat argument must be an array, got int"},
		{fn: "splat_nil", want: "splat argument must be an array, got nil"},
		{fn: "keyword_splat_non_hash", want: "keyword splat argument must be a hash, got array"},
		{fn: "keyword_splat_bad_key", want: "keyword splat keys must be strings or symbols, got int"},
		{fn: "missing_after_expansion", want: "missing argument b"},
		{fn: "surplus_after_expansion", want: "unexpected positional arguments"},
		{fn: "unknown_keyword", want: "unexpected keyword argument nope"},
	}
	for _, tc := range cases {
		err := callScriptErr(t, ctx, script, tc.fn, nil, CallOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want substring %q", tc.fn, err, tc.want)
		}
	}
}

// TestCallSplatSandboxQuotas pins the sandbox contract for expansion: a
// splatted array charges the step quota per expanded element and the
// expanded argument backing charges the memory quota, so a splat call needs
// measurably more budget than merely building and holding the same array.
// The thresholds are found by binary search so the test pins the relation
// (splat cost ~ baseline cost + n elements) rather than absolute costs.
func TestCallSplatSandboxQuotas(t *testing.T) {
	t.Parallel()

	source := `def sink(*values)
  values.size
end

def build(n)
  (1..n).to_a
end

def baseline(n)
  args = build(n)
  args.size
end

def splat_call(n)
  args = build(n)
  sink(*args)
end`

	const n = 800
	ctx := context.Background()

	runWith := func(t *testing.T, cfg Config, fn string) error {
		t.Helper()
		script := compileScriptWithConfig(t, cfg, source)
		_, err := script.Call(ctx, fn, []Value{NewInt(n)}, CallOptions{})
		return err
	}

	// minimalQuota binary-searches the smallest quota value in [1, hi] for
	// which run succeeds.
	minimalQuota := func(t *testing.T, hi int, run func(quota int) error) int {
		t.Helper()
		lo := 1
		if err := run(hi); err != nil {
			t.Fatalf("run at quota %d must succeed, got %v", hi, err)
		}
		for lo < hi {
			mid := lo + (hi-lo)/2
			if run(mid) == nil {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo
	}

	t.Run("steps", func(t *testing.T) {
		t.Parallel()
		baselineSteps := minimalQuota(t, 1<<20, func(quota int) error {
			return runWith(t, Config{StepQuota: quota, MemoryQuotaBytes: 64 << 20}, "baseline")
		})
		// Expansion walks every element, so the splat call needs at least n
		// more steps than the baseline that only builds the array.
		err := runWith(t, Config{StepQuota: baselineSteps + n/2, MemoryQuotaBytes: 64 << 20}, "splat_call")
		if err == nil || !strings.Contains(err.Error(), "step quota exceeded") {
			t.Fatalf("splat_call at baseline+n/2 steps = %v, want step quota exceeded", err)
		}
		if err := runWith(t, Config{StepQuota: baselineSteps + 8*n, MemoryQuotaBytes: 64 << 20}, "splat_call"); err != nil {
			t.Fatalf("splat_call with generous steps failed: %v", err)
		}
	})

	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		baselineBytes := minimalQuota(t, 64<<20, func(quota int) error {
			return runWith(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: quota}, "baseline")
		})
		// The expanded argument backing and the rest-parameter copy are fresh
		// slot allocations on top of the source array, so a quota with only a
		// sliver of headroom above the baseline peak must reject the splat.
		err := runWith(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: baselineBytes + 8*n}, "splat_call")
		if err == nil || !strings.Contains(err.Error(), "memory quota exceeded") {
			t.Fatalf("splat_call at baseline+8n bytes = %v, want memory quota exceeded", err)
		}
		if err := runWith(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: baselineBytes * 4}, "splat_call"); err != nil {
			t.Fatalf("splat_call with generous memory failed: %v", err)
		}
	})
}
