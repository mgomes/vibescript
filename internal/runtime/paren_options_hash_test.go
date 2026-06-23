package runtime

import (
	"context"
	"testing"
)

// TestParenthesizedFunctionOptionsHash verifies that a parenthesized plain
// function call collapses its keyword arguments into a trailing positional
// options hash on the same terms as the parenless form, mirroring Ruby's
// options-hash binding.
func TestParenthesizedFunctionOptionsHash(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def configure(opts)
      opts[:retries]
    end

    def typed_configure(opts: { retries: int })
      opts[:retries]
    end

    def configure_with_prefix(prefix, opts)
      prefix + opts[:suffix]
    end

    def keyword_signature(retries:)
      retries
    end

    def positional_and_keyword(opts, name:)
      [opts, name]
    end

    def parenless_call
      configure retries: 3
    end

    def parenthesized_call
      configure(retries: 3)
    end

    def parenthesized_typed_call
      typed_configure(retries: 3)
    end

    def parenthesized_prefix_call
      configure_with_prefix("pre", suffix: "fix")
    end

    def parenthesized_keyword_binding
      keyword_signature(retries: 7)
    end

    def parenthesized_keyword_signature
      positional_and_keyword(retry: true, name: "Ada")
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "parenless", fn: "parenless_call", want: NewInt(3)},
		{name: "parenthesized", fn: "parenthesized_call", want: NewInt(3)},
		{name: "parenthesized typed shape", fn: "parenthesized_typed_call", want: NewInt(3)},
		{name: "parenthesized prefix and options", fn: "parenthesized_prefix_call", want: NewString("prefix")},
		{name: "parenthesized keyword parameter binds directly", fn: "parenthesized_keyword_binding", want: NewInt(7)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := callFunc(t, script, tc.fn, nil); !got.Equal(tc.want) {
				t.Fatalf("%s() = %v, want %v", tc.fn, got, tc.want)
			}
		})
	}

	// A function with a keyword parameter does not absorb a mismatched keyword
	// into the positional options parameter, so the unfilled positional reports
	// a missing argument just as the parenless form does.
	requireCallErrorContains(t, script, "parenthesized_keyword_signature", nil, CallOptions{}, "missing argument opts")
}

// TestParenthesizedPositionalAfterKeywordRejected verifies that a parenthesized
// call rejects a positional argument that follows a keyword argument for both
// the direct-call and function-value (`call` alias) forms, matching Ruby (which
// treats `f(a: 1, 2)` as a syntax error) and the parenless form. Without the
// rejection the synthesized options hash would be appended after the trailing
// positional, silently mis-binding `opts = "tail"` and `value = {first: 1}`.
func TestParenthesizedPositionalAfterKeywordRejected(t *testing.T) {
	t.Parallel()

	const want = "positional arguments cannot follow keyword arguments"

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "direct call",
			source: `
    def collect(opts, value)
      [opts, value]
    end

    def run
      collect(first: 1, "tail")
    end
    `,
		},
		{
			name: "function value call alias",
			source: `
    def collect(opts, value)
      [opts, value]
    end

    def run
      collect.call(first: 1, "tail")
    end
    `,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireCompileErrorContainsDefault(t, tc.source, want)
		})
	}
}

// TestParenthesizedFunctionOptionsHashTypeMismatch verifies that type
// validation runs against the synthesized options hash, so a shape mismatch is
// rejected with the type error rather than a missing-argument error.
func TestParenthesizedFunctionOptionsHashTypeMismatch(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def configure(opts: { retries: int })
      opts[:retries]
    end

    def bad_shape
      configure(retries: "slow")
    end
    `)

	requireCallErrorContains(t, script, "bad_shape", nil, CallOptions{}, "expected { retries: int }")
}

// TestParenthesizedConstructorOptionsHashStaysStrict guards the boundary with
// issue #576: parenthesized constructor calls keep strict keyword binding, so
// they continue to report a missing positional argument rather than collapsing
// into an options hash.
func TestParenthesizedConstructorOptionsHashStaysStrict(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Person
      def initialize(opts)
        @name = opts[:name]
      end

      def name
        @name
      end
    end

    def parenthesized_constructor
      Person.new(name: "Ada")
    end
    `)

	requireCallErrorContains(t, script, "parenthesized_constructor", nil, CallOptions{}, "missing argument opts")
}

