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

func TestParserModuloBeforeIndexedOrCalledWIIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name: "indexed_w",
			source: `def run
  total%w[0]
end`,
			wantExpr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "total"},
				Operator: ast.TokenPercent,
				Right: &ast.IndexExpr{
					Object: &ast.Identifier{Name: "w"},
					Index:  &ast.IntegerLiteral{Value: 0},
				},
			},
		},
		{
			name: "spaced_operator_indexed_w",
			source: `def run
  total % w[0]
end`,
			wantExpr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "total"},
				Operator: ast.TokenPercent,
				Right: &ast.IndexExpr{
					Object: &ast.Identifier{Name: "w"},
					Index:  &ast.IntegerLiteral{Value: 0},
				},
			},
		},
		{
			name: "called_i",
			source: `def run
  total%i(0)
end`,
			wantExpr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "total"},
				Operator: ast.TokenPercent,
				Right: &ast.CallExpr{
					Callee: &ast.Identifier{Name: "i"},
					Args: []ast.Expression{
						&ast.IntegerLiteral{Value: 0},
					},
					KwArgs: []ast.KeywordArg{},
				},
			},
		},
		{
			name: "spaced_operator_called_i",
			source: `def run
  total % i(0)
end`,
			wantExpr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "total"},
				Operator: ast.TokenPercent,
				Right: &ast.CallExpr{
					Callee: &ast.Identifier{Name: "i"},
					Args: []ast.Expression{
						&ast.IntegerLiteral{Value: 0},
					},
					KwArgs: []ast.KeywordArg{},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: tc.wantExpr},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserLocalModuloBeforeCompactWIIdentifiers(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  w = [3]
  total %w[0]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "total"},
			Value:  &ast.IntegerLiteral{Value: 10},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "w"},
			Value: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.IntegerLiteral{Value: 3},
			}},
		},
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "total"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "w"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// A percent-array argument whose interior looks like a comment must not
// cause the lexer to swallow the closing delimiter and following lines.
func TestParserPercentArrayArgumentDoesNotCommentOutFollowingLines(t *testing.T) {
	t.Parallel()

	source := `def run
  collect %w[#]
  after
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "collect"},
			Args: []ast.Expression{
				&ast.ArrayLiteral{Elements: []ast.Expression{
					&ast.StringLiteral{Value: "#"},
				}},
			},
			KwArgs: []ast.KeywordArg{},
		}},
		&ast.ExprStmt{Expr: &ast.Identifier{Name: "after"}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// A local declared in an enclosing snippet scope must not leak into a
// function body: inside the function the name is not local, so the
// percent literal is a parenless call argument, not modulo.
func TestParserPercentArrayArgumentIgnoresEnclosingSnippetLocals(t *testing.T) {
	t.Parallel()

	source := `collect = 1
def run
  collect %w[ok]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(got.Statements) != 2 {
		t.Fatalf("parseSource returned %d statements, want 2", len(got.Statements))
	}
	fn, ok := got.Statements[1].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("statement[1] = %T, want *ast.FunctionStmt", got.Statements[1])
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "collect"},
			Args: []ast.Expression{
				&ast.ArrayLiteral{Elements: []ast.Expression{
					&ast.StringLiteral{Value: "ok"},
				}},
			},
			KwArgs: []ast.KeywordArg{},
		}},
	}
	if diff := cmp.Diff(wantBody, fn.Body, astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// Blocks still close over enclosing locals: a name assigned in the
// surrounding scope remains local inside a block, so the percent literal
// there is modulo, not a call argument. This guards the function-scope
// boundary from over-broadening into block scopes.
func TestParserBlockClosesOverEnclosingLocalForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  w = [3]
  [1].each do |n|
    total %w[0]
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 3 {
		t.Fatalf("function body has %d statements, want 3", len(body))
	}
	exprStmt, ok := body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[2] = %T, want *ast.ExprStmt", body[2])
	}
	eachCall, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("body[2].Expr = %T, want *ast.CallExpr", exprStmt.Expr)
	}
	if eachCall.Block == nil {
		t.Fatalf("each call has no block")
	}

	wantBlockBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "total"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "w"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
	}
	if diff := cmp.Diff(wantBlockBody, eachCall.Block.Body, astCmpOpts); diff != "" {
		t.Fatalf("block body mismatch (-want +got):\n%s", diff)
	}
}

