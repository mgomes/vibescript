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
			Rescue: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
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
	if stmt.RescueTy == nil || stmt.RescueTy.Name != "RuntimeError" {
		t.Fatalf("RescueTy = %#v, want RuntimeError", stmt.RescueTy)
	}
	if stmt.RescueBinding != "err" {
		t.Fatalf("RescueBinding = %q, want err", stmt.RescueBinding)
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
	if diff := cmp.Diff(wantRescue, stmt.Rescue, astCmpOpts); diff != "" {
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
			if tt.wantType == "" {
				if stmt.RescueTy != nil {
					t.Fatalf("RescueTy = %#v, want nil", stmt.RescueTy)
				}
			} else if stmt.RescueTy == nil || stmt.RescueTy.Name != tt.wantType {
				t.Fatalf("RescueTy = %#v, want %q", stmt.RescueTy, tt.wantType)
			}
			if stmt.RescueBinding != tt.wantBinding {
				t.Fatalf("RescueBinding = %q, want %q", stmt.RescueBinding, tt.wantBinding)
			}
			if len(stmt.Rescue) != 1 {
				t.Fatalf("Rescue length = %d, want 1", len(stmt.Rescue))
			}
			rescueExpr, ok := stmt.Rescue[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("Rescue[0] = %T, want *ast.ExprStmt", stmt.Rescue[0])
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
