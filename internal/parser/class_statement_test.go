package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserRejectsSingletonClassSyntax(t *testing.T) {
	t.Parallel()
	_, errs := Parse(`
class << self
  def build
    1
  end
end
`)
	if len(errs) == 0 {
		t.Fatal("expected parse error for class << self")
	}
	if !strings.Contains(errs[0].Error(), "class << self definitions are not supported; use def self.name") {
		t.Fatalf("unexpected parse error: %v", errs[0])
	}
}

func TestParserClassPropertyTypeAnnotations(t *testing.T) {
	t.Parallel()
	source := `class User
  property name: string
  getter age: int
  setter friend, manager: User
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	classStmt, ok := got.Statements[0].(*ast.ClassStmt)
	if !ok {
		t.Fatalf("statement 0 = %T, want *ast.ClassStmt", got.Statements[0])
	}
	want := []ast.PropertyDecl{
		{
			Names: []string{"name"},
			Kind:  "property",
			Type:  &ast.TypeExpr{Name: "string", Kind: ast.TypeString},
		},
		{
			Names: []string{"age"},
			Kind:  "getter",
			Type:  &ast.TypeExpr{Name: "int", Kind: ast.TypeInt},
		},
		{
			Names: []string{"friend", "manager"},
			Kind:  "setter",
			Type:  &ast.TypeExpr{Name: "User", Kind: ast.TypeEnum},
		},
	}
	if diff := cmp.Diff(want, classStmt.Properties, astCmpOpts); diff != "" {
		t.Fatalf("properties mismatch (-want +got):\n%s", diff)
	}
}
