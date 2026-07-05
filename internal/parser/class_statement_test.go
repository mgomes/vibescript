package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTypedAccessorDeclarations(t *testing.T) {
	t.Parallel()
	source := `class User
  property name: string
  getter age: int?
  setter tag: string
  property x, y
  property a: int, b: string | int
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	class, ok := got.Statements[0].(*ast.ClassStmt)
	if !ok {
		t.Fatalf("expected class statement, got %T", got.Statements[0])
	}

	want := []ast.PropertyDecl{
		{
			Kind:  "property",
			Names: []ast.PropertyName{{Name: "name", Type: &ast.TypeExpr{Name: "string", Kind: ast.TypeString}}},
		},
		{
			Kind:  "getter",
			Names: []ast.PropertyName{{Name: "age", Type: &ast.TypeExpr{Name: "int?", Kind: ast.TypeInt, Nullable: true}}},
		},
		{
			Kind:  "setter",
			Names: []ast.PropertyName{{Name: "tag", Type: &ast.TypeExpr{Name: "string", Kind: ast.TypeString}}},
		},
		{
			Kind:  "property",
			Names: []ast.PropertyName{{Name: "x"}, {Name: "y"}},
		},
		{
			Kind: "property",
			Names: []ast.PropertyName{
				{Name: "a", Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
				{Name: "b", Type: &ast.TypeExpr{
					Name: "string | int",
					Kind: ast.TypeUnion,
					Union: []*ast.TypeExpr{
						{Name: "string", Kind: ast.TypeString},
						{Name: "int", Kind: ast.TypeInt},
					},
				}},
			},
		},
	}

	if diff := cmp.Diff(want, class.Properties, astCmpOpts); diff != "" {
		t.Fatalf("properties mismatch (-want +got):\n%s", diff)
	}
}

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
  setter friend: User, manager: User
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
			Kind:  "property",
			Names: []ast.PropertyName{{Name: "name", Type: &ast.TypeExpr{Name: "string", Kind: ast.TypeString}}},
		},
		{
			Kind:  "getter",
			Names: []ast.PropertyName{{Name: "age", Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}}},
		},
		{
			Kind: "setter",
			Names: []ast.PropertyName{
				{Name: "friend", Type: &ast.TypeExpr{Name: "User", Kind: ast.TypeEnum}},
				{Name: "manager", Type: &ast.TypeExpr{Name: "User", Kind: ast.TypeEnum}},
			},
		},
	}
	if diff := cmp.Diff(want, classStmt.Properties, astCmpOpts); diff != "" {
		t.Fatalf("properties mismatch (-want +got):\n%s", diff)
	}
}
