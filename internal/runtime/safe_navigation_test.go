package runtime

import "testing"

// TestSafeNavigation covers Ruby-style safe navigation (`receiver&.member`):
// when the receiver is nil the expression short-circuits to nil, otherwise it
// dispatches like the corresponding ordinary member access or method call.
func TestSafeNavigation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Person
      property name

      def initialize(name)
        @name = name
      end

      def greet(prefix)
        prefix + " " + @name
      end
    end

    def member_read_nil
      user = nil
      user&.name
    end

    def member_read_present
      user = Person.new("Ada")
      user&.name
    end

    def method_call_nil
      user = nil
      user&.greet("hi")
    end

    def method_call_present
      user = Person.new("Ada")
      user&.greet("hi")
    end

    def builtin_call_nil
      value = nil
      value&.upcase
    end

    def builtin_call_present
      value = "ada"
      value&.upcase
    end

    def hash_member_nil
      data = nil
      data&.size
    end

    def hash_member_present
      data = {a: 1, b: 2}
      data&.size
    end

    def chain_short_circuits
      user = nil
      user&.name&.upcase
    end

    def chain_present
      user = Person.new("ada")
      user&.name&.upcase
    end

    def parenless_call_nil
      user = nil
      user&.greet "hi"
    end

    def parenless_call_present
      user = Person.new("Ada")
      user&.greet "hi"
    end
  `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "member read on nil yields nil", fn: "member_read_nil", want: NewNil()},
		{name: "member read on receiver dispatches", fn: "member_read_present", want: NewString("Ada")},
		{name: "method call on nil yields nil", fn: "method_call_nil", want: NewNil()},
		{name: "method call on receiver dispatches", fn: "method_call_present", want: NewString("hi Ada")},
		{name: "builtin call on nil yields nil", fn: "builtin_call_nil", want: NewNil()},
		{name: "builtin call on receiver dispatches", fn: "builtin_call_present", want: NewString("ADA")},
		{name: "hash member on nil yields nil", fn: "hash_member_nil", want: NewNil()},
		{name: "hash member on receiver dispatches", fn: "hash_member_present", want: NewInt(2)},
		{name: "chain short-circuits at first nil", fn: "chain_short_circuits", want: NewNil()},
		{name: "chain dispatches through receivers", fn: "chain_present", want: NewString("ADA")},
		{name: "parenless call on nil yields nil", fn: "parenless_call_nil", want: NewNil()},
		{name: "parenless call on receiver dispatches", fn: "parenless_call_present", want: NewString("hi Ada")},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestSafeNavigationSkipsArgumentsAndBlock verifies that a short-circuited safe
// call evaluates neither its arguments nor its block, matching Ruby, where
// `nil&.foo(bar)` never evaluates bar.
func TestSafeNavigationSkipsArgumentsAndBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def boom
      raise "argument should not be evaluated"
    end

    def skips_arguments
      user = nil
      user&.profile(boom)
    end

    def skips_block
      user = nil
      user&.each do |item|
        raise "block should not be evaluated"
      end
    end
  `)

	for _, fn := range []string{"skips_arguments", "skips_block"} {
		fn := fn
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, fn, nil); got.Kind() != KindNil {
				t.Fatalf("%s() = %#v, want nil", fn, got)
			}
		})
	}
}

// TestSafeNavigationDoesNotGuardSubsequentChain documents that `&.` only guards
// its immediate access: a non-safe access later in the chain still errors when
// its own receiver is nil, matching Ruby (`a&.b.c` raises when `a&.b` is nil).
func TestSafeNavigationDoesNotGuardSubsequentChain(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def unsafe_tail
      user = nil
      user&.profile.name
    end
  `)
	requireCallErrorContains(t, script, "unsafe_tail", nil, CallOptions{}, "nil")
}