// TestParenthesizedMethodOptionsHashStaysStrict verifies that parenthesized
// method calls remain strict; only plain function calls are realigned by issue
// #589.
func TestParenthesizedMethodOptionsHashStaysStrict(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Server
      def configure(opts)
        opts[:retries]
      end
    end

    def parenthesized_method
      Server.new.configure(retries: 3)
    end
    `)

	requireCallErrorContains(t, script, "parenthesized_method", nil, CallOptions{}, "missing argument opts")
}

// TestFunctionCallAliasOptionsHashParity verifies that invoking an
// options-taking function through its `call` alias collapses keyword arguments
// into a positional options hash for both call forms, matching direct
// invocation. This guards the direct-call/`fn.call` parity documented for
// function values.
func TestFunctionCallAliasOptionsHashParity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def configure(opts)
      opts[:retries]
    end

    def typed_configure(opts: { retries: int })
      opts[:retries]
    end

    def direct_call
      configure(retries: 3)
    end

    def parenthesized_call_alias
      configure.call(retries: 3)
    end

    def parenless_call_alias
      configure.call retries: 3
    end

    def typed_call_alias
      typed_configure.call(retries: 3)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "direct call", fn: "direct_call", want: NewInt(3)},
		{name: "parenthesized call alias", fn: "parenthesized_call_alias", want: NewInt(3)},
		{name: "parenless call alias", fn: "parenless_call_alias", want: NewInt(3)},
		{name: "typed call alias", fn: "typed_call_alias", want: NewInt(3)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := callFunc(t, script, tc.fn, nil); !got.Equal(tc.want) {
				t.Fatalf("%s() = %v, want %v", tc.fn, got, tc.want)
			}
		})
	}
}

// TestFunctionCallAliasOptionsHashTypeMismatch verifies that type validation
// runs against the synthesized options hash when binding through the `call`
// alias, rejecting a shape mismatch with the type error rather than a
// missing-argument error.
func TestFunctionCallAliasOptionsHashTypeMismatch(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def configure(opts: { retries: int })
      opts[:retries]
    end

    def bad_shape
      configure.call(retries: "slow")
    end
    `)

	requireCallErrorContains(t, script, "bad_shape", nil, CallOptions{}, "expected { retries: int }")
}

// TestParenthesizedMethodNamedCallStaysStrict guards the boundary between a
// function value's `call` alias and an instance method named `call`. A
// parenthesized call to the method must stay strict, while the parenless form
// still collapses into an options hash like any other method call.
func TestParenthesizedMethodNamedCallStaysStrict(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Server
      def call(opts)
        opts[:retries]
      end
    end

    def parenthesized_method_call
      Server.new.call(retries: 3)
    end

    def parenless_method_call
      Server.new.call retries: 3
    end
    `)

	requireCallErrorContains(t, script, "parenthesized_method_call", nil, CallOptions{}, "missing argument opts")

	if got := callFunc(t, script, "parenless_method_call", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("parenless_method_call() = %v, want %v", got, NewInt(3))
	}
}

// TestMemberHeldFunctionOptionsHashParity verifies that a plain function value
// reached through member access (such as a module function exported on a
// namespace object) collapses parenthesized keyword arguments into a positional
// options hash, matching both the direct call form and the parenless member
// form. The member access surfaces a bare function value, not a genuine method,
// so it must behave like any other plain function call. This guards issue #589's
// member-held function path.
func TestMemberHeldFunctionOptionsHashParity(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
    def direct
      helpers = require("options_helper")
      helpers.configure(retries: 3)
    end

    def parenless
      helpers = require("options_helper")
      helpers.configure retries: 3
    end

    def typed_member
      helpers = require("options_helper")
      helpers.typed_configure(retries: 7)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "parenthesized member call", fn: "direct", want: NewInt(3)},
		{name: "parenless member call", fn: "parenless", want: NewInt(3)},
		{name: "typed member call", fn: "typed_member", want: NewInt(7)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := callScript(t, context.Background(), script, tc.fn, nil, CallOptions{}); !got.Equal(tc.want) {
				t.Fatalf("%s() = %v, want %v", tc.fn, got, tc.want)
			}
		})
	}
}

// TestMemberHeldFunctionOptionsHashTypeMismatch verifies that type validation
// runs against the synthesized options hash when collapsing keyword arguments
// for a member-held function value, rejecting a shape mismatch with the type
// error rather than a missing-argument error.
func TestMemberHeldFunctionOptionsHashTypeMismatch(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
    def bad_shape
      helpers = require("options_helper")
      helpers.typed_configure(retries: "slow")
    end
    `)

	requireCallErrorContains(t, script, "bad_shape", nil, CallOptions{}, "expected { retries: int }")
}

// TestParenthesizedGenuineMethodStaysStrictWhenNamedLikeMember guards the other
// side of the member-held function distinction: a genuine instance method must
// keep strict keyword binding for a parenthesized call even though the member
// path surfaces methods as bare function values, while the parenless form still
// collapses into an options hash like any method call.
func TestParenthesizedGenuineMethodStaysStrictWhenNamedLikeMember(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Server
      def configure(opts)
        opts[:retries]
      end
    end

    def parenthesized_method
      Server.new.configure(retries: 3)
    end

    def parenless_method
      Server.new.configure retries: 3
    end
    `)

	requireCallErrorContains(t, script, "parenthesized_method", nil, CallOptions{}, "missing argument opts")

	if got := callFunc(t, script, "parenless_method", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("parenless_method() = %v, want %v", got, NewInt(3))
	}
}
