package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserFunctionCaptureParameters(t *testing.T) {
	t.Parallel()
	source := `def collect(prefix, *items: array, **opts: hash, &block)
  nil
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name: "collect",
				Params: []ast.Param{
					{Name: "prefix"},
					{
						Name: "items",
						Kind: ast.ParamRest,
						Type: &ast.TypeExpr{Name: "array", Kind: ast.TypeArray},
					},
					{
						Name: "opts",
						Kind: ast.ParamKeywordRest,
						Type: &ast.TypeExpr{Name: "hash", Kind: ast.TypeHash},
					},
					{Name: "block", Kind: ast.ParamBlock},
				},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.NilLiteral{}},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionRequiredKeywordParameters(t *testing.T) {
	t.Parallel()
	source := `def takes(name:, age:)
  name
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name: "takes",
				Params: []ast.Param{
					{Name: "name", Kind: ast.ParamKeyword},
					{Name: "age", Kind: ast.ParamKeyword},
				},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.Identifier{Name: "name"}},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionParametersWithoutParens(t *testing.T) {
	t.Parallel()
	source := `def inc value
  value + 1
end

def add left, right
  left + right
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name:   "inc",
				Params: []ast.Param{{Name: "value"}},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "value"},
						Operator: ast.TokenPlus,
						Right:    &ast.IntegerLiteral{Value: 1},
					}},
				},
			},
			&ast.FunctionStmt{
				Name:   "add",
				Params: []ast.Param{{Name: "left"}, {Name: "right"}},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "left"},
						Operator: ast.TokenPlus,
						Right:    &ast.Identifier{Name: "right"},
					}},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionWithoutParensPreservesZeroArgBody(t *testing.T) {
	t.Parallel()
	source := `def run
  value
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name:   "run",
				Params: []ast.Param{},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.Identifier{Name: "value"}},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionParametersWithoutParensSupportTypesAndDefaults(t *testing.T) {
	t.Parallel()
	source := `def scale value: int, factor = 2 -> int
  value * factor
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name: "scale",
				Params: []ast.Param{
					{Name: "value", Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
					{Name: "factor", DefaultVal: &ast.IntegerLiteral{Value: 2}},
				},
				ReturnTy: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt},
				Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "value"},
						Operator: ast.TokenAsterisk,
						Right:    &ast.Identifier{Name: "factor"},
					}},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionCaptureParameterErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "ordinary_after_rest",
			source: `def bad(*items, tail)
  nil
end`,
			wantErr: "ordinary parameters must precede rest, keyword, keyword rest, and block capture parameters",
		},
		{
			name: "block_not_last",
			source: `def bad(&block, value)
  nil
end`,
			wantErr: "block capture parameter must be last",
		},
		{
			name: "duplicate_keyword_rest",
			source: `def bad(**left, **right)
  nil
end`,
			wantErr: "duplicate keyword rest parameter",
		},
		{
			name: "capture_default",
			source: `def bad(*items = [])
  nil
end`,
			wantErr: "capture parameters cannot have default values",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tt.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want %q", tt.source, tt.wantErr)
			}
			var got strings.Builder
			for _, err := range errs {
				got.WriteString(err.Error())
				got.WriteByte('\n')
			}
			if !strings.Contains(got.String(), tt.wantErr) {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", tt.source, got.String(), tt.wantErr)
			}
		})
	}
}
