package runtime

import "testing"

func TestIncludeMixesInstanceMethodsIntoClass(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Named
  def display_name
    "I am " + name
  end
end

class Person
  include Named

  def initialize(name)
    @name = name
  end

  def name
    @name
  end
end

def run
  Person.new("Ada").display_name
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewString("I am Ada")) {
		t.Fatalf("run = %v", got)
	}
}

func TestExtendMixesMethodsIntoClassSurface(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Buildable
  def build_tag
    "built"
  end
end

class Widget
  extend Buildable
end

def class_side
  Widget.build_tag
end

def instance_side
  Widget.new.build_tag
end
`)

	if got := callFunc(t, script, "class_side", nil); !got.Equal(NewString("built")) {
		t.Fatalf("class_side = %v", got)
	}
	requireCallErrorContains(t, script, "instance_side", nil, CallOptions{}, "unknown member build_tag")
}

func TestMixinCollisionRules(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module A
  def who
    "A"
  end
end

module B
  def who
    "B"
  end
end

class EarlyOwn
  def who
    "own"
  end

  include A
end

class LaterOwn
  include A

  def who
    "own"
  end
end

class LaterIncludeWins
  include A
  include B
end

class OneDirective
  include A, B
end

def run
  [EarlyOwn.new.who, LaterOwn.new.who, LaterIncludeWins.new.who, OneDirective.new.who]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	want := []string{"own", "own", "B", "A"}
	for i, expect := range want {
		if !got[i].Equal(NewString(expect)) {
			t.Fatalf("collision case %d = %v, want %s", i, got[i], expect)
		}
	}
}

func TestIncludePreservesModuleMethodVisibility(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Ops
  def uses_hidden
    hidden_helper
  end

  private def hidden_helper
    "hh"
  end
end

class Thing
  include Ops
end

def sanctioned
  Thing.new.uses_hidden
end

def external
  Thing.new.hidden_helper
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewString("hh")) {
		t.Fatalf("sanctioned = %v", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "private method hidden_helper")
}

func TestVisibilityDirectiveOnIncludedMethod(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Loud
  def shout
    "hey"
  end
end

class Quiet
  include Loud
  private :shout

  def whisper
    shout
  end
end

def sanctioned
  Quiet.new.whisper
end

def external
  Quiet.new.shout
end

def module_still_public
  Loud
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewString("hey")) {
		t.Fatalf("sanctioned = %v", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "private method shout")
}

func TestIncludedOperatorAndIndexMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Arith
  def +(other)
    value + other
  end

  def [](index)
    value * index
  end
end

class Num
  include Arith

  def initialize(value)
    @value = value
  end

  def value
    @value
  end
end

def plus
  Num.new(4) + 3
end

def index
  Num.new(4)[2]
end
`)

	if got := callFunc(t, script, "plus", nil); !got.Equal(NewInt(7)) {
		t.Fatalf("plus = %v, want 7", got)
	}
	if got := callFunc(t, script, "index", nil); !got.Equal(NewInt(8)) {
		t.Fatalf("index = %v, want 8", got)
	}
}

func TestIncludedModuleConstantsSurfaceAsClassConstants(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Limits
  MAX = 9
end

class Config
  include Limits

  def max_from_method
    MAX
  end
end

def scoped
  Config::MAX
end

def through_method
  Config.new.max_from_method
end
`)

	if got := callFunc(t, script, "scoped", nil); !got.Equal(NewInt(9)) {
		t.Fatalf("scoped = %v, want 9", got)
	}
	if got := callFunc(t, script, "through_method", nil); !got.Equal(NewInt(9)) {
		t.Fatalf("through_method = %v, want 9", got)
	}
}

func TestClassOwnConstantWinsOverIncluded(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Limits
  MAX = 9
end

class Config
  include Limits
  MAX = 3
end

def run
  [Config::MAX, Limits::MAX]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	if !got[0].Equal(NewInt(3)) || !got[1].Equal(NewInt(9)) {
		t.Fatalf("run = %v, want [3, 9]", got)
	}
}

func TestIncludedConstantMutationIsolatedPerCallAndPerClass(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Registry
  TAG = "fresh"
end

class Store
  include Registry

  def self.overwrite
    TAG = "dirty"
    TAG
  end
end

def dirty
  Store.overwrite
  [Store::TAG, Registry::TAG]
end

def fresh
  [Store::TAG, Registry::TAG]
end
`)

	got := callFunc(t, script, "dirty", nil).Array()
	if !got[0].Equal(NewString("dirty")) || !got[1].Equal(NewString("fresh")) {
		t.Fatalf("dirty = %v, want class copy mutated, module untouched", got)
	}
	got = callFunc(t, script, "fresh", nil).Array()
	if !got[0].Equal(NewString("fresh")) || !got[1].Equal(NewString("fresh")) {
		t.Fatalf("fresh = %v, want both fresh (state leaked across calls)", got)
	}
}

