package runtime

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestFunctionValueCall covers Ruby-style fn.call(...) on script function
// values, which must mirror direct fn(...) invocation including args,
// kwargs, and block forwarding.
func TestFunctionValueCall(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inc(n)
      n + 1
    end

    def greet(name:)
      "hello " + name
    end

    def apply(x)
      yield(x)
    end

    def call_positional(fn)
      fn.call(2)
    end

    def direct_positional(fn)
      fn(2)
    end

    def call_keyword(fn)
      fn.call(name: "Ada")
    end

    def call_block(fn)
      fn.call(10) do |value|
        value * 3
      end
    end

    def run_positional
      call_positional(inc)
    end

    def run_direct
      direct_positional(inc)
    end

    def run_keyword
      call_keyword(greet)
    end

    def run_block
      call_block(apply)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "positional parity with direct call", fn: "run_positional", want: NewInt(3)},
		{name: "direct call control", fn: "run_direct", want: NewInt(3)},
		{name: "keyword forwarding", fn: "run_keyword", want: NewString("hello Ada")},
		{name: "block forwarding", fn: "run_block", want: NewInt(30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestFunctionValueCallZeroArityFollowsIssue416 documents that fn.call does
// not change the zero-arity behavior tracked by #416: a bare reference to a
// zero-arity function is still auto-invoked, so the callee receives the return
// value, not a function value. Obtaining a zero-arity function as a callable
// value (so it could be reached by fn.call) is out of scope here and tracked
// by #416.
func TestFunctionValueCallZeroArityFollowsIssue416(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def receive(value)
      value
    end

    def run
      receive(answer)
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42 (zero-arity function auto-invoked per #416)", got)
	}
}

func TestZeroArityFunctionValuePreservedForFunctionTypedArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def receive_untyped(value)
      value
    end

    def receive_callable(fn: function)
      fn.call
    end

    def receive_callable_rest(*fns: array<function>)
      fns[0].call
    end

    def feed_block(&block)
      block.call(answer)
    end

    def run_untyped
      receive_untyped(answer)
    end

    def run_typed
      receive_callable(answer)
    end

    def run_call_alias
      receive_callable.call(answer)
    end

    def run_rest
      receive_callable_rest(answer)
    end

    def run_block_param
      feed_block do |fn: function|
        fn.call
      end
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "untyped still auto invokes", fn: "run_untyped", want: NewInt(42)},
		{name: "typed positional keeps callable", fn: "run_typed", want: NewInt(42)},
		{name: "function call alias keeps callable", fn: "run_call_alias", want: NewInt(42)},
		{name: "typed rest keeps callable element", fn: "run_rest", want: NewInt(42)},
		{name: "typed block call parameter keeps callable", fn: "run_block_param", want: NewInt(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

func TestCapturedBlockValuesAreCallable(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def invoke_direct(&block)
      block(2)
    end

    def invoke_call(&block)
      block.call(3)
    end

    def accept_typed_block(&block: function)
      yield(4)
    end

    def forward_typed_block(&block: function)
      require_callable(block)
    end

    def require_callable(fn: function)
      fn.call(5)
    end

    def run_direct
      invoke_direct do |value|
        value + 1
      end
    end

    def run_call
      invoke_call do |value|
        value * 2
      end
    end

    def run_typed_block
      accept_typed_block do |value|
        value + 6
      end
    end

    def run_forwarded_block
      forward_typed_block do |value|
        value + 7
      end
    end

    def run_respond_to
      capture_respond_to do
        1
      end
    end

    def capture_respond_to(&block)
      block.respond_to?(:call)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "direct call", fn: "run_direct", want: NewInt(3)},
		{name: "call member", fn: "run_call", want: NewInt(6)},
		{name: "typed block parameter", fn: "run_typed_block", want: NewInt(10)},
		{name: "block value satisfies function type", fn: "run_forwarded_block", want: NewInt(12)},
		{name: "respond_to call", fn: "run_respond_to", want: NewBool(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestFunctionValueCallErrors verifies that misuse of fn.call surfaces the
// same argument and type errors as direct invocation, anchored at the call
// site, and that unknown members suggest call.
func TestFunctionValueCallErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		errMsg string
	}{
		{
			name: "too many positional arguments",
			source: `
        def inc(n)
          n + 1
        end

        def run(fn)
          fn.call(1, 2)
        end
      `,
			errMsg: "unexpected positional arguments",
		},
		{
			name: "argument type mismatch",
			source: `
        def inc(n: int)
          n + 1
        end

        def run(fn)
          fn.call("x")
        end
      `,
			errMsg: "argument n expected int, got string",
		},
		{
			name: "missing required keyword",
			source: `
        def greet(name:)
          "hello " + name
        end

        def run(fn)
          fn.call()
        end
      `,
			errMsg: "missing keyword argument name",
		},
		{
			name: "unknown member suggests call",
			source: `
        def inc(n)
          n + 1
        end

        def run(fn)
          fn.cll(1)
        end
      `,
			errMsg: `unknown member cll (did you mean "call"?)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", []Value{exportedFunctionValue(t, script, "inc", "greet")}, CallOptions{}, tt.errMsg)
		})
	}
}

// exportedFunctionValue resolves the first of the named functions defined in
// the script to a function value, so error-path tests can pass it straight
// to a run helper without an extra wrapper.
func exportedFunctionValue(t *testing.T, script *Script, names ...string) Value {
	t.Helper()
	for _, name := range names {
		if fn, ok := script.functions[name]; ok {
			return NewFunction(fn)
		}
	}
	t.Fatalf("none of %v defined in script", names)
	return NewNil()
}

// TestFunctionValueCallMemberSuggestion confirms the function member list is
// wired into editor completion metadata. The list carries the function-specific
// call member alongside the universal Object-level helpers (itself, nil?, eql?,
// equal?, tap, yield_self) and the introspection predicates (respond_to?, is_a?,
// kind_of?, instance_of?) exposed on every value kind.
func TestFunctionValueCallMemberSuggestion(t *testing.T) {
	t.Parallel()
	names, ok := MemberCompletionNames()["function"]
	if !ok {
		t.Fatalf("MemberCompletionNames missing function entry")
	}
	want := append([]string{"call"}, universalMemberNames...)
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("function member completion mismatch (-want +got):\n%s", diff)
	}
}

func TestBlockValueCallMemberSuggestion(t *testing.T) {
	t.Parallel()
	names, ok := MemberCompletionNames()["block"]
	if !ok {
		t.Fatalf("MemberCompletionNames missing block entry")
	}
	want := append([]string{"call"}, universalMemberNames...)
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("block member completion mismatch (-want +got):\n%s", diff)
	}
}
