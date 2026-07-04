package runtime

import (
	"context"
	"testing"
)

// TestBlockNonLocalReturn pins Ruby's non-local return: an explicit return in
// a normal block returns from the method whose body created the block, ending
// iteration immediately.
func TestBlockNonLocalReturn(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def first_even(values)
  values.each do |value|
    if value % 2 == 0
      return value
    end
  end
  "after"
end

def visits_before_return(values)
  seen = []
  values.each do |value|
    seen = seen + [value]
    if value % 2 == 0
      return seen
    end
  end
  seen
end

def nested_blocks
  [1, 2].each do |a|
    [10, 20].each do |b|
      if a == 2 && b == 20
        return a + b
      end
    end
  end
  "none"
end

def map_return
  [5, 6, 7].map do |v|
    if v == 6
      return "found six"
    end
    v
  end
end

def run(values)
  first_even(values)
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "run", []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})}, CallOptions{}); got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("first_even via run = %v, want 2", got)
	}
	if got := callScript(t, ctx, script, "first_even", []Value{NewArray([]Value{NewInt(1), NewInt(3)})}, CallOptions{}); got.String() != "after" {
		t.Fatalf("first_even with no even = %v, want after", got)
	}
	seen := callScript(t, ctx, script, "visits_before_return", []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})}, CallOptions{})
	compareArrays(t, seen, []Value{NewInt(1), NewInt(2)})
	if got := callScript(t, ctx, script, "nested_blocks", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 22 {
		t.Fatalf("nested_blocks = %v, want 22", got)
	}
	if got := callScript(t, ctx, script, "map_return", nil, CallOptions{}); got.String() != "found six" {
		t.Fatalf("map_return = %v, want found six", got)
	}
}

// TestBlockNonLocalReturnThroughYield pins the lexical home: a block created
// in one method and yielded by another returns from its creator, not from the
// yielding callee.
func TestBlockNonLocalReturnThroughYield(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def driver
  yield 5
  "driver done"
end

def outer
  driver do |x|
    return x * 10
  end
  "outer done"
end

def inner_literal_homes_to_outer
  driver do |x|
    [x].each do |y|
      return y + 1
    end
  end
  "outer done"
end

def run
  outer
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "run", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 50 {
		t.Fatalf("outer via yield = %v, want 50", got)
	}
	// A literal created while a block body runs under yield still belongs to
	// the block's method, even though the yielding callee's frame is innermost.
	if got := callScript(t, ctx, script, "inner_literal_homes_to_outer", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 6 {
		t.Fatalf("nested literal under yield = %v, want 6", got)
	}
}

// TestBlockNonLocalReturnEnsureAndRescue pins the unwind semantics: ensure
// blocks run on the way out, and no rescue clause (untyped or typed) may
// intercept the return.
func TestBlockNonLocalReturnEnsureAndRescue(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def with_ensure
  trace = []
  result = [1, 2].each do |x|
    begin
      return "early"
    ensure
      trace = trace + ["cleanup"]
    end
  end
  "late"
end

def rescue_does_not_catch
  [1].each do |x|
    begin
      return "escaped"
    rescue
      return "untyped rescued"
    end
  end
  "fell through"
end

def typed_rescue_does_not_catch
  [1].each do |x|
    begin
      return "escaped"
    rescue RuntimeError
      return "typed rescued"
    end
  end
  "fell through"
end

def modifier_rescue_does_not_catch
  [1].each do |x|
    return "escaped"
  end rescue "modifier rescued"
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "with_ensure", nil, CallOptions{}); got.String() != "early" {
		t.Fatalf("with_ensure = %v, want early", got)
	}
	if got := callScript(t, ctx, script, "rescue_does_not_catch", nil, CallOptions{}); got.String() != "escaped" {
		t.Fatalf("rescue_does_not_catch = %v, want escaped", got)
	}
	if got := callScript(t, ctx, script, "typed_rescue_does_not_catch", nil, CallOptions{}); got.String() != "escaped" {
		t.Fatalf("typed_rescue_does_not_catch = %v, want escaped", got)
	}
	if got := callScript(t, ctx, script, "modifier_rescue_does_not_catch", nil, CallOptions{}); got.String() != "escaped" {
		t.Fatalf("modifier_rescue_does_not_catch = %v, want escaped", got)
	}
}

// TestBlockNonLocalReturnValidatesReturnType pins that a non-local return is
// the method's return: a typed method validates the value like any other
// return path.
func TestBlockNonLocalReturnValidatesReturnType(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def find(values) -> int
  values.each do |v|
    return v
  end
  0
end

def run(values)
  find(values)
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "run", []Value{NewArray([]Value{NewInt(7)})}, CallOptions{}); got.Kind() != KindInt || got.Int() != 7 {
		t.Fatalf("find = %v, want 7", got)
	}
	requireCallErrorContains(t, script, "run", []Value{NewArray([]Value{NewString("nope")})}, CallOptions{}, "return value for find expected int, got string")
}

// TestBlockNonLocalReturnDeadFrameLocalJumpError pins the escape case: a block
// held by the host past its method's return has no live frame to return from,
// so invoking it later reports LocalJumpError instead of hijacking an
// unrelated invocation.
func TestBlockNonLocalReturnDeadFrameLocalJumpError(t *testing.T) {
	t.Parallel()

	var stashed Value
	adapter := NewObject(map[string]Value{
		"stash": NewBuiltin("adapter.stash", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			stashed = block
			return NewNil(), nil
		}),
		"invoke": NewBuiltin("adapter.invoke", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.CallBlock(stashed, []Value{NewInt(1)})
		}),
	})

	script := compileScript(t, `def maker
  adapter.stash do |x|
    return x
  end
  "made"
end

def run
  maker
  adapter.invoke()
  "unreachable"
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{Globals: map[string]Value{"adapter": adapter}}, "unexpected return")
}
