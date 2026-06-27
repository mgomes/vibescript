package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserShovelExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  values << element
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "values"},
				Operator: ast.TokenShovel,
				Right:    &ast.Identifier{Name: "element"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserIntersectionExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  left & right
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "left"},
				Operator: ast.TokenAmpersand,
				Right:    &ast.Identifier{Name: "right"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserCollectionOperatorPrecedence pins the relative binding strengths of
// the new operators against the existing arithmetic and comparison operators,
// mirroring Ruby's ordering: "+"/"-" bind tighter than "<<", which binds
// tighter than "&", which binds tighter than comparison.
func TestParserCollectionOperatorPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   ast.Expression
	}{
		{
			name:   "plus_binds_tighter_than_shovel",
			source: "a + b << c",
			want: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "a"},
					Operator: ast.TokenPlus,
					Right:    &ast.Identifier{Name: "b"},
				},
				Operator: ast.TokenShovel,
				Right:    &ast.Identifier{Name: "c"},
			},
		},
		{
			name:   "shovel_binds_tighter_than_intersection",
			source: "a << b & c",
			want: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "a"},
					Operator: ast.TokenShovel,
					Right:    &ast.Identifier{Name: "b"},
				},
				Operator: ast.TokenAmpersand,
				Right:    &ast.Identifier{Name: "c"},
			},
		},
		{
			name:   "intersection_binds_tighter_than_equality",
			source: "a & b == c",
			want: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "a"},
					Operator: ast.TokenAmpersand,
					Right:    &ast.Identifier{Name: "b"},
				},
				Operator: ast.TokenEQ,
				Right:    &ast.Identifier{Name: "c"},
			},
		},
		{
			name:   "shovel_is_left_associative",
			source: "a << b << c",
			want: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "a"},
					Operator: ast.TokenShovel,
					Right:    &ast.Identifier{Name: "b"},
				},
				Operator: ast.TokenShovel,
				Right:    &ast.Identifier{Name: "c"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run\n  " + tc.source + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}
			wantBody := []ast.Statement{&ast.ExprStmt{Expr: tc.want}}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserCollectionOperatorAssignment confirms the operators parse on the
// right-hand side of an assignment, the idiomatic accumulator forms.
func TestParserCollectionOperatorAssignment(t *testing.T) {
	t.Parallel()

	source := `def run
  values = values << element
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "values"},
			Value: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "values"},
				Operator: ast.TokenShovel,
				Right:    &ast.Identifier{Name: "element"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserCollectionOperatorContinuesAcrossNewline confirms a trailing
// operator continues the expression onto the next physical line, matching the
// other binary operators.
func TestParserCollectionOperatorContinuesAcrossNewline(t *testing.T) {
	t.Parallel()

	source := `def run
  result = left
    &
    right
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "result"},
			Value: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "left"},
				Operator: ast.TokenAmpersand,
				Right:    &ast.Identifier{Name: "right"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserSpacedAmpersandIsIntersectionAfterLocal pins the spacing
// disambiguation: a spaced "&" after a known local parses as the binary
// intersection operator rather than a parenless block-pass argument.
func TestParserSpacedAmpersandIsIntersectionAfterLocal(t *testing.T) {
	t.Parallel()

	source := `def run
  values = [1, 2, 3]
  values & [2, 3]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 2 {
		t.Fatalf("function body has %d statements, want 2", len(body))
	}
	stmt, ok := body[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("second statement is %T, want *ast.ExprStmt", body[1])
	}
	binary, ok := stmt.Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expression is %T, want *ast.BinaryExpr", stmt.Expr)
	}
	if binary.Operator != ast.TokenAmpersand {
		t.Fatalf("operator = %q, want %q", binary.Operator, ast.TokenAmpersand)
	}
}

// TestParserIntersectionSpacingShapes pins the spacing disambiguation between
// the binary intersection operator and the (unsupported) block-pass argument
// after a local identifier or a member expression. Ruby reads only "foo &bar"
// (detached from the callee, flush against the operand) as a block-pass; the
// flush-both-sides "foo&bar", the spaced "foo & bar", and the trailing "&"
// line continuation are all the binary operator.
func TestParserIntersectionSpacingShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "flush_both_sides_local", expr: "values&other"},
		{name: "flush_both_sides_member", expr: "self.values&other"},
		{name: "spaced_local", expr: "values & other"},
		{name: "spaced_member", expr: "self.values & other"},
		{name: "trailing_continuation_local", expr: "values &\n    other"},
		{name: "trailing_continuation_member", expr: "self.values &\n    other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run\n  values = [1, 2, 3]\n  " + tc.expr + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			body := parsedFunctionBody(t, got)
			if len(body) != 2 {
				t.Fatalf("function body has %d statements, want 2", len(body))
			}
			stmt, ok := body[1].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("second statement is %T, want *ast.ExprStmt", body[1])
			}
			binary, ok := stmt.Expr.(*ast.BinaryExpr)
			if !ok {
				t.Fatalf("expression is %T, want *ast.BinaryExpr", stmt.Expr)
			}
			if binary.Operator != ast.TokenAmpersand {
				t.Fatalf("operator = %q, want %q", binary.Operator, ast.TokenAmpersand)
			}
			if _, ok := binary.Right.(*ast.Identifier); !ok {
				t.Fatalf("right operand is %T, want *ast.Identifier", binary.Right)
			}
		})
	}
}

// TestParserBlockPassShapeReportsDiagnostic confirms the spacing rule still
// surfaces the helpful block-pass diagnostic for the "foo &bar" shape, where
// the ampersand is detached from the callee but flush against the operand,
// after both a local identifier and a member expression.
func TestParserBlockPassShapeReportsDiagnostic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "after_identifier",
			source: `def run
  collect &block
end`,
		},
		{
			name: "after_member",
			source: `def run
  self.collect &block
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatal("expected a parse error for ampersand block-pass shape")
			}
			if !strings.Contains(errs[0].Error(), "ampersand block forwarding") {
				t.Fatalf("error = %q, want block-pass diagnostic", errs[0].Error())
			}
		})
	}
}

