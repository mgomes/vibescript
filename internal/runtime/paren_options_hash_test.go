package runtime

import "testing"

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
