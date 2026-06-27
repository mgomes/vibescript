package runtime

import (
	"slices"
	"testing"
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
// wired into editor completion metadata, including the universal Object-level
// helpers that resolve on every value.
func TestFunctionValueCallMemberSuggestion(t *testing.T) {
	t.Parallel()
	names, ok := MemberCompletionNames()["function"]
	if !ok {
		t.Fatalf("MemberCompletionNames missing function entry")
	}
	want := append([]string{"call"}, universalMemberNames...)
	if !slices.Equal(names, want) {
		t.Fatalf("function member completion = %v, want %v", names, want)
	}
}
