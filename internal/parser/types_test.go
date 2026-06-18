package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTypeSyntaxCompositeForms(t *testing.T) {
	t.Parallel()
	source := `def run(
  rows: array<int | string>,
  payload: { id: string, stats: { wins: int } }
) -> hash<string, { score: int | nil }>
  payload
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name: "run",
				Params: []ast.Param{
					{
						Name: "rows",
						Type: &ast.TypeExpr{
							Name: "array",
							Kind: ast.TypeArray,
							TypeArgs: []*ast.TypeExpr{
								{
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
						Name: "payload",
						Type: &ast.TypeExpr{
							Kind: ast.TypeShape,
							Shape: map[string]*ast.TypeExpr{
								"id": {Name: "string", Kind: ast.TypeString},
								"stats": {
									Kind: ast.TypeShape,
									Shape: map[string]*ast.TypeExpr{
										"wins": {Name: "int", Kind: ast.TypeInt},
									},
								},
							},
						},
					},
				},
				ReturnTy: &ast.TypeExpr{
					Name: "hash",
					Kind: ast.TypeHash,
					TypeArgs: []*ast.TypeExpr{
						{Name: "string", Kind: ast.TypeString},
						{
							Kind: ast.TypeShape,
							Shape: map[string]*ast.TypeExpr{
								"score": {
									Name: "int | nil",
									Kind: ast.TypeUnion,
									Union: []*ast.TypeExpr{
										{Name: "int", Kind: ast.TypeInt},
										{Name: "nil", Kind: ast.TypeNil},
									},
								},
							},
						},
					},
				},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.Identifier{Name: "payload"}},
				},
			},
		},
	}

	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTypeShapeAllowsEnumFieldName(t *testing.T) {
	t.Parallel()
	source := `def run(payload: { enum: string, nested: { enum: int } })
  payload
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := got.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", got.Statements[0])
	}
	wantType := &ast.TypeExpr{
		Kind: ast.TypeShape,
		Shape: map[string]*ast.TypeExpr{
			"enum": {Name: "string", Kind: ast.TypeString},
			"nested": {
				Kind: ast.TypeShape,
				Shape: map[string]*ast.TypeExpr{
					"enum": {Name: "int", Kind: ast.TypeInt},
				},
			},
		},
	}
	if diff := cmp.Diff(wantType, fn.Params[0].Type, astCmpOpts); diff != "" {
		t.Fatalf("payload type mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTypeShapeAllowsWordBooleanFieldNames(t *testing.T) {
	t.Parallel()
	source := `def run(payload: { and: bool, or: bool, nested: { and: bool } })
  payload
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := got.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", got.Statements[0])
	}
	wantType := &ast.TypeExpr{
		Kind: ast.TypeShape,
		Shape: map[string]*ast.TypeExpr{
			"and": {Name: "bool", Kind: ast.TypeBool},
			"or":  {Name: "bool", Kind: ast.TypeBool},
			"nested": {
				Kind: ast.TypeShape,
				Shape: map[string]*ast.TypeExpr{
					"and": {Name: "bool", Kind: ast.TypeBool},
				},
			},
		},
	}
	if diff := cmp.Diff(wantType, fn.Params[0].Type, astCmpOpts); diff != "" {
		t.Fatalf("payload type mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTypeSyntaxTypedBlockParameters(t *testing.T) {
	t.Parallel()
	source := `def run(values)
  values.map do |value: int | string, label: string?|
    label
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := got.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", got.Statements[0])
	}
	exprStmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement, got %T", fn.Body[0])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression, got %T", exprStmt.Expr)
	}
	if call.Block == nil {
		t.Fatalf("expected call block")
	}

	wantParams := []ast.Param{
		{
			Name: "value",
			Type: &ast.TypeExpr{
				Name: "int | string",
				Kind: ast.TypeUnion,
				Union: []*ast.TypeExpr{
					{Name: "int", Kind: ast.TypeInt},
					{Name: "string", Kind: ast.TypeString},
				},
			},
		},
		{
			Name: "label",
			Type: &ast.TypeExpr{Name: "string?", Kind: ast.TypeString, Nullable: true},
		},
	}
	if diff := cmp.Diff(wantParams, call.Block.Params, astCmpOpts); diff != "" {
		t.Fatalf("block params mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTypeErrorCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "duplicate_shape_field",
			source: `def run(payload: { id: string, id: int })
  payload
end`,
			wantErr: "duplicate shape field id",
		},
		{
			name: "generic_args_on_scalar",
			source: `def run(value: int<string>)
  value
end`,
			wantErr: "type int does not accept type arguments",
		},
		{
			name: "array_with_two_args",
			source: `def run(values: array<int, string>)
  values
end`,
			wantErr: "array type expects exactly 1 type argument",
		},
		{
			name: "hash_with_one_arg",
			source: `def run(values: hash<string>)
  values
end`,
			wantErr: "hash type expects exactly 2 type arguments",
		},
		{
			name: "symbolic_boolean_shape_field",
			source: `def run(payload: { &&: bool })
  payload
end`,
			wantErr: "shape field name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("expected parse errors, got none")
			}
			if got := errs[0].Error(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("got error %q, want substring %q", got, tc.wantErr)
			}
		})
	}
}
