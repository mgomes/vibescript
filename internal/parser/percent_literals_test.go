package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserPercentWordArrayLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  %w[alpha beta gamma]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.StringLiteral{Value: "alpha"},
			&ast.StringLiteral{Value: "beta"},
			&ast.StringLiteral{Value: "gamma"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentSymbolArrayLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  %i[alpha beta gamma]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.SymbolLiteral{Name: "alpha"},
			&ast.SymbolLiteral{Name: "beta"},
			&ast.SymbolLiteral{Name: "gamma"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentArrayLiteralEscapes(t *testing.T) {
	t.Parallel()

	source := `def run
  [%w[alpha\ beta bracket\] slash\\ literal\n], %i[alpha\ beta]]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "alpha beta"},
				&ast.StringLiteral{Value: "bracket]"},
				&ast.StringLiteral{Value: `slash\`},
				&ast.StringLiteral{Value: `literal\n`},
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.SymbolLiteral{Name: "alpha beta"},
			}},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentArrayLiteralAlternativeDelimiters(t *testing.T) {
	t.Parallel()

	source := `def run
  [%w(alpha beta), %i{gamma delta}, %w<left right>, %i!open closed!]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "alpha"},
				&ast.StringLiteral{Value: "beta"},
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.SymbolLiteral{Name: "gamma"},
				&ast.SymbolLiteral{Name: "delta"},
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "left"},
				&ast.StringLiteral{Value: "right"},
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.SymbolLiteral{Name: "open"},
				&ast.SymbolLiteral{Name: "closed"},
			}},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentArrayLiteralEmptyAndNestedDelimiters(t *testing.T) {
	t.Parallel()

	source := `def run
  [%w[], %w[[]]]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "[]"},
			}},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
