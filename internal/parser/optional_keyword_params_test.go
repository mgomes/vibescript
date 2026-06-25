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
		{
			// A shape field whose value is a bare enum type name (not a local
			// value in scope) stays a shape type. The local-value check that
			// routes `{ sum: a }` to a hash default must not catch a genuine
			// enum field like `Color` here.
			name: "shape_type_with_enum_field",
			source: `def f(a: { x: Color })
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Type: &ast.TypeExpr{
						Kind:  ast.TypeShape,
						Shape: map[string]*ast.TypeExpr{"x": {Name: "Color", Kind: ast.TypeEnum}},
					},
				},
			},
		},
		{
			name: "shape_type_with_nullable_union_field",
			source: `def f(a: { x: int | nil })
  a
end`,
			want: []ast.Param{
				{
					Name: "a",
					Type: &ast.TypeExpr{
						Kind: ast.TypeShape,
						Shape: map[string]*ast.TypeExpr{
							"x": {
								Name: "int | nil",
								Kind: ast.TypeUnion,
								Union: []*ast.TypeExpr{
									{Name: "int", Kind: ast.TypeInt},
									{Name: "nil", Kind: ast.TypeNil, Nullable: false},
								},
							},
						},
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

// TestParserOptionalKeywordHashDefault verifies that a `{ ... }` keyword
// default is parsed as a hash literal default rather than a shape type when its
// contents are values rather than types. A shape type's field values are
// themselves types all the way down, so any non-type value (a number here, or a
// nested non-type value) marks the brace group as a hash default.
func TestParserOptionalKeywordHashDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.Param
	}{
		{
			name: "hash_default",
			source: `def f(opts: { retry: 3 })
  opts
end`,
			want: []ast.Param{
				{
					Name: "opts",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{Key: &ast.SymbolLiteral{Name: "retry"}, Value: &ast.IntegerLiteral{Value: 3}},
					}},
				},
			},
		},
		{
			name: "empty_hash_default",
			source: `def f(opts: {})
  opts
end`,
			want: []ast.Param{
				{Name: "opts", Kind: ast.ParamKeyword, DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{}}},
			},
		},
		{
			name: "nested_hash_default",
			source: `def f(opts: { a: { b: 1 } })
  opts
end`,
			want: []ast.Param{
				{
					Name: "opts",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{
							Key: &ast.SymbolLiteral{Name: "a"},
							Value: &ast.HashLiteral{Pairs: []ast.HashPair{
								{Key: &ast.SymbolLiteral{Name: "b"}, Value: &ast.IntegerLiteral{Value: 1}},
							}},
						},
					}},
				},
			},
		},
		{
			name: "nil_valued_hash_default",
			source: `def f(opts: { previous: nil })
  opts
end`,
			want: []ast.Param{
				{
					Name: "opts",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{Key: &ast.SymbolLiteral{Name: "previous"}, Value: &ast.NilLiteral{}},
					}},
				},
			},
		},
		{
			name: "nested_nil_valued_hash_default",
			source: `def f(opts: { inner: { previous: nil } })
  opts
end`,
			want: []ast.Param{
				{
					Name: "opts",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{
							Key: &ast.SymbolLiteral{Name: "inner"},
							Value: &ast.HashLiteral{Pairs: []ast.HashPair{
								{Key: &ast.SymbolLiteral{Name: "previous"}, Value: &ast.NilLiteral{}},
							}},
						},
					}},
				},
			},
		},
		{
			name: "hash_default_references_earlier_keyword",
			source: `def g(a:, b: { sum: a + 1 })
  b
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword},
				{
					Name: "b",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{
							Key: &ast.SymbolLiteral{Name: "sum"},
							Value: &ast.BinaryExpr{
								Left:     &ast.Identifier{Name: "a"},
								Operator: ast.TokenPlus,
								Right:    &ast.IntegerLiteral{Value: 1},
							},
						},
					}},
				},
			},
		},
		{
			// A hash default whose value is a *bare* identifier referencing an
			// earlier keyword parameter. The speculative shape parse accepts the
			// identifier as an enum type, so without the local-value check the
			// brace group is misclassified as a positional shape annotation
			// (which is then rejected for following a keyword parameter).
			name: "bare_ident_hash_default_references_earlier_keyword",
			source: `def g(a:, b: { sum: a })
  b
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword},
				{
					Name: "b",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{
							Key:   &ast.SymbolLiteral{Name: "sum"},
							Value: &ast.Identifier{Name: "a"},
						},
					}},
				},
			},
		},
		{
			// The bare-identifier local-value check also looks through nested
			// brace groups, so a nested hash value referencing an earlier
			// parameter keeps the whole group a hash default.
			name: "nested_bare_ident_hash_default_references_earlier_keyword",
			source: `def g(a:, b: { inner: { sum: a } })
  b
end`,
			want: []ast.Param{
				{Name: "a", Kind: ast.ParamKeyword},
				{
					Name: "b",
					Kind: ast.ParamKeyword,
					DefaultVal: &ast.HashLiteral{Pairs: []ast.HashPair{
						{
							Key: &ast.SymbolLiteral{Name: "inner"},
							Value: &ast.HashLiteral{Pairs: []ast.HashPair{
								{
									Key:   &ast.SymbolLiteral{Name: "sum"},
									Value: &ast.Identifier{Name: "a"},
								},
							}},
						},
					}},
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

// TestParserShapeTypeStillTypedPositional verifies that a `{ field: Type }`
// brace group whose field values are types remains a typed positional parameter
// with a shape type, even though `{ ... }` keyword defaults are now hash
// literals. A trailing postfix continuation (such as a method call) instead
// makes the group a hash default, since a shape type cannot carry one.
func TestParserShapeTypeStillTypedPositional(t *testing.T) {
	t.Parallel()

	source := `def f(a: { x: int })
  a
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
		{
			Name: "a",
			Type: &ast.TypeExpr{
				Kind:  ast.TypeShape,
				Shape: map[string]*ast.TypeExpr{"x": {Name: "int", Kind: ast.TypeInt}},
			},
		},
	}
	if diff := cmp.Diff(want, fn.Params, astCmpOpts); diff != "" {
		t.Fatalf("params mismatch (-want +got):\n%s", diff)
	}
}

