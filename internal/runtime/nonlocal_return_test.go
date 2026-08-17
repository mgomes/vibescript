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

// TestBlockNonLocalReturnFromCalleeInsideBlock pins the other direction of the
// lexical-home rule: a method called from a block body opens a fresh scope, so
// a block it creates returns from the callee — not from the method that owns
// the enclosing block.
func TestBlockNonLocalReturnFromCalleeInsideBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def helper
  [1].each do |x|
    return "done"
  end
  "helper fell through"
end

def outer
  results = [1, 2].map do |x|
    helper
  end
  ["outer finished", results]
end`)

	got := callScript(t, context.Background(), script, "outer", nil, CallOptions{})
	// helper's non-local return ends helper only; outer's map keeps iterating
	// and outer runs to completion.
	compareArrays(t, got, []Value{
		NewString("outer finished"),
		NewArray([]Value{NewString("done"), NewString("done")}),
	})
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
end

def rescued_run
  maker
  begin
    adapter.invoke()
    "unreachable"
  rescue LocalJumpError
    "caught local jump"
  end
end`)

	opts := CallOptions{Globals: map[string]Value{"adapter": adapter}}
	requireCallErrorContains(t, script, "run", nil, opts, "unexpected return")

	// The error is raised at the invocation site as a regular LocalJumpError,
	// so a surrounding rescue can catch it like any other local jump failure.
	if got := callScript(t, context.Background(), script, "rescued_run", nil, opts); got.String() != "caught local jump" {
		t.Fatalf("rescued_run = %v, want caught local jump", got)
	}
}

// TestBlockNonLocalReturnFromDefaultArgument pins token ordering around
// argument binding: a block in a default-argument expression homes to the
// invocation being bound, so a return inside it returns from that method —
// through both the in-script call path and the host entry path — with the
// caller unaffected and return-type validation still applied.
func TestBlockNonLocalReturnFromDefaultArgument(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def f(x = [1].each { |i| return "from default" })
  "body"
end

def typed_default(x = [1].each { |i| return 42 }) -> int
  0
end

def run
  [f(), "after"]
end`)

	ctx := context.Background()
	got := callScript(t, ctx, script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewString("from default"), NewString("after")})

	// The entry path takes the same bind-time return, through validation.
	if got := callScript(t, ctx, script, "f", nil, CallOptions{}); got.String() != "from default" {
		t.Fatalf("entry f() = %v, want from default", got)
	}
	if got := callScript(t, ctx, script, "typed_default", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 42 {
		t.Fatalf("typed_default() = %v, want 42", got)
	}
}

// TestModuleLevelBlockReturnDoesNotEscapeIntoImporter pins the home of blocks
// created outside any method: module top-level statements run while the
// requiring method's frame is live, but a module-level block has no enclosing
// method, so a return inside it raises LocalJumpError instead of returning
// from the importer.
func TestModuleLevelBlockReturnDoesNotEscapeIntoImporter(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "escaper.vibe", content: `[1].each do |x|
  return 99
end

def ok
  1
end
`})
	engine := mustNewEngineWithModuleRoot(t, dir)
	script := compileScriptWithEngine(t, engine, `
def run
  require("escaper")
  "after require"
end
`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unexpected return")
}
