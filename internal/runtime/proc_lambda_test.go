package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestProcLambdaLiteralForms pins the three Ruby-style callable constructors:
// Proc.new { }, lambda { }, and the stabby lambda ->() { }. Each produces a
// value invocable with .call.
func TestProcLambdaLiteralForms(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def proc_new
  fn = Proc.new do |n|
    n + 1
  end
  fn.call(1)
end

def kernel_lambda
  fn = lambda do |n|
    n + 1
  end
  fn.call(1)
end

def stabby
  fn = ->(n) { n + 1 }
  fn.call(1)
end

def stabby_do
  fn = ->(a, b) do
    a * b
  end
  fn.call(6, 7)
end

def stabby_no_params
  fn = -> { 42 }
  fn.call
end

def stabby_implicit
  fn = -> { it * 2 }
  fn.call(21)
end

def lambda_predicate
  [->(x) { x }.lambda?, lambda { 1 }.lambda?, proc { 1 }.lambda?, Proc.new { 1 }.lambda?]
end

def typed_boundary(f: Function, x)
  f.call(x)
end

def through_typed_boundary
  typed_boundary(->(n) { n * 2 }, 21)
end`)

	ctx := context.Background()
	cases := []struct {
		fn   string
		want int64
	}{
		{fn: "proc_new", want: 2},
		{fn: "kernel_lambda", want: 2},
		{fn: "stabby", want: 2},
		{fn: "stabby_do", want: 42},
		{fn: "stabby_no_params", want: 42},
		{fn: "stabby_implicit", want: 42},
		{fn: "through_typed_boundary", want: 42},
	}
	for _, tc := range cases {
		if got := callScript(t, ctx, script, tc.fn, nil, CallOptions{}); got.Kind() != KindInt || got.Int() != tc.want {
			t.Fatalf("%s = %v, want %d", tc.fn, got, tc.want)
		}
	}
	preds := callScript(t, ctx, script, "lambda_predicate", nil, CallOptions{})
	compareArrays(t, preds, []Value{NewBool(true), NewBool(true), NewBool(false), NewBool(false)})
}

// TestLambdaStrictArity pins lambda arity enforcement: unlike procs and
// blocks, a lambda rejects both missing and surplus arguments.
func TestLambdaStrictArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def missing
  ->(a, b) { a + b }.call(1)
end

def surplus
  ->(a) { a }.call(1, 2)
end

def zero_gets_one
  -> { 1 }.call(2)
end

def one_argument_missing
  ->(a) { a }.call
end

def no_autosplat
  ->(a, b) { [a, b] }.call([1, 2])
end

def proc_pads
  pr = proc { |a, b| [a, b] }
  pr.call(1)
end

def proc_truncates
  pr = proc { |a| a }
  pr.call(1, 2)
end

def proc_autosplats
  pr = proc { |a, b| [a, b] }
  pr.call([1, 2])
end`)

	ctx := context.Background()
	arityErrors := []struct {
		fn   string
		want string
	}{
		{fn: "missing", want: "lambda expects 2 arguments, got 1"},
		{fn: "surplus", want: "lambda expects 1 argument, got 2"},
		{fn: "zero_gets_one", want: "lambda expects 0 arguments, got 1"},
		{fn: "one_argument_missing", want: "lambda expects 1 argument, got 0"},
		{fn: "no_autosplat", want: "lambda expects 2 arguments, got 1"},
	}
	for _, tc := range arityErrors {
		err := callScriptErr(t, ctx, script, tc.fn, nil, CallOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want substring %q", tc.fn, err, tc.want)
		}
	}

	pads := callScript(t, ctx, script, "proc_pads", nil, CallOptions{})
	compareArrays(t, pads, []Value{NewInt(1), NewNil()})
	if got := callScript(t, ctx, script, "proc_truncates", nil, CallOptions{}); got.Int() != 1 {
		t.Fatalf("proc_truncates = %v, want 1", got)
	}
	splat := callScript(t, ctx, script, "proc_autosplats", nil, CallOptions{})
	compareArrays(t, splat, []Value{NewInt(1), NewInt(2)})
}

