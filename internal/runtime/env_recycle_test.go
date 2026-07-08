package runtime

import (
	"os"
	"testing"
)

// maybeEnableEnvRecycleVerify turns on env-recycle verification when the
// VIBES_ENV_RECYCLE_VERIFY environment variable is set to "1". It is called from
// TestMain before any test runs so the flag is write-once: production code and
// tests only read it thereafter, keeping the package race-free under -race even
// with parallel tests. Running `VIBES_ENV_RECYCLE_VERIFY=1 go test` then executes
// the entire corpus with every recycled call frame poisoned, so any capture site
// the recycler wrongly judged dead panics on its next access.
func maybeEnableEnvRecycleVerify() {
	if os.Getenv("VIBES_ENV_RECYCLE_VERIFY") == "1" {
		envRecycleVerify = true
	}
}

// TestAcquireRecycleCallEnvPooling pins the production pooling contract of
// acquireCallEnv / recycleCallEnv directly: a reuse-eligible function's frame is
// returned to the free list and handed back to the next acquire (same pointer,
// reparented and cleared), while an ineligible function always allocates a fresh
// frame and never draws from the pool.
func TestAcquireRecycleCallEnvPooling(t *testing.T) {
	if envRecycleVerify {
		t.Skip("pool is bypassed under env-recycle verification")
	}

	exec := &Execution{}
	parentA := newEnv(nil)
	parentB := newEnv(nil)
	reusable := &ScriptFunction{Env: parentA, reuseCallEnv: true}

	first := exec.acquireCallEnv(reusable, 2)
	if first.parent != parentA {
		t.Fatalf("fresh acquire parent = %p, want %p", first.parent, parentA)
	}
	first.Define("scratch", NewInt(7))

	exec.recycleCallEnv(first)
	if len(exec.callEnvFreeList) != 1 {
		t.Fatalf("free list after recycle = %d, want 1", len(exec.callEnvFreeList))
	}
	// Recycling clears the frame immediately, so a pooled frame never pins the
	// heap payloads of its former locals until the next acquire.
	if _, ok := first.getOwn("scratch"); ok {
		t.Fatalf("recycled frame kept a stale binding before reuse")
	}
	if first.parent != nil {
		t.Fatalf("recycled frame kept parent %p, want nil", first.parent)
	}

	second := exec.acquireCallEnv(reusable, 2)
	if second != first {
		t.Fatalf("acquire did not reuse the recycled frame (%p vs %p)", second, first)
	}
	if len(exec.callEnvFreeList) != 0 {
		t.Fatalf("free list after reuse = %d, want 0", len(exec.callEnvFreeList))
	}
	if _, ok := second.getOwn("scratch"); ok {
		t.Fatalf("recycled frame kept a stale binding")
	}

	// A frame from a different function reparents to that function's Env.
	other := &ScriptFunction{Env: parentB, reuseCallEnv: true}
	exec.recycleCallEnv(second)
	third := exec.acquireCallEnv(other, 1)
	if third != second {
		t.Fatalf("cross-function acquire did not reuse the pooled frame")
	}
	if third.parent != parentB {
		t.Fatalf("reused frame parent = %p, want %p", third.parent, parentB)
	}

	// An ineligible function never draws from the pool, even when one is waiting.
	exec.recycleCallEnv(third)
	ineligible := &ScriptFunction{Env: parentA, reuseCallEnv: false}
	fresh := exec.acquireCallEnv(ineligible, 1)
	if fresh == third {
		t.Fatalf("ineligible function drew a frame from the pool")
	}
	if len(exec.callEnvFreeList) != 1 {
		t.Fatalf("free list after ineligible acquire = %d, want 1", len(exec.callEnvFreeList))
	}
}

// withEnvRecycleVerify runs fn with verification enabled and restores the prior
// value afterward. It must be called only from non-parallel tests: sequential
// top-level tests never run concurrently with any other test, so toggling the
// package global here does not race even under -race.
func withEnvRecycleVerify(t *testing.T, fn func()) {
	t.Helper()
	prev := envRecycleVerify
	envRecycleVerify = true
	defer func() { envRecycleVerify = prev }()
	fn()
}

// TestRecycleCallEnvPoisonsUnderVerify pins the verification-mode contract: a
// recycled frame is poisoned and dropped rather than pooled, and touching it
// afterward panics.
func TestRecycleCallEnvPoisonsUnderVerify(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		exec := &Execution{}
		fn := &ScriptFunction{Env: newEnv(nil), reuseCallEnv: true}
		env := exec.acquireCallEnv(fn, 1)
		env.Define("x", NewInt(1))

		exec.recycleCallEnv(env)
		if len(exec.callEnvFreeList) != 0 {
			t.Fatalf("verification mode pooled a frame; free list = %d, want 0", len(exec.callEnvFreeList))
		}
		if !env.poisoned {
			t.Fatalf("recycled frame was not poisoned under verification")
		}

		defer func() {
			if recover() == nil {
				t.Fatalf("accessing a poisoned frame did not panic")
			}
		}()
		env.Define("y", NewInt(2))
	})
}

