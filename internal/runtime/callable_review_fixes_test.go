package runtime

import (
	"context"
	"testing"
)

// TestCallableExpectationDoesNotEscapeUniversalBuiltins pins that a universal
// or type-dispatch member (list.size) under a callable-including expectation
// evaluates like a normal argument: the value reaches the callee, never the
// raw builtin, which would later run against a nil receiver.
func TestCallableExpectationDoesNotEscapeUniversalBuiltins(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def take(f: function | int)
  f
end

def strict(f: function)
  f()
end

def run
  take([1, 2, 3].size)
end

def strict_run
  strict("hello".upcase)
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "run", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 3 {
		t.Fatalf("take([1,2,3].size) = %v, want 3", got)
	}
	// A plain string member still evaluates and then fails the contract with
	// the master-era type error rather than escaping a builtin.
	requireCallErrorContains(t, script, "strict_run", nil, CallOptions{}, "argument f expected function, got string")
}

// TestStoredCallablePropertyParenlessCall pins that c.cb.call without parens
// resolves the stored callable exactly like c.cb.call().
func TestStoredCallablePropertyParenlessCall(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class C
  property cb: function
end

def five
  5
end

def run
  c = C.new
  c.cb = five
  [c.cb.call, c.cb.call()]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(5), NewInt(5)})
}

// TestBoundMethodRespondsToCall pins Ruby's Method#call protocol on bound
// method references captured under a function contract.
func TestBoundMethodRespondsToCall(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class K
  def helper
    9
  end
  def scaled(n)
    n * 2
  end
end

def take(f: function)
  f.call
end

def take_args(f: function)
  f.call(21)
end

def run
  [take(K.new.helper), take_args(K.new.scaled)]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(9), NewInt(42)})
}

// TestIndexedReceiverCallableSetterExpectation pins that a literal-indexed
// receiver infers the same callable setter expectation as a bare identifier,
// so the RHS function reference is stored rather than auto-invoked.
func TestIndexedReceiverCallableSetterExpectation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class C
  property cb: function
end

def five
  5
end

def run
  arr = [C.new]
  arr[0].cb = five
  h = {c: C.new}
  h[:c].cb = five
  s = {"k": C.new}
  s["k"].cb = five
  [arr[0].cb.call(), h[:c].cb.call(), s["k"].cb.call()]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(5), NewInt(5), NewInt(5)})
}