// A for-loop iterator binds a local in the surrounding scope, so the
// percent literal in its body is modulo, not a parenless call argument.
func TestParserForIteratorIsLocalForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  items = [1]
  for w in items
    w %w[0]
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 2 {
		t.Fatalf("function body has %d statements, want 2", len(body))
	}
	forStmt, ok := body[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("body[1] = %T, want *ast.ForStmt", body[1])
	}

	wantForBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "w"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "w"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
	}
	if diff := cmp.Diff(wantForBody, forStmt.Body, astCmpOpts); diff != "" {
		t.Fatalf("for body mismatch (-want +got):\n%s", diff)
	}
}

// A rescue binding introduces a local in the surrounding scope, so the
// percent literal in the rescue body is modulo, not a call argument.
func TestParserRescueBindingIsLocalForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  w = [3]
  begin
    1
  rescue => err
    err %w[0]
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 2 {
		t.Fatalf("function body has %d statements, want 2", len(body))
	}
	tryStmt, ok := body[1].(*ast.TryStmt)
	if !ok {
		t.Fatalf("body[1] = %T, want *ast.TryStmt", body[1])
	}

	wantRescueBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "err"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "w"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
	}
	if diff := cmp.Diff(wantRescueBody, tryStmt.Rescue, astCmpOpts); diff != "" {
		t.Fatalf("rescue body mismatch (-want +got):\n%s", diff)
	}
}

// A function-level rescue tail is still inside the function scope, so it
// resolves function-body locals and parses the percent literal as modulo.
func TestParserFunctionRescueTailSeesFunctionLocalsForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  w = [3]
rescue
  total %w[0]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 1 {
		t.Fatalf("function body has %d statements, want 1 try statement", len(body))
	}
	tryStmt, ok := body[0].(*ast.TryStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.TryStmt", body[0])
	}

	wantRescueBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "total"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "w"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
	}
	if diff := cmp.Diff(wantRescueBody, tryStmt.Rescue, astCmpOpts); diff != "" {
		t.Fatalf("rescue body mismatch (-want +got):\n%s", diff)
	}
}

// A rescue binding is scoped to the rescue body only; after the handler the
// name is no longer local, so a later percent literal is a parenless call.
func TestParserRescueBindingDoesNotLeakAfterHandler(t *testing.T) {
	t.Parallel()

	source := `begin
  1
rescue => collect
  2
end
collect %w[ok]`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(got.Statements) != 2 {
		t.Fatalf("parseSource returned %d statements, want 2", len(got.Statements))
	}

	wantStmt := &ast.ExprStmt{Expr: &ast.CallExpr{
		Callee: &ast.Identifier{Name: "collect"},
		Args: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "ok"},
			}},
		},
		KwArgs: []ast.KeywordArg{},
	}}
	if diff := cmp.Diff(wantStmt, got.Statements[1], astCmpOpts); diff != "" {
		t.Fatalf("post-rescue statement mismatch (-want +got):\n%s", diff)
	}
}

// A local assigned inside a rescue body belongs to the surrounding scope
// (only the exception binding is body-local), so a later percent literal
// that uses it is modulo, not a parenless call.
func TestParserRescueBodyAssignmentLeaksToOuterScope(t *testing.T) {
	t.Parallel()

	source := `begin
  1
rescue => err
  total = 10
  w = [3]
end
total %w[0]`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(got.Statements) != 2 {
		t.Fatalf("parseSource returned %d statements, want 2", len(got.Statements))
	}

	wantStmt := &ast.ExprStmt{Expr: &ast.BinaryExpr{
		Left:     &ast.Identifier{Name: "total"},
		Operator: ast.TokenPercent,
		Right: &ast.IndexExpr{
			Object: &ast.Identifier{Name: "w"},
			Index:  &ast.IntegerLiteral{Value: 0},
		},
	}}
	if diff := cmp.Diff(wantStmt, got.Statements[1], astCmpOpts); diff != "" {
		t.Fatalf("post-rescue statement mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentArrayParenlessCallArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name: "multi_word_array",
			source: `def run
  collect %w[alpha beta]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "alpha"},
						&ast.StringLiteral{Value: "beta"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "single_word_array",
			source: `def run
  collect %w[ok]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "ok"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "single_numeric_word_array",
			source: `def run
  collect %w[123]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "123"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "single_symbol_array",
			source: `def run
  collect %i[ok]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.SymbolLiteral{Name: "ok"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "single_numeric_symbol_array",
			source: `def run
  collect %i[123]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.SymbolLiteral{Name: "123"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "symbol_array_member_call",
			source: `def run
  logger.info %i[ok]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "logger"},
					Property: "info",
				},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.SymbolLiteral{Name: "ok"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: tc.wantExpr},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
