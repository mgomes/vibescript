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

// TestBlockGivenIgnoresShadowingBindings confirms a script binding cannot spoof
// or suppress the call's block. block_given? and yield read the call frame's
// dedicated slot, so a parameter, local, or block parameter named __block__ is
// just an ordinary variable and never alters the predicate or yield target.
func TestBlockGivenIgnoresShadowingBindings(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def param_spoofs(__block__)
      block_given?
    end

    def local_spoofs
      __block__ = 1
      block_given?
    end

    def yields_value
      yield 7
    end

    def param_does_not_suppress_yield(__block__)
      yields_value { |n| n + __block__ }
    end

    def block_param_spoofs
      [1].map do |__block__|
        block_given?
      end
    end

    def spoof_param_without
      param_spoofs(1)
    end

    def spoof_param_with
      param_spoofs(1) { 1 }
    end

    def spoof_local_without
      local_spoofs
    end

    def spoof_local_with
      local_spoofs { 1 }
    end

    def yield_through_param
      param_does_not_suppress_yield(10)
    end

    def block_param_without
      block_param_spoofs
    end

    def block_param_with
      block_param_spoofs { 1 }
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "argument named __block__ does not fake a block", fn: "spoof_param_without", want: NewBool(false)},
		{name: "real block survives an argument named __block__", fn: "spoof_param_with", want: NewBool(true)},
		{name: "local named __block__ does not fake a block", fn: "spoof_local_without", want: NewBool(false)},
		{name: "real block survives a local named __block__", fn: "spoof_local_with", want: NewBool(true)},
		{name: "argument named __block__ does not suppress yield", fn: "yield_through_param", want: NewInt(17)},
		{name: "block parameter named __block__ does not fake a block", fn: "block_param_without", want: NewArray([]Value{NewBool(false)})},
		{name: "real block survives a block parameter named __block__", fn: "block_param_with", want: NewArray([]Value{NewBool(true)})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callScript(t, context.Background(), script, tt.fn, nil, CallOptions{})
			assertValueEqual(t, got, tt.want)
		})
	}
}

// TestBlockGivenSurvivesHostCloneInEscapedDefaultProc pins that a hash default
// proc which captures the block of the method that produced it keeps that block
// across the host boundary. The proc escapes one Script.Call (so its captured
// environment is host-cloned on export) and is re-entered through another, where
// a missing-key lookup runs it. Cloning the env for the host previously dropped
// the call frame's hidden block slot, so block_given? flipped to false on
// re-entry; this matches Ruby, where a block defined in a method called with a
// block sees block_given? as true.
func TestBlockGivenSurvivesHostCloneInEscapedDefaultProc(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def make
  Hash.new { |_, _| block_given? }
end

def export_with_block
  make { 1 }
end

def export_without_block
  make
end

def lookup(h, k)
  h[k]
end
`)

	tests := []struct {
		name     string
		exporter string
		want     Value
	}{
		{name: "producing method called with a block", exporter: "export_with_block", want: NewBool(true)},
		{name: "producing method called without a block", exporter: "export_without_block", want: NewBool(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exported := callScript(t, context.Background(), script, tt.exporter, nil, CallOptions{})
			if exported.Kind() != KindHash {
				t.Fatalf("%s returned %v, want a hash", tt.exporter, exported.Kind())
			}
			got := callScript(t, context.Background(), script, "lookup",
				[]Value{exported, NewString("key")}, CallOptions{})
			assertValueEqual(t, got, tt.want)
		})
	}
}

// TestYieldSurvivesHostCloneInEscapedDefaultProc pins that a hash default proc
// which yields to the block of its producing method still finds that block after
// crossing the host boundary. The proc escapes one Script.Call and is re-entered
// through another; cloning the env for the host previously dropped the call
// frame's hidden block slot, so yield raised "no block given" on re-entry.
func TestYieldSurvivesHostCloneInEscapedDefaultProc(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def make
  Hash.new { |_, k| yield k }
end

def export_hash
  make { |k| "yielded-" + k }
end

def lookup(h, k)
  h[k]
end
`)

	exported := callScript(t, context.Background(), script, "export_hash", nil, CallOptions{})
	if exported.Kind() != KindHash {
		t.Fatalf("export_hash returned %v, want a hash", exported.Kind())
	}

	got := callScript(t, context.Background(), script, "lookup",
		[]Value{exported, NewString("x")}, CallOptions{})
	assertValueEqual(t, got, NewString("yielded-x"))
}

// TestYieldRejectsParamNamedBlock confirms a method that takes a __block__
// parameter still reports "no block given" when called without a block, proving
// yield never reads the parameter as the call's block.
func TestYieldRejectsParamNamedBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def yields(__block__)
      yield __block__
    end

    def run
      yields(1)
    end
    `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "no block given")
}