func TestModuleIncludesModuleTransitively(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Base
  ROOT = 1

  def base_tag
    "base"
  end
end

module Extra
  include Base

  def extra_tag
    base_tag + "+extra"
  end
end

class Uses
  include Extra
end

def run
  [Uses.new.extra_tag, Uses::ROOT]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	if !got[0].Equal(NewString("base+extra")) || !got[1].Equal(NewInt(1)) {
		t.Fatalf("run = %v, want [base+extra, 1]", got)
	}
}

func TestIncludeScopedAndSiblingModuleReferences(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Support
  module Naming
    def named_tag
      "named"
    end
  end

  module Wrapper
    include Naming
  end
end

class Person
  include Support::Naming
end

class Wrapped
  include Support::Wrapper
end

def direct
  Person.new.named_tag
end

def through_sibling
  Wrapped.new.named_tag
end
`)

	if got := callFunc(t, script, "direct", nil); !got.Equal(NewString("named")) {
		t.Fatalf("direct = %v", got)
	}
	if got := callFunc(t, script, "through_sibling", nil); !got.Equal(NewString("named")) {
		t.Fatalf("through_sibling = %v", got)
	}
}

func TestIsAReportsIncludedModules(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Limits
end

module Unrelated
end

class Config
  include Limits
end

def run
  c = Config.new
  [c.is_a?(Limits), c.kind_of?(Limits), c.instance_of?(Limits), c.is_a?(Unrelated)]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	want := []bool{true, true, false, false}
	for i, expect := range want {
		if !got[i].Equal(NewBool(expect)) {
			t.Fatalf("predicate %d = %v, want %v", i, got[i], expect)
		}
	}
}

func TestModuleTypeContractAcceptsIncludingClass(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Named
  def tag
    "n"
  end
end

class Person
  include Named
end

class Robot
end

def describe(thing: Named)
  thing.tag
end

def accepted
  describe(Person.new)
end

def rejected
  describe(Robot.new)
end
`)

	if got := callFunc(t, script, "accepted", nil); !got.Equal(NewString("n")) {
		t.Fatalf("accepted = %v", got)
	}
	requireCallErrorContains(t, script, "rejected", nil, CallOptions{}, "expected Named")
}

func TestNonLocalReturnThroughIncludedMethod(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Seek
  def find_first(items)
    items.each do |item|
      return item if item > 1
    end
    nil
  end
end

class Finder
  include Seek
end

def run
  Finder.new.find_first([0, 1, 5, 7])
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(5)) {
		t.Fatalf("run = %v, want 5", got)
	}
}

func TestIncludedAccessorDeclarations(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module HasName
  property name: string

  def initialize(name)
    @name = name
  end
end

class Person
  include HasName
end

def run
  p = Person.new("Ada")
  p.name = "Grace"
  p.name
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewString("Grace")) {
		t.Fatalf("run = %v", got)
	}
}

func TestMixinCompileErrors(t *testing.T) {
	t.Parallel()
	requireCompileErrorContainsDefault(t, `
class Person
  include Missing
end
`, "include target module Missing is not defined")

	requireCompileErrorContainsDefault(t, `
class Person
end

class Other
  include Person
end
`, "include target Person is not a module")

	requireCompileErrorContainsDefault(t, `
module Later
end

class Uses
  extend Missing
end
`, "extend target module Missing is not defined")
}