// TestPoisonedEnvGuardsAllAccessPaths verifies the poison tripwire fires no
// matter how a stale reference to a recycled frame is used. Every access path a
// running program can take to a scope's own storage must route through a guarded
// primitive; this pins that so a newly added accessor that forgets the guard is
// caught. A missed path would let a wrongly recycled capturing frame be read or
// mutated in verification mode without panicking, defeating the whole safety net.
func TestPoisonedEnvGuardsAllAccessPaths(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		cases := []struct {
			name   string
			access func(*Env)
		}{
			{"Get", func(e *Env) { e.Get("x") }},
			{"getBoundValue", func(e *Env) { e.getBoundValue("x", nil) }},
			{"getCallLocal", func(e *Env) { e.getCallLocal("x") }},
			{"getSkipping", func(e *Env) { e.getSkipping("x", nil) }},
			{"Define", func(e *Env) { e.Define("y", NewInt(2)) }},
			{"Assign", func(e *Env) { e.Assign("x", NewInt(2)) }},
			{"getOwn", func(e *Env) { e.getOwn("x") }},
			{"hasOwnBinding", func(e *Env) { e.hasOwnBinding("x") }},
			{"hasDynamic", func(e *Env) { e.hasDynamic("x") }},
			{"setDynamic", func(e *Env) { e.setDynamic("x", NewInt(3)) }},
			{"setExistingDynamic", func(e *Env) { e.setExistingDynamic("x", NewInt(3)) }},
			{"deleteDynamic", func(e *Env) { e.deleteDynamic("x") }},
			{"lookupBindingScope", func(e *Env) { e.lookupBindingScope("x") }},
			{"arrayAppendBuffer", func(e *Env) { e.arrayAppendBuffer("x") }},
			{"setArrayAppendBuffer", func(e *Env) { e.setArrayAppendBuffer("x", nil) }},
			{"lookupCallBlock", func(e *Env) { e.lookupCallBlock() }},
			{"setCallBlock", func(e *Env) { e.setCallBlock(NewNil()) }},
			{"rangeDynamicBindings", func(e *Env) { e.rangeDynamicBindings(func(string, Value) {}) }},
			{"rangeStaticBindings", func(e *Env) { e.rangeStaticBindings(func(string, Value) {}) }},
			{"visibleNames", func(e *Env) { e.visibleNames() }},
			{"CloneShallow", func(e *Env) { e.CloneShallow() }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newEnv(nil)
				env.Define("x", NewInt(1))
				env.poisoned = true
				defer func() {
					if recover() == nil {
						t.Fatalf("%s did not panic on a poisoned env", tc.name)
					}
				}()
				tc.access(env)
			})
		}
	})
}

// TestPoisonedParentEnvGuarded pins that chain walks catch a poisoned *ancestor*,
// not just the entry scope — the case where a closure captured a wrongly recycled
// frame as a parent and then reads a variable, yields, or settles an array. The
// entry scope is live; the poison sits one level up.
func TestPoisonedParentEnvGuarded(t *testing.T) {
	withEnvRecycleVerify(t, func() {
		cases := []struct {
			name   string
			access func(child *Env)
		}{
			{"Get", func(child *Env) { child.Get("x") }},
			{"Assign", func(child *Env) { child.Assign("x", NewInt(9)) }},
			{"lookupCallBlock", func(child *Env) { child.lookupCallBlock() }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				parent := newEnv(nil)
				parent.Define("x", NewInt(1))
				parent.poisoned = true
				child := newEnv(parent)
				defer func() {
					if recover() == nil {
						t.Fatalf("%s did not panic on a poisoned parent env", tc.name)
					}
				}()
				tc.access(child)
			})
		}
	})
}

