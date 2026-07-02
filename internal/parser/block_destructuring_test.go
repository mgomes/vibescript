package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserBlockParameterDestructuring(t *testing.T) {
	t.Parallel()

	source := `def run
  pairs.map do |(a, b), label|
    a + b
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	stmt, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.ExprStmt", body[0])
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("body[0].Expr = %T, want *ast.CallExpr", stmt.Expr)
	}
	if call.Block == nil {
		t.Fatal("call block = nil, want block")
	}

	want := []ast.Param{
		{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "a"}},
				{Target: &ast.Identifier{Name: "b"}},
			}},
		},
		{Name: "label"},
	}
	if diff := cmp.Diff(want, call.Block.Params, astCmpOpts); diff != "" {
		t.Fatalf("block params mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBlockParameterDestructuringTypeAnnotations(t *testing.T) {
	t.Parallel()

	source := `def run
  rows.map do |(id: int, name: string), (head: int, *tail: array<int>)|
    name
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	stmt := body[0].(*ast.ExprStmt)
	call := stmt.Expr.(*ast.CallExpr)
	if call.Block == nil {
		t.Fatal("call block = nil, want block")
	}

	want := []ast.Param{
		{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "id"}, Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
				{Target: &ast.Identifier{Name: "name"}, Type: &ast.TypeExpr{Name: "string", Kind: ast.TypeString}},
			}},
		},
		{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "head"}, Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
				{
					Target: &ast.Identifier{Name: "tail"},
					Rest:   true,
					Type: &ast.TypeExpr{
						Name:     "array",
						Kind:     ast.TypeArray,
						TypeArgs: []*ast.TypeExpr{{Name: "int", Kind: ast.TypeInt}},
					},
				},
			}},
		},
	}
	if diff := cmp.Diff(want, call.Block.Params, astCmpOpts); diff != "" {
		t.Fatalf("block params mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBlockParameterDestructuringAnonymousRest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params string
		want   []ast.DestructureElement
	}{
		{
			name:   "trailing anonymous rest",
			params: "(head, *)",
			want: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "head"}},
				{Rest: true},
			},
		},
		{
			name:   "middle anonymous rest",
			params: "(head, *, tail)",
			want: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "head"}},
				{Rest: true},
				{Target: &ast.Identifier{Name: "tail"}},
			},
		},
		{
			name:   "leading anonymous rest",
			params: "(*, tail)",
			want: []ast.DestructureElement{
				{Rest: true},
				{Target: &ast.Identifier{Name: "tail"}},
			},
		},
		{
			name:   "bracket form anonymous rest",
			params: "[head, *]",
			want: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "head"}},
				{Rest: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  values.map do |" + tt.params + "|\n    head\n  end\nend"

			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			body := parsedFunctionBody(t, got)
			stmt, ok := body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("body[0] = %T, want *ast.ExprStmt", body[0])
			}
			call, ok := stmt.Expr.(*ast.CallExpr)
			if !ok {
				t.Fatalf("body[0].Expr = %T, want *ast.CallExpr", stmt.Expr)
			}
			if call.Block == nil {
				t.Fatal("call block = nil, want block")
			}

			want := []ast.Param{
				{Target: &ast.DestructureTarget{Elements: tt.want}},
			}
			if diff := cmp.Diff(want, call.Block.Params, astCmpOpts); diff != "" {
				t.Fatalf("block params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserBlockParameterDestructuringRejectsNonIdentifierTargets(t *testing.T) {
	t.Parallel()

	source := `def run
  values.map do |(record.value)|
    record
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want invalid destructuring target error", source)
	}
	if got, want := errs[0].Error(), "invalid block parameter destructuring target"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
