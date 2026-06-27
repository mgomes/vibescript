package runtime

import (
	"context"
	"testing"
)

// TestBlockGivenReportsBlockPresence exercises Ruby-style block_given? across
// plain functions, instance methods, class methods, nested calls, blocks, and
// the parenthesized call form. Each scenario is driven by a wrapper function
// that calls the optional-block API with and without a block.
func TestBlockGivenReportsBlockPresence(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def optional
      block_given?
    end

    def optional_paren
      block_given?()
    end

    def yields_when_given
      if block_given?
        yield "yielded"
      else
        "no block"
      end
    end

    def outer_with_inner
      [block_given?, optional]
    end

    def block_given_inside_block
      [1].map do |_|
        block_given?
      end
    end

    class Greeter
      def greet
        block_given?
      end

      def self.build
        block_given?
      end
    end

    def plain_without
      optional
    end

    def plain_with
      optional { 1 }
    end

    def paren_without
      optional_paren
    end

    def paren_with
      optional_paren { 1 }
    end

    def yield_branch_without
      yields_when_given
    end

    def yield_branch_with
      yields_when_given { |m| m }
    end

    def nested_without
      outer_with_inner
    end

    def nested_with
      outer_with_inner { 1 }
    end

    def inside_block_without
      block_given_inside_block
    end

    def inside_block_with
      block_given_inside_block { 1 }
    end

    def instance_without
      Greeter.new.greet
    end

    def instance_with
      Greeter.new.greet { 1 }
    end

    def class_method_without
      Greeter.build
    end

    def class_method_with
      Greeter.build { 1 }
    end

    def top_level_without
      block_given?
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "plain function without block", fn: "plain_without", want: NewBool(false)},
		{name: "plain function with block", fn: "plain_with", want: NewBool(true)},
		{name: "paren form without block", fn: "paren_without", want: NewBool(false)},
		{name: "paren form with block", fn: "paren_with", want: NewBool(true)},
		{name: "yield branch falls through without block", fn: "yield_branch_without", want: NewString("no block")},
		{name: "yield branch yields with block", fn: "yield_branch_with", want: NewString("yielded")},
		{name: "instance method without block", fn: "instance_without", want: NewBool(false)},
		{name: "instance method with block", fn: "instance_with", want: NewBool(true)},
		{name: "class method without block", fn: "class_method_without", want: NewBool(false)},
		{name: "class method with block", fn: "class_method_with", want: NewBool(true)},
		{
			name: "nested call does not inherit caller block",
			fn:   "nested_with",
			want: NewArray([]Value{NewBool(true), NewBool(false)}),
		},
		{
			name: "nested call without caller block",
			fn:   "nested_without",
			want: NewArray([]Value{NewBool(false), NewBool(false)}),
		},
		{
			name: "block body reflects enclosing method without block",
			fn:   "inside_block_without",
			want: NewArray([]Value{NewBool(false)}),
		},
		{
			name: "block body reflects enclosing method with block",
			fn:   "inside_block_with",
			want: NewArray([]Value{NewBool(true)}),
		},
		{name: "top-level method without block is false", fn: "top_level_without", want: NewBool(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callScript(t, context.Background(), script, tt.fn, nil, CallOptions{})
			assertValueEqual(t, got, tt.want)
		})
	}
}

// TestBlockGivenRejectsArguments verifies the parenthesized call form mirrors
// Ruby by refusing positional arguments, keyword arguments, and a block.
func TestBlockGivenRejectsArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "positional argument",
			source: "def run\n  block_given?(1)\nend\n",
			want:   "block_given? takes no arguments",
		},
		{
			name:   "keyword argument",
			source: "def run\n  block_given?(flag: true)\nend\n",
			want:   "block_given? takes no arguments",
		},
		{
			name:   "block argument",
			source: "def run\n  block_given? { 1 }\nend\n",
			want:   "block_given? does not accept a block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

// TestBlockGivenNotShadowableByMember confirms member access keeps its normal
// lookup so block_given? is only the Kernel predicate as a bare call.
func TestBlockGivenNotShadowableByMember(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run\n  \"hi\".block_given?\nend\n")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown string method block_given?")
}
