package runtime

import (
	"context"
	"testing"
)

// runProgram compiles source, calls entry with no args under unlimited quotas,
// and returns the result. It exercises the argument-buffer pool through the
// normal call path.
func runProgram(t *testing.T, source, entry string) Value {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(source, entry)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := script.Call(context.Background(), entry, nil, CallOptions{})
	if err != nil {
		t.Fatalf("call %s: %v", entry, err)
	}
	return result
}

// TestArgBufferPoolDeepRecursion checks that reusing argument backings across a
// deep recursion produces correct results — a stale or aliased buffer would
// corrupt an argument and change the sum.
func TestArgBufferPoolDeepRecursion(t *testing.T) {
	t.Parallel()

	got := runProgram(t, `
def fib(n)
  if n < 2
    n
  else
    fib(n - 1) + fib(n - 2)
  end
end

fib(20)
`, "__main__")
	if got.Kind() != KindInt || got.Int() != 6765 {
		t.Fatalf("fib(20) = %v, want 6765", got)
	}
}

// TestArgBufferPoolNestedArgumentCalls checks the LIFO discipline: an argument
// is itself a call, so the inner call borrows and returns a buffer while the
// outer call's buffer is mid-fill. A shared or double-returned buffer would
// scramble the arguments.
func TestArgBufferPoolNestedArgumentCalls(t *testing.T) {
	t.Parallel()

	got := runProgram(t, `
def add(a, b, c)
  a + b + c
end

def twice(x)
  x * 2
end

add(twice(1), add(2, 3, 4), twice(add(1, 1, 1)))
`, "__main__")
	// twice(1)=2, add(2,3,4)=9, twice(add(1,1,1))=6 -> 2+9+6 = 17
	if got.Kind() != KindInt || got.Int() != 17 {
		t.Fatalf("nested arg calls = %v, want 17", got)
	}
}

// TestArgBufferPoolVaryingArity mixes call arities so the pool serves buffers of
// different lengths, catching a length/capacity mismatch in reuse.
func TestArgBufferPoolVaryingArity(t *testing.T) {
	t.Parallel()

	got := runProgram(t, `
def one(a)
  a
end

def three(a, b, c)
  a + b + c
end

total = 0
i = 0
while i < 100
  total = total + one(i) + three(i, i, i)
  i = i + 1
end
total
`, "__main__")
	// sum over i in 0..99 of (i + 3i) = 4 * sum(0..99) = 4 * 4950 = 19800
	if got.Kind() != KindInt || got.Int() != 19800 {
		t.Fatalf("varying arity = %v, want 19800", got)
	}
}

// TestArgBufferPoolKeywordOptionsCollapse exercises the path where keyword
// arguments collapse into a trailing options hash, which appends to the
// argument slice — the buffer must be capped so the append never scribbles the
// pooled backing.
func TestArgBufferPoolKeywordOptionsCollapse(t *testing.T) {
	t.Parallel()

	got := runProgram(t, `
def configure(name, opts)
  opts[:size] + opts[:count]
end

total = 0
i = 0
while i < 50
  total = total + configure("widget", size: i, count: i + 1)
  i = i + 1
end
total
`, "__main__")
	// sum over i in 0..49 of (i + (i+1)) = sum(2i+1) = 2*1225 + 50 = 2500
	if got.Kind() != KindInt || got.Int() != 2500 {
		t.Fatalf("keyword options collapse = %v, want 2500", got)
	}
}
