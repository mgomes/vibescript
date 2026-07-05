package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserBeginRescueElseEnsure(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    1
  rescue
    2
  else
    3
  ensure
    4
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.TryStmt{
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 1}},
			},
			Rescues: []ast.RescueClause{
				{Body: []ast.Statement{
					&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
				}},
			},
			Else: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 3}},
			},
			Ensure: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 4}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserFunctionRescueElseEnsure(t *testing.T) {
	t.Parallel()

	source := `def run
  raise("boom")
rescue RuntimeError => err
  err.message
else
  "ok"
ensure
  cleanup
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 1 {
		t.Fatalf("function body length = %d, want 1", len(body))
	}
	stmt, ok := body[0].(*ast.TryStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.TryStmt", body[0])
	}
	if len(stmt.Rescues) != 1 {
		t.Fatalf("Rescues length = %d, want 1", len(stmt.Rescues))
	}
	clause := stmt.Rescues[0]
	if clause.Ty == nil || clause.Ty.Name != "RuntimeError" {
		t.Fatalf("rescue Ty = %#v, want RuntimeError", clause.Ty)
	}
	if clause.Binding != "err" {
		t.Fatalf("rescue Binding = %q, want err", clause.Binding)
	}
	wantBody := []ast.Statement{
		&ast.RaiseStmt{Value: &ast.StringLiteral{Value: "boom"}},
	}
	if diff := cmp.Diff(wantBody, stmt.Body, astCmpOpts); diff != "" {
		t.Fatalf("try body mismatch (-want +got):\n%s", diff)
	}
	wantRescue := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.MemberExpr{
			Object:   &ast.Identifier{Name: "err"},
			Property: "message",
		}},
	}
	if diff := cmp.Diff(wantRescue, clause.Body, astCmpOpts); diff != "" {
		t.Fatalf("try rescue mismatch (-want +got):\n%s", diff)
	}
	wantElse := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "ok"}},
	}
	if diff := cmp.Diff(wantElse, stmt.Else, astCmpOpts); diff != "" {
		t.Fatalf("try else mismatch (-want +got):\n%s", diff)
	}
	wantEnsure := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.Identifier{Name: "cleanup"}},
	}
	if diff := cmp.Diff(wantEnsure, stmt.Ensure, astCmpOpts); diff != "" {
		t.Fatalf("try ensure mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBeginRescueRubyStyleClauses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rescueLine  string
		wantType    string
		wantBinding string
	}{
		{name: "typed", rescueLine: "rescue RuntimeError", wantType: "RuntimeError"},
		{name: "typed_binding", rescueLine: "rescue RuntimeError => err", wantType: "RuntimeError", wantBinding: "err"},
		{name: "union_binding", rescueLine: "rescue AssertionError | RuntimeError => err", wantType: "AssertionError | RuntimeError", wantBinding: "err"},
		{name: "binding", rescueLine: "rescue => err", wantBinding: "err"},
		{name: "parenthesized_binding", rescueLine: "rescue(RuntimeError) => err", wantType: "RuntimeError", wantBinding: "err"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := `def run
  begin
    raise("boom")
  ` + tt.rescueLine + `
    "rescued"
  end
end`

			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			body := parsedFunctionBody(t, got)
			if len(body) != 1 {
				t.Fatalf("function body length = %d, want 1", len(body))
			}
			stmt, ok := body[0].(*ast.TryStmt)
			if !ok {
				t.Fatalf("body[0] = %T, want *ast.TryStmt", body[0])
			}
			if len(stmt.Rescues) != 1 {
				t.Fatalf("Rescues length = %d, want 1", len(stmt.Rescues))
			}
			clause := stmt.Rescues[0]
			if tt.wantType == "" {
				if clause.Ty != nil {
					t.Fatalf("rescue Ty = %#v, want nil", clause.Ty)
				}
			} else if clause.Ty == nil || clause.Ty.Name != tt.wantType {
				t.Fatalf("rescue Ty = %#v, want %q", clause.Ty, tt.wantType)
			}
			if clause.Binding != tt.wantBinding {
				t.Fatalf("rescue Binding = %q, want %q", clause.Binding, tt.wantBinding)
			}
			if len(clause.Body) != 1 {
				t.Fatalf("rescue body length = %d, want 1", len(clause.Body))
			}
			rescueExpr, ok := clause.Body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("rescue body[0] = %T, want *ast.ExprStmt", clause.Body[0])
			}
			lit, ok := rescueExpr.Expr.(*ast.StringLiteral)
			if !ok {
				t.Fatalf("rescue expression = %T, want *ast.StringLiteral", rescueExpr.Expr)
			}
			if lit.Value != "rescued" {
				t.Fatalf("rescue literal = %q, want rescued", lit.Value)
			}
		})
	}
}

func TestParserBeginRescueRubyStyleErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "unknown_type",
			source: `def run
  begin
    raise("boom")
  rescue NotARealError => err
    "rescued"
  end
end`,
			want: "unknown rescue error type NotARealError",
		},
		{
			name: "binding_identifier",
			source: `def run
  begin
    raise("boom")
  rescue => 123
    "rescued"
  end
end`,
			want: "rescue binding must be an identifier",
		},
		{
			name: "thin_arrow_binding",
			source: `def run
  begin
    raise("boom")
  rescue RuntimeError -> err
    "rescued"
  end
end`,
			want: "rescue binding must use =>",
		},
		{
			name: "thin_arrow_binding_with_nested_do_block",
			source: `def run
  begin
    raise("boom")
  rescue RuntimeError -> err
    values.each do |value|
      value
    end
  end
end`,
			want: "rescue binding must use =>",
		},
		{
			name: "bare_thin_arrow_binding",
			source: `def run
  begin
    raise("boom")
  rescue -> err
    "rescued"
  end
end`,
			want: "rescue binding must use =>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, tt.source)
			if len(errs) != 1 {
				t.Fatalf("parseSource(%q) errors = %d, want 1 containing %q: %v", tt.source, len(errs), tt.want, errs)
			}
			if got := errs[0].Error(); !strings.Contains(got, tt.want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", tt.source, got, tt.want)
			}
		})
	}
}

// TestParserMultipleRescueClauses pins that ordered rescue clauses parse into
// TryStmt.Rescues in source order, each keeping its own type, binding, and body.
func TestParserMultipleRescueClauses(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    raise("boom")
  rescue AssertionError
    "assertion"
  rescue RuntimeError => err
    err.message
  rescue
    "fallback"
  else
    "ok"
  ensure
    cleanup
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 1 {
		t.Fatalf("function body length = %d, want 1", len(body))
	}
	stmt, ok := body[0].(*ast.TryStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.TryStmt", body[0])
	}
	if len(stmt.Rescues) != 3 {
		t.Fatalf("Rescues length = %d, want 3", len(stmt.Rescues))
	}

	first := stmt.Rescues[0]
	if first.Ty == nil || first.Ty.Name != "AssertionError" || first.Binding != "" {
		t.Fatalf("Rescues[0] = Ty %#v Binding %q, want AssertionError with no binding", first.Ty, first.Binding)
	}
	second := stmt.Rescues[1]
	if second.Ty == nil || second.Ty.Name != "RuntimeError" || second.Binding != "err" {
		t.Fatalf("Rescues[1] = Ty %#v Binding %q, want RuntimeError => err", second.Ty, second.Binding)
	}
	third := stmt.Rescues[2]
	if third.Ty != nil || third.Binding != "" {
		t.Fatalf("Rescues[2] = Ty %#v Binding %q, want untyped with no binding", third.Ty, third.Binding)
	}
	for i, clause := range stmt.Rescues {
		if len(clause.Body) != 1 {
			t.Fatalf("Rescues[%d] body length = %d, want 1", i, len(clause.Body))
		}
	}
	if len(stmt.Else) != 1 || len(stmt.Ensure) != 1 {
		t.Fatalf("Else/Ensure lengths = %d/%d, want 1/1", len(stmt.Else), len(stmt.Ensure))
	}
}

// TestParserRescueAfterElseRejected pins Ruby's clause ordering: every rescue
// must precede else, so a rescue after else fails to parse.
func TestParserRescueAfterElseRejected(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    1
  rescue AssertionError
    2
  else
    3
  rescue RuntimeError
    4
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = none, want rescue-after-else rejection", source)
	}
}

func TestParserBeginElseWithoutRescue(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    1
  else
    2
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want begin else diagnostic", source)
	}
	if got, want := errs[0].Error(), "begin else requires rescue"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
