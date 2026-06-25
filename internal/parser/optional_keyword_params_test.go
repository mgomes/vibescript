package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestParserOptionalKeywordParameters verifies that `name: default` declares an
// optional keyword-only parameter (kind ParamKeyword with a default value),
// distinct from both the bare `name:` required keyword form and the
// `name: Type` typed positional form.
func TestParserOptionalKeywordParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.Param
	}{
		{
			name: "integer_default",
			source: `def f(a: 0)
  a
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 0}},
			},
		},
		{
			name: "default_references_earlier_keyword",
			source: `def g(a:, b: a + 1)
  b
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword},
				{
					Name: "b",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "a"},
						Operator: ast.TokenPlus,
						Right:    &ast.IntegerLiteral{Value: 1},
					},
				},
			},
		},
		{
			name: "nil_default",
			source: `def f(a: nil)
  a
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.NilLiteral{}},
			},
		},
		{
			name: "string_default",
			source: `def f(a: "hi")
  a
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.StringLiteral{Value: "hi"}},
			},
		},
		{
			name: "boolean_default",
			source: `def f(a: true)
  a
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.BoolLiteral{Value: true}},
			},
		},
		{
			name: "array_default",
			source: `def f(a: [1, 2])
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 1},
						&ast.IntegerLiteral{Value: 2},
					}},
				},
			},
		},
		{
			name: "positional_then_optional_keyword",
			source: `def f(x, a: 0)
  x + a
end`,
			want: []ast.Param{
				{Name: "x"},
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 0}},
			},
		},
		{
			name: "required_keyword_then_optional_keyword",
			source: `def f(a:, b: 10)
  a + b
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword},
				{Name: "b", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 10}},
			},
		},
		{
			name: "optional_keyword_then_keyword_rest",
			source: `def f(a: 0, **rest)
  a
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 0}},
				{Name: "rest", Kind: ast.ParamKeywordRest},
			},
		},
		{
			name: "default_expression_with_member_access",
			source: `def f(a: helper.value)
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.MemberExpr{
						Object:   &ast.Identifier{Name: "helper"},
						Property: "value",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tt.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tt.source, errs)
			}
			fn, ok := got.Statements[0].(*ast.FunctionStmt)
			if !ok {
				t.Fatalf("statement 0 = %T, want *ast.FunctionStmt", got.Statements[0])
			}
			if diff := cmp.Diff(tt.want, fn.Params, astCmpOpts); diff != "" {
				t.Fatalf("params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserOptionalKeywordParametersParenless verifies that the optional
// keyword default form is also recognized in the parenless parameter list,
// where defaults are limited to a single line.
func TestParserOptionalKeywordParametersParenless(t *testing.T) {
	t.Parallel()

	source := `def f a: 0, b: 1
  a + b
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn, ok := got.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("statement 0 = %T, want *ast.FunctionStmt", got.Statements[0])
	}
	want := []ast.Param{
		{Name: "a", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 0}},
		{Name: "b", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 1}},
	}
	if diff := cmp.Diff(want, fn.Params, astCmpOpts); diff != "" {
		t.Fatalf("params mismatch (-want +got):\n%s", diff)
	}
}

// TestParserTypedPositionalNotOptionalKeyword verifies that the type-annotation
// forms remain typed positional parameters and are never reclassified as
// optional keyword parameters. The disambiguation hinges on what follows the
// colon: a type name standing at a parameter boundary, continuing as a union or
// generic, or carrying a `= default`.
func TestParserTypedPositionalNotOptionalKeyword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.Param
	}{
		{
			name: "bare_type_name",
			source: `def f(a: int)
  a
end`,
			want: []ast.Param{
				{Name: "a", Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
			},
		},
		{
			name: "enum_type_name",
			source: `def f(a: Color)
  a
end`,
			want: []ast.Param{
				{Name: "a", Type: &ast.TypeExpr{Name: "Color", Kind: ast.TypeEnum}},
			},
		},
		{
			name: "typed_positional_with_default",
			source: `def f(a: int = 5)
  a
end`,
			want: []ast.Param{
				{
					Name:       "a",
					Type:       &ast.TypeExpr{Name: "int", Kind: ast.TypeInt},
					DefaultVal: &ast.IntegerLiteral{Value: 5},
				},
			},
		},
		{
			name: "generic_type",
			source: `def f(a: array<int>)
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Type: &ast.TypeExpr{
						Name:     "array",
						Kind:     ast.TypeArray,
						TypeArgs: []*ast.TypeExpr{{Name: "int", Kind: ast.TypeInt}},
					},
				},
			},
		},
		{
			name: "union_type",
			source: `def f(a: int | string)
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Type: &ast.TypeExpr{
						Name: "int | string",
						Kind: ast.TypeUnion,
						Union: []*ast.TypeExpr{
							{Name: "int", Kind: ast.TypeInt},
							{Name: "string", Kind: ast.TypeString},
						},
					},
				},
			},
		},
		{
			name: "shape_type",
			source: `def f(a: { x: int })
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Type: &ast.TypeExpr{
						Kind:  ast.TypeShape,
						Shape: map[string]*ast.TypeExpr{"x": {Name: "int", Kind: ast.TypeInt}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tt.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tt.source, errs)
			}
			fn, ok := got.Statements[0].(*ast.FunctionStmt)
			if !ok {
				t.Fatalf("statement 0 = %T, want *ast.FunctionStmt", got.Statements[0])
			}
			if diff := cmp.Diff(tt.want, fn.Params, astCmpOpts); diff != "" {
				t.Fatalf("params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserOptionalKeywordCaptureRejectsDefault verifies that the optional
// keyword default form does not leak into capture parameters: `*rest: 0`
// remains rejected because rest parameters cannot carry a colon-introduced
// default.
func TestParserOptionalKeywordCaptureRejectsDefault(t *testing.T) {
	t.Parallel()

	source := `def f(*items: 0)
  items
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want a diagnostic", source)
	}
	// A rest parameter's colon introduces a type, so an integer there is a type
	// error rather than a silently accepted keyword default.
	var got strings.Builder
	for _, err := range errs {
		got.WriteString(err.Error())
		got.WriteByte('\n')
	}
	if !strings.Contains(got.String(), "expected type name") {
		t.Fatalf("parseSource(%q) errors = %s, want a type-name diagnostic", source, got.String())
	}
}