// TestEnvRecycleBattery runs a spread of programs that exercise every call-frame
// escape route the recycler must respect. Each asserts an exact result, so a
// frame that was wrongly reused (aliasing a live binding) changes the answer.
// Run under VIBES_ENV_RECYCLE_VERIFY=1 the same programs additionally poison
// every recycled frame, so a wrongly-recycled capturing frame panics on access.
func TestEnvRecycleBattery(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   int64
	}{
		{
			// Pure hot path: deep recursion with no capture. Every frame is
			// reuse-eligible and cycles through the pool.
			name: "deep_recursion",
			source: `
def fib(n)
  if n < 2
    n
  else
    fib(n - 1) + fib(n - 2)
  end
end
fib(22)`,
			want: 17711,
		},
		{
			// Mutual recursion: two reuse-eligible functions share the pool.
			name: "mutual_recursion",
			source: `
def is_even(n)
  if n == 0
    true
  else
    is_odd(n - 1)
  end
end
def is_odd(n)
  if n == 0
    false
  else
    is_even(n - 1)
  end
end
count = 0
i = 0
while i < 200
  if is_even(i)
    count = count + 1
  end
  i = i + 1
end
count`,
			want: 100,
		},
		{
			// Array built by append and returned: the frame is reuse-eligible, so
			// the escaping array must be settled before the frame is pooled or a
			// later call would scribble the returned backing.
			name: "append_and_return",
			source: `
def build(n)
  acc = []
  i = 0
  while i < n
    acc << i * i
    i = i + 1
  end
  acc
end
total = 0
r = 0
while r < 50
  arr = build(5)
  arr.each { |v| total = total + v }
  r = r + 1
end
total`,
			// each build -> [0,1,4,9,16] sums to 30; 50 rounds -> 1500.
			want: 1500,
		},
		{
			// Closures returned from a factory and invoked AFTER it returns. The
			// factory frame must NOT be recycled (its lambda captures it); a wrong
			// recycle would corrupt each closure's captured n.
			name: "returned_closures",
			source: `
def make_adder(n)
  ->(x) { x + n }
end
adders = []
i = 0
while i < 30
  adders << make_adder(i)
  i = i + 1
end
total = 0
adders.each { |f| total = total + f.call(100) }
total`,
			// sum over i in 0..29 of (100 + i) = 3000 + 435 = 3435.
			want: 3435,
		},
		{
			// A captured closure calls a reuse-eligible helper on every
			// invocation: recycling the helper frame must never touch the
			// closure's retained binding.
			name: "closure_calls_recycled_helper",
			source: `
def helper(a, b)
  a * b + a - b
end
def make(n)
  ->(x) { helper(x, n) }
end
fns = []
i = 1
while i <= 20
  fns << make(i)
  i = i + 1
end
total = 0
fns.each { |f| total = total + f.call(3) }
total`,
			// helper(3, n) = 3n + 3 - n = 2n + 3; sum over n in 1..20 of (2n+3)
			// = 2*210 + 60 = 480.
			want: 480,
		},
		{
			// Non-capturing default parameter: still reuse-eligible.
			name: "noncapturing_default",
			source: `
def scale(x, factor = 3)
  x * factor
end
total = 0
i = 0
while i < 100
  total = total + scale(i) + scale(i, 2)
  i = i + 1
end
total`,
			// sum over i of (3i + 2i) = 5 * sum(0..99) = 5 * 4950 = 24750.
			want: 24750,
		},
		{
			// yield to a block passed in: the callee frame does not capture the
			// block (created in the caller), so it stays reuse-eligible.
			name: "yielding_reducer",
			source: `
def reduce_range(n)
  acc = 0
  i = 0
  while i < n
    acc = acc + yield(i)
    i = i + 1
  end
  acc
end
reduce_range(100) { |v| v * 2 }`,
			// sum over i in 0..99 of 2i = 2 * 4950 = 9900.
			want: 9900,
		},
		{
			// Property accessors in a hot loop: getter/setter frames are
			// reuse-eligible and cycle heavily.
			name: "accessors",
			source: `
class Counter
  property value
  def initialize
    @value = 0
  end
  def bump(by)
    @value = @value + by
  end
end
c = Counter.new
i = 0
while i < 300
  c.bump(i)
  i = i + 1
end
c.value`,
			// sum(0..299) = 44850.
			want: 44850,
		},
		{
			// Ivar-assignment parameters (`def initialize(@x)`) bind through the
			// param Target, which the body walk never sees. The frame is still
			// reuse-eligible (assigning an ivar does not capture), and heavy
			// construction cycles the pool.
			name: "ivar_params",
			source: `
class Point
  property x
  property y
  def initialize(@x, @y)
  end
  def dist_sq
    @x * @x + @y * @y
  end
end
total = 0
i = 0
while i < 200
  p = Point.new(i, i + 1)
  total = total + p.dist_sq
  i = i + 1
end
total`,
			// sum over i in 0..199 of (i^2 + (i+1)^2).
			want: 5333400,
		},
		{
			// Recursion whose body passes a block to a method (a capture): the
			// function is NOT reuse-eligible, verifying the analysis excludes it
			// while still producing the right answer.
			name: "recursion_with_block_call",
			source: `
def sum_pairs(n)
  if n <= 0
    0
  else
    parts = [n, n]
    subtotal = 0
    parts.each { |p| subtotal = subtotal + p }
    subtotal + sum_pairs(n - 1)
  end
end
sum_pairs(50)`,
			// each level contributes 2n; sum over n in 1..50 of 2n = 2 * 1275 = 2550.
			want: 2550,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runProgram(t, tc.source, "__main__")
			if got.Kind() != KindInt || got.Int() != tc.want {
				t.Fatalf("%s = %v, want %d", tc.name, got, tc.want)
			}
		})
	}
}
