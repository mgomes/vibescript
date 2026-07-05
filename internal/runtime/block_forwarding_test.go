package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestBlockForwarding pins Ruby-style `&block` forwarding: a captured block
// parameter forwards to another call's block slot, including into builtins
// that drive blocks, and yield sees the forwarded block.
func TestBlockForwarding(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def call_it
  yield 3
end

def forward(&block)
  call_it(&block)
end

def issue_repro
  forward do |n|
    n + 1
  end
end

def map_forward(&blk)
  [1, 2, 3].map(&blk)
end

def map_forwarded
  map_forward { |x| x * 10 }
end

def chain_outer(&b)
  chain_inner(&b)
end

def chain_inner(&b)
  yield 5
end

def chained
  chain_outer { |n| n * 3 }
end

def tap_forward(&b)
  5.tap(&b)
end

def tapped
  tap_forward { |x| x * 2 }
end

def optional(&b)
  block_given?
end

def forwards_nil
  optional(&nil)
end

def forwards_nil_variable
  blk = nil
  optional(&blk)
end

def lambda_forward
  call_it(&->(a) { a * 7 })
end

def lambda_forward_arity
  call_it(&->(a, b) { a + b })
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "issue_repro", nil, CallOptions{}); got.Int() != 4 {
		t.Fatalf("issue_repro = %v, want 4", got)
	}
	mapped := callScript(t, ctx, script, "map_forwarded", nil, CallOptions{})
	compareArrays(t, mapped, []Value{NewInt(10), NewInt(20), NewInt(30)})
	if got := callScript(t, ctx, script, "chained", nil, CallOptions{}); got.Int() != 15 {
		t.Fatalf("chained = %v, want 15", got)
	}
	// tap returns its receiver after running the forwarded block.
	if got := callScript(t, ctx, script, "tapped", nil, CallOptions{}); got.Int() != 5 {
		t.Fatalf("tapped = %v, want 5", got)
	}
	if got := callScript(t, ctx, script, "forwards_nil", nil, CallOptions{}); got.Truthy() {
		t.Fatalf("forwards_nil = %v, want false", got)
	}
	if got := callScript(t, ctx, script, "forwards_nil_variable", nil, CallOptions{}); got.Truthy() {
		t.Fatalf("forwards_nil_variable = %v, want false", got)
	}
	if got := callScript(t, ctx, script, "lambda_forward", nil, CallOptions{}); got.Int() != 21 {
		t.Fatalf("lambda_forward = %v, want 21", got)
	}
	err := callScriptErr(t, ctx, script, "lambda_forward_arity", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "lambda expects 2 arguments, got 1") {
		t.Fatalf("lambda_forward_arity error = %v, want strict lambda arity", err)
	}
}