// TestParserFlushAmpersandReportsBlockPass confirms the spacing rule still
// surfaces the helpful block-pass diagnostic for the flush form "foo &block",
// which Ruby reads as passing a block rather than the binary operator.
func TestParserFlushAmpersandReportsBlockPass(t *testing.T) {
	t.Parallel()

	source := `def run
  collect &block
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for flush ampersand block-pass")
	}
	if !strings.Contains(errs[0].Error(), "ampersand block forwarding") {
		t.Fatalf("error = %q, want block-pass diagnostic", errs[0].Error())
	}
}

// TestLexerDisambiguatesLessThanSigils pins the maximal-munch rule for "<"
// runs: "<" is comparison, "<=" is less-or-equal, "<=>" is the spaceship, and
// "<<" is the shovel operator.
func TestLexerDisambiguatesLessThanSigils(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.TokenType
	}{
		{
			name:   "single_is_less_than",
			source: "<",
			want:   []ast.TokenType{ast.TokenLT, ast.TokenEOF},
		},
		{
			name:   "less_or_equal",
			source: "<=",
			want:   []ast.TokenType{ast.TokenLTE, ast.TokenEOF},
		},
		{
			name:   "spaceship",
			source: "<=>",
			want:   []ast.TokenType{ast.TokenSpaceship, ast.TokenEOF},
		},
		{
			name:   "double_is_shovel",
			source: "<<",
			want:   []ast.TokenType{ast.TokenShovel, ast.TokenEOF},
		},
		{
			name:   "triple_is_shovel_then_less_than",
			source: "<<<",
			want:   []ast.TokenType{ast.TokenShovel, ast.TokenLT, ast.TokenEOF},
		},
		{
			name:   "shovel_with_operands",
			source: "a << b",
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenShovel, ast.TokenIdent, ast.TokenEOF},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lex := newLexer(tc.source)
			var got []ast.TokenType
			for {
				tok := lex.NextToken()
				got = append(got, tok.Type)
				if tok.Type == ast.TokenEOF {
					break
				}
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("token stream mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
