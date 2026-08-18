package runtime

import (
	"testing"

	"github.com/mgomes/vibescript/internal/parser"
)

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