// TestBlockForwardingNonLocalReturn pins non-local return transparency
// through a forwarded block: the return unwinds to the method whose body
// created the block literal, across the forwarding hop.
func TestBlockForwardingNonLocalReturn(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def call_it
  yield 3
  "callee finished"
end

def forward(&b)
  call_it(&b)
  "forward finished"
end

def home
  forward do |n|
    return n * 100
  end
  "home finished"
end`)

	if got := callScript(t, context.Background(), script, "home", nil, CallOptions{}); got.Int() != 300 {
		t.Fatalf("home = %v, want 300 (non-local return through forwarded block)", got)
	}
}

// TestSymbolToProc pins `&:name` symbol-to-proc: member dispatch over
// enumerables, operator methods through reduce, typed accessors as the
// invoked member, and privacy enforcement.
func TestSymbolToProc(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class User
  getter name: String
  getter age: int

  def initialize(name, age)
    @name = name
    @age = age
  end

  def shout
    @name.upcase
  end

  private

  def secret
    "hidden"
  end
end

def upcase_all
  ["a", "b"].map(&:upcase)
end

def select_odd
  [1, 2, 3, 4].select(&:odd?)
end

def reduce_plus
  [1, 2, 3].reduce(&:+)
end

def reduce_star
  [1, 2, 3, 4].reduce(&:*)
end

def typed_accessors
  [User.new("Ada", 36), User.new("Grace", 45)].map(&:name)
end

def method_member
  [User.new("Ada", 36)].map(&:shout)
end

def private_member
  [User.new("Ada", 36)].map(&:secret)
end

def symbol_variable
  op = :upcase
  ["x"].map(&op)
end

def missing_member
  [1].map(&:no_such_member)
end`)

	ctx := context.Background()
	compareArrays(t, callScript(t, ctx, script, "upcase_all", nil, CallOptions{}), []Value{NewString("A"), NewString("B")})
	compareArrays(t, callScript(t, ctx, script, "select_odd", nil, CallOptions{}), []Value{NewInt(1), NewInt(3)})
	if got := callScript(t, ctx, script, "reduce_plus", nil, CallOptions{}); got.Int() != 6 {
		t.Fatalf("reduce_plus = %v, want 6", got)
	}
	if got := callScript(t, ctx, script, "reduce_star", nil, CallOptions{}); got.Int() != 24 {
		t.Fatalf("reduce_star = %v, want 24", got)
	}
	compareArrays(t, callScript(t, ctx, script, "typed_accessors", nil, CallOptions{}), []Value{NewString("Ada"), NewString("Grace")})
	compareArrays(t, callScript(t, ctx, script, "method_member", nil, CallOptions{}), []Value{NewString("ADA")})
	compareArrays(t, callScript(t, ctx, script, "symbol_variable", nil, CallOptions{}), []Value{NewString("X")})

	err := callScriptErr(t, ctx, script, "private_member", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "private method secret") {
		t.Fatalf("private_member error = %v, want private method secret", err)
	}
	err = callScriptErr(t, ctx, script, "missing_member", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "no_such_member") {
		t.Fatalf("missing_member error = %v, want unknown member", err)
	}
}

// TestFunctionForwarding pins `&fn` forwarding of function values and the
// misuse error for non-callable block arguments.
func TestFunctionForwarding(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def double(x)
  x * 2
end

def map_function
  [1, 2, 3].map(&double)
end

def call_it
  yield 4
end

def yield_function
  call_it(&double)
end

def non_callable
  [1].map(&12)
end

def arity_mismatch
  call_two(&double)
end

def call_two
  yield 1, 2, 3
end`)

	ctx := context.Background()
	compareArrays(t, callScript(t, ctx, script, "map_function", nil, CallOptions{}), []Value{NewInt(2), NewInt(4), NewInt(6)})
	if got := callScript(t, ctx, script, "yield_function", nil, CallOptions{}); got.Int() != 8 {
		t.Fatalf("yield_function = %v, want 8", got)
	}
	err := callScriptErr(t, ctx, script, "non_callable", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "block argument must be a block, function, or symbol, got int") {
		t.Fatalf("non_callable error = %v, want block argument type error", err)
	}
	// A forwarded function keeps method arity: yielding surplus arguments
	// reports the function's own binding error instead of padding.
	err = callScriptErr(t, ctx, script, "arity_mismatch", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("arity_mismatch error = %v, want unexpected positional arguments", err)
	}
}

// TestLambdaConversionKeepsSourceProc pins the Kernel#lambda conversion
// contract chosen for Vibescript: lambda(&existing_proc) returns a
// lambda-semantics copy and leaves the original proc untouched (Ruby 3.3
// raises for the non-literal form instead; converting keeps the older
// contract without mutating the source).
func TestLambdaConversionKeepsSourceProc(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def convert
  pr = proc { |a, b| [a, b] }
  lam = lambda(&pr)
  [lam.lambda?, pr.lambda?]
end

def proc_of_lambda
  lam = ->(a) { a }
  proc(&lam).lambda?
end`)

	ctx := context.Background()
	got := callScript(t, ctx, script, "convert", nil, CallOptions{})
	compareArrays(t, got, []Value{NewBool(true), NewBool(false)})
	if got := callScript(t, ctx, script, "proc_of_lambda", nil, CallOptions{}); !got.Truthy() {
		t.Fatalf("proc_of_lambda = %v, want true (proc(&lambda) returns the lambda unchanged)", got)
	}
}