// TestParserOptionalKeywordLessThanDefault verifies that a keyword default
// whose expression starts with an earlier keyword parameter followed by `<` is
// parsed as a less-than comparison rather than a generic type continuation. A
// non-generic identifier (an earlier parameter) is a value, so `ok: limit < 10`
// is a default expression rather than a malformed `limit<...>` type.
func TestParserOptionalKeywordLessThanDefault(t *testing.T) {
	t.Parallel()

	source := `def f(limit:, ok: limit < 10)
  ok
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
		{Name: "limit", Kind: ast.ParamKeyword},
		{
			Name: "ok",
			Kind: ast.ParamKeyword,
			DefaultVal: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "limit"},
				Operator: ast.TokenLT,
				Right:    &ast.IntegerLiteral{Value: 10},
			},
		},
	}
	if diff := cmp.Diff(want, fn.Params, astCmpOpts); diff != "" {
		t.Fatalf("params mismatch (-want +got):\n%s", diff)
	}
}

// TestParserGenericContainerTypeNotLessThan verifies that the less-than
// disambiguation does not misclassify a genuine generic container type. `array`
// is not a value, so `a: array<int>` remains a typed positional parameter with
// the generic continuing the type rather than opening a comparison.
func TestParserGenericContainerTypeNotLessThan(t *testing.T) {
	t.Parallel()

	source := `def f(a: array<int>)
  a
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
		{
			Name: "a",
			Type: &ast.TypeExpr{
				Name:     "array",
				Kind:     ast.TypeArray,
				TypeArgs: []*ast.TypeExpr{{Name: "int", Kind: ast.TypeInt}},
			},
		},
	}
	if diff := cmp.Diff(want, fn.Params, astCmpOpts); diff != "" {
		t.Fatalf("params mismatch (-want +got):\n%s", diff)
	}
}

// TestParserGenericContainerTypeNotShadowedByLocal verifies that a built-in
// generic container type name keeps continuing as a type even when an earlier
// parameter shadows it with a value local. `array` here names a positional
// parameter, yet `values: array<int>` must still parse as a generic type
// annotation rather than a `array < int >` comparison: built-in generic type
// parsing is never shadowed by value locals.
func TestParserGenericContainerTypeNotShadowedByLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.Param
	}{
		{
			name: "array_shadowed_by_param",
			source: `def f(array, values: array<int>)
  values
end`,
			want: []ast.Param{
				{Name: "array"},
				{
					Name: "values",
					Type: &ast.TypeExpr{
						Name:     "array",
						Kind:     ast.TypeArray,
						TypeArgs: []*ast.TypeExpr{{Name: "int", Kind: ast.TypeInt}},
					},
				},
			},
		},
		{
			name: "hash_shadowed_by_param",
			source: `def f(hash, lookup: hash<string, int>)
  lookup
end`,
			want: []ast.Param{
				{Name: "hash"},
				{
					Name: "lookup",
					Type: &ast.TypeExpr{
						Name: "hash",
						Kind: ast.TypeHash,
						TypeArgs: []*ast.TypeExpr{
							{Name: "string", Kind: ast.TypeString},
							{Name: "int", Kind: ast.TypeInt},
						},
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

// TestParserContainerLocalComparisonNeedsParens documents that a container-type
// name continues as a type even after a colon, so a default that compares a
// container-named local with `<` uses the documented parenthesized escape hatch
// (`ok: (array < 1)`), matching the bare-identifier rule the changelog records.
func TestParserContainerLocalComparisonNeedsParens(t *testing.T) {
	t.Parallel()

	source := `def f(array, ok: (array < 1))
  ok
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
		{Name: "array"},
		{
			Name: "ok",
			Kind: ast.ParamKeyword,
			DefaultVal: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "array"},
				Operator: ast.TokenLT,
				Right:    &ast.IntegerLiteral{Value: 1},
			},
		},
	}
	if diff := cmp.Diff(want, fn.Params, astCmpOpts); diff != "" {
		t.Fatalf("params mismatch (-want +got):\n%s", diff)
	}
}
