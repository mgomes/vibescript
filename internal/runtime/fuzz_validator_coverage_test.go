package runtime

import (
	"reflect"
	"testing"

	"github.com/mgomes/vibescript/internal/parser"
)

// TestFuzzValidatorCoversModernSyntax parses one exemplar of every syntax
// form added since the fuzz validator was written and requires that the
// completeness check and the AST cloner both accept it. New AST node types
// or fields that the validator or cloner miss surface here as deterministic
// failures instead of latent fuzz flakes (issue #902 was exactly that: the
// validator rejected endless ranges and splat arguments, and the cloner
// dropped visibility member declarations).
func TestFuzzValidatorCoversModernSyntax(t *testing.T) {
	t.Parallel()

	cases := []string{
		"0..",
		"x = ..5",
		"y = 5..",
		"f(*[1])",
		"f *[1]",
		"f(**{\"a\": 1})",
		"f **{\"a\": 1}",
		"f(&:size)",
		"f(&blk)",
		"f(1, *rest, k: 2, **opts, &blk)",
		"l = -> { 1 }",
		"l = ->(x) { x }",
		"p = proc do\n  1\nend",
		"p = lambda do |x|\n  x\nend",
		"module Billing\n  LIMIT = 1\n  def self.code()\n    1\n  end\nend",
		"module Outer\n  module Inner\n    def self.deep()\n      1\n    end\n  end\nend",
		"class C\n  private\n  def m()\n    1\n  end\nend",
		"class C\n  protected def m()\n    1\n  end\nend",
		"class C\n  def m()\n    1\n  end\n  private :m\nend",
		"match /id/",
		"describe /id/i, 2",
		"s = :\"\"",
		"* = 0",
		"*, x = 1, 2",
		"a, * = 1, 2, 3",
	}
	for _, src := range cases {
		program, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Errorf("Parse(%q) failed: %v", src, errs[0])
			continue
		}
		if err := validateFuzzProgram(program); err != nil {
			t.Errorf("validateFuzzProgram(%q) rejected a valid parse: %v", src, err)
			continue
		}
		cloned := &Program{Statements: cloneStatements(program.Statements)}
		if err := validateFuzzProgram(cloned); err != nil {
			t.Errorf("cloneStatements(%q) produced an invalid AST: %v", src, err)
			continue
		}
		if !reflect.DeepEqual(program, cloned) {
			t.Errorf("cloneStatements(%q) changed the AST", src)
		}
	}
}

// TestCloneStatementsDeepClonesClassMemberDecls pins that the cloner copies
// the visibility and nested-module structures rather than sharing them:
// mutating the original after cloning must not leak into the clone.
func TestCloneStatementsDeepClonesClassMemberDecls(t *testing.T) {
	t.Parallel()

	program, errs := parser.Parse("class C\n  private :m\n  def m()\n    1\n  end\nend\nmodule Outer\n  module Inner\n  end\nend")
	if len(errs) > 0 {
		t.Fatalf("parse failed: %v", errs[0])
	}
	cloned := &Program{Statements: cloneStatements(program.Statements)}

	class := program.Statements[0].(*ClassStmt)
	classClone := cloned.Statements[0].(*ClassStmt)
	for i, member := range class.Members {
		if member.Visibility != nil {
			if classClone.Members[i].Visibility == member.Visibility {
				t.Fatalf("members[%d].Visibility is shared with the original", i)
			}
		}
	}

	outer := program.Statements[1].(*ClassStmt)
	outerClone := cloned.Statements[1].(*ClassStmt)
	if len(outer.Modules) == 0 {
		t.Fatal("expected Outer to record a nested module")
	}
	if outerClone.Modules[0] == outer.Modules[0] {
		t.Fatal("nested module ClassStmt is shared with the original")
	}
}