// TestLambdaLocalControlFlow pins Ruby's lambda semantics: return, break, and
// next in a lambda body all end the lambda call with a value, never the
// enclosing method. A block nested in the lambda body homes its return to the
// lambda invocation.
func TestLambdaLocalControlFlow(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def lambda_return
  fn = -> { return 10 }
  fn.call + 1
end

def lambda_break
  fn = -> { break 5 }
  fn.call + 1
end

def lambda_next
  fn = -> { next 7 }
  fn.call + 1
end

def lambda_bare_break
  fn = -> { break }
  fn.call
end

def nested_block_return
  fn = -> {
    [1, 2, 3].each do |x|
      if x == 2
        return x * 100
      end
    end
    0
  }
  fn.call + 1
end

def lambda_loop_break
  fn = -> {
    total = 0
    while true
      total = total + 1
      if total == 3
        break
      end
    end
    total
  }
  fn.call
end`)

	ctx := context.Background()
	cases := []struct {
		fn   string
		want Value
	}{
		{fn: "lambda_return", want: NewInt(11)},
		{fn: "lambda_break", want: NewInt(6)},
		{fn: "lambda_next", want: NewInt(8)},
		{fn: "lambda_bare_break", want: NewNil()},
		{fn: "nested_block_return", want: NewInt(201)},
		{fn: "lambda_loop_break", want: NewInt(3)},
	}
	for _, tc := range cases {
		got := callScript(t, ctx, script, tc.fn, nil, CallOptions{})
		if got.Inspect() != tc.want.Inspect() {
			t.Fatalf("%s = %s, want %s", tc.fn, got.Inspect(), tc.want.Inspect())
		}
	}
}

// TestProcNonLocalControlFlow pins proc semantics: a proc keeps block
// behavior, so return unwinds the method whose body created it, and a proc
// whose home frame is gone raises LocalJumpError.
func TestProcNonLocalControlFlow(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def proc_return
  pr = proc { return 10 }
  pr.call
  99
end

def proc_new_return
  pr = Proc.new { return 11 }
  pr.call
  99
end

def make_proc
  proc { return 1 }
end

def dead_frame
  pr = make_proc
  pr.call
end

def dead_frame_rescued
  pr = make_proc
  begin
    pr.call
    "no error"
  rescue LocalJumpError
    "rescued"
  end
end

def proc_break
  pr = proc { break 5 }
  pr.call
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "proc_return", nil, CallOptions{}); got.Int() != 10 {
		t.Fatalf("proc_return = %v, want 10", got)
	}
	if got := callScript(t, ctx, script, "proc_new_return", nil, CallOptions{}); got.Int() != 11 {
		t.Fatalf("proc_new_return = %v, want 11", got)
	}
	err := callScriptErr(t, ctx, script, "dead_frame", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected return") {
		t.Fatalf("dead_frame error = %v, want unexpected return (LocalJumpError)", err)
	}
	if got := callScript(t, ctx, script, "dead_frame_rescued", nil, CallOptions{}); got.String() != "rescued" {
		t.Fatalf("dead_frame_rescued = %v, want rescued", got)
	}
	// Vibescript restricts break to loops and lambda bodies, so a proc body
	// reports the block-break error when invoked (Ruby raises LocalJumpError
	// for a proc break outside iteration; the failure mode matches in spirit).
	err = callScriptErr(t, ctx, script, "proc_break", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "break cannot cross call boundary") {
		t.Fatalf("proc_break error = %v, want break used outside of loop", err)
	}
}

// TestProcLambdaConstructorMisuse pins the errors for building callables
// without a block and for lambdas invoked with keyword arguments.
func TestProcLambdaConstructorMisuse(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def proc_new_without_block
  Proc.new
end

def proc_without_block
  proc
end

def lambda_without_block
  lambda
end

def lambda_kwargs
  ->(a) { a }.call(a: 1)
end

def typed_lambda_param
  ->(x: Int) { x + 1 }.call("nope")
end`)

	ctx := context.Background()
	cases := []struct {
		fn   string
		want string
	}{
		{fn: "proc_new_without_block", want: "tried to create a Proc object without a block"},
		{fn: "proc_without_block", want: "tried to create a Proc object without a block"},
		{fn: "lambda_without_block", want: "tried to create a lambda without a block"},
		{fn: "lambda_kwargs", want: "unexpected keyword argument a"},
		{fn: "typed_lambda_param", want: "argument x expected int, got string"},
	}
	for _, tc := range cases {
		err := callScriptErr(t, ctx, script, tc.fn, nil, CallOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want substring %q", tc.fn, err, tc.want)
		}
	}
}
