package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Modules are namespaces (ADR-006 item 5). This file is the removal gate for
// behavior injection: every way a module's members used to become reachable
// from somewhere other than the module's own name has one case here, so
// restoring any part of the machinery fails a test rather than quietly
// widening the language again.

// TestMixinDirectivesAreCompileErrors covers the directive spellings the
// parser used to accept. Each one must name the namespace-call replacement:
// an author migrating a mixin reads the error far more often than the docs.
func TestMixinDirectivesAreCompileErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "include in a class body",
			source: "module Naming\nend\n\nclass Person\n  include Naming\nend\n",
			want:   "include is not supported",
		},
		{
			name:   "extend in a class body",
			source: "module Naming\nend\n\nclass Person\n  extend Naming\nend\n",
			want:   "extend is not supported",
		},
		{
			name:   "include in a module body",
			source: "module Base\nend\n\nmodule Naming\n  include Base\nend\n",
			want:   "include is not supported",
		},
		{
			name:   "parenthesized include",
			source: "module Naming\nend\n\nclass Person\n  include(Naming)\nend\n",
			want:   "include is not supported",
		},
		{
			name:   "multiple modules in one directive",
			source: "module A\nend\nmodule B\nend\n\nclass Person\n  include A, B\nend\n",
			want:   "include is not supported",
		},
		{
			name:   "scope-qualified include",
			source: "module Support\n  module Naming\n  end\nend\n\nclass Person\n  include Support::Naming\nend\n",
			want:   "include is not supported",
		},
		{
			name:   "extend self",
			source: "module Naming\n  extend self\nend\n",
			want:   "extend is not supported",
		},
		{
			name:   "bare include",
			source: "class Person\n  include\nend\n",
			want:   "include is not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := compileScriptErrorDefault(t, tc.source)
			requireErrorContains(t, err, tc.want)
			requireErrorContains(t, err, "Naming.display_name(person)")
		})
	}
}

// TestModuleInstanceMembersAreCompileErrors covers the declarations that only
// ever existed to be copied into an including class. A module has no
// instances, so each is rejected where it is written rather than left inert.
func TestModuleInstanceMembersAreCompileErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "instance-style method",
			source: "module Naming\n  def display_name\n    \"n\"\n  end\nend\n",
			want:   "def display_name in module Naming must be def self.display_name",
		},
		{
			name:   "operator method",
			source: "module Adding\n  def +(other)\n    other\n  end\nend\n",
			want:   "def + in module Adding must be def self.+",
		},
		{
			name:   "property",
			source: "module Named\n  property name\nend\n",
			want:   "property in module Named is not supported",
		},
		{
			name:   "getter",
			source: "module Named\n  getter name\nend\n",
			want:   "getter in module Named is not supported",
		},
		{
			name:   "setter",
			source: "module Named\n  setter name\nend\n",
			want:   "setter in module Named is not supported",
		},
		{
			name:   "alias",
			source: "module Naming\n  def self.tag\n    1\n  end\n  alias label tag\nend\n",
			want:   "alias in module Naming is not supported",
		},
		{
			name:   "alias_method",
			source: "module Naming\n  def self.tag\n    1\n  end\n  alias_method :label, :tag\nend\n",
			want:   "alias_method in module Naming is not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCompileErrorContainsDefault(t, tc.source, tc.want)
		})
	}
}

// TestModuleIsNotATypeRelationship covers the introspection and contract
// answers inclusion used to change. A module names a namespace, so no
// instance belongs to one and the three predicates agree on every value.
func TestModuleIsNotATypeRelationship(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
module Naming
  def self.display_name(person)
    "I am " + person.name
  end
end

class Person
  def initialize(name)
    @name = name
  end

  def name
    @name
  end
end

def predicates
  p = Person.new("Ada")
  [p.is_a?(Naming), p.kind_of?(Naming), p.instance_of?(Naming), p.is_a?(Person)]
end

def namespace_call
  Naming.display_name(Person.new("Ada"))
end

def module_contract(thing: Naming)
  thing
end

def rejected
  module_contract(Person.new("Ada"))
end
`)

	got := callFunc(t, script, "predicates", nil).Array()
	want := []bool{false, false, false, true}
	for i, expect := range want {
		if !got[i].Equal(NewBool(expect)) {
			t.Fatalf("predicate %d = %v, want %v", i, got[i], expect)
		}
	}
	if result := callFunc(t, script, "namespace_call", nil); !result.Equal(NewString("I am Ada")) {
		t.Fatalf("namespace_call = %v, want \"I am Ada\"", result)
	}
	requireCallErrorContains(t, script, "rejected", nil, CallOptions{}, "expected Naming")
}

// TestModuleConstantsStayOnTheirModule covers the per-execution adoption that
// used to copy a module's constants into an including class. A constant is
// reachable through the module that declares it and nowhere else.
func TestModuleConstantsStayOnTheirModule(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
module Limits
  MAX = 9
end

class Config
end

def scoped
  Limits::MAX
end

def through_the_class
  Config::MAX
end
`)

	if got := callFunc(t, script, "scoped", nil); !got.Equal(NewInt(9)) {
		t.Fatalf("scoped = %v, want 9", got)
	}
	requireCallErrorContains(t, script, "through_the_class", nil, CallOptions{}, "MAX")

	if _, ok := script.classes["Config"].ClassVars["MAX"]; ok {
		t.Fatal("a class adopted a module constant")
	}
}

// TestClassDefinitionCarriesNoInclusionState is the structural half of the
// gate. The ancestor list and its transitive-closure ordering were the state
// every adoption, predicate, and contract answer read; a field reintroducing
// them would restore the behavior above without any of those tests naming it.
func TestClassDefinitionCarriesNoInclusionState(t *testing.T) {
	t.Parallel()

	classDef := reflect.TypeOf(ClassDef{})
	for i := range classDef.NumField() {
		name := classDef.Field(i).Name
		lowered := strings.ToLower(name)
		if strings.Contains(lowered, "include") || strings.Contains(lowered, "mixin") ||
			strings.Contains(lowered, "ancestor") || strings.Contains(lowered, "extend") {
			t.Fatalf("ClassDef.%s reintroduces module inclusion state", name)
		}
	}
}

// TestScriptCallDoesNotAdoptModuleState pins the call path itself: a class
// with no body of its own does no per-execution work on behalf of a module,
// so nothing a module holds can be charged to, or leak into, a class.
func TestScriptCallDoesNotAdoptModuleState(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
module Limits
  MAX = 9
end

class Config
end

def run
  Config
end
`)

	classVal := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	classDef := valueClass(classVal)
	if classDef == nil {
		t.Fatal("run did not return a class")
	}
	if len(classDef.ClassVars) != 0 {
		t.Fatalf("the returned class carries %v, want no adopted state", classDef.ClassVars)
	}
	if len(classDef.Methods) != 0 {
		t.Fatalf("the returned class carries methods %v, want none", classDef.Methods)
	}
}
