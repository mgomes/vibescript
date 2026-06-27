package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestLexerScansSafeNavigationOperator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.TokenType
	}{
		{
			name:   "safe_navigation",
			source: "user&.name",
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenSafeNav, ast.TokenIdent, ast.TokenEOF},
		},
		{
			name:   "block_pass_stays_ampersand",
			source: "&block",
			want:   []ast.TokenType{ast.TokenAmpersand, ast.TokenIdent, ast.TokenEOF},
		},
		{
			name:   "logical_and_stays_and",
			source: "a && b",
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenAnd, ast.TokenIdent, ast.TokenEOF},
		},
	}

	for _, tc := range tests {
		tc := tc
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

func TestParserParsesSafeNavigationMemberRead(t *testing.T) {
	t.Parallel()

	member := firstStatementExpr(t, `def run
  user&.name
end`).(*ast.MemberExpr)

	if member == nil {
		t.Fatal("expected member expression")
	}
	if !member.Safe {
		t.Fatalf("member.Safe = false, want true")
	}
	if member.Property != "name" {
		t.Fatalf("member.Property = %q, want %q", member.Property, "name")
	}
	object, ok := member.Object.(*ast.Identifier)
	if !ok || object.Name != "user" {
		t.Fatalf("member.Object = %#v, want identifier user", member.Object)
	}
}

func TestParserParsesSafeNavigationMethodCall(t *testing.T) {
	t.Parallel()

	call := firstStatementExpr(t, `def run
  user&.profile("public")
end`).(*ast.CallExpr)

	if !call.Safe {
		t.Fatalf("call.Safe = false, want true")
	}
	member, ok := call.Callee.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("call.Callee = %T, want *ast.MemberExpr", call.Callee)
	}
	if !member.Safe {
		t.Fatalf("callee member.Safe = false, want true")
	}
	if member.Property != "profile" {
		t.Fatalf("member.Property = %q, want %q", member.Property, "profile")
	}
	if len(call.Args) != 1 {
		t.Fatalf("call.Args = %d, want 1", len(call.Args))
	}
}

func TestParserOrdinaryMemberAccessIsNotSafe(t *testing.T) {
	t.Parallel()

	member := firstStatementExpr(t, `def run
  user.name
end`).(*ast.MemberExpr)

	if member.Safe {
		t.Fatalf("member.Safe = true, want false for ordinary access")
	}
}

func TestParserParsesWrappedSafeNavigationCall(t *testing.T) {
	t.Parallel()

	fn := firstFunction(t, `def run
  user&.
    profile("public")
  "fallback"
end`)
	if len(fn.Body) != 2 {
		t.Fatalf("fn.Body = %d statements, want 2", len(fn.Body))
	}
	stmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("fn.Body[0] = %T, want *ast.ExprStmt", fn.Body[0])
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("stmt.Expr = %T, want *ast.CallExpr", stmt.Expr)
	}
	if !call.Safe {
		t.Fatalf("call.Safe = false, want true")
	}
}

func TestParserParsesSafeNavigationAsCallArgument(t *testing.T) {
	t.Parallel()

	call := firstStatementExpr(t, `def run
  inspect(user&.name, "fallback")
end`).(*ast.CallExpr)

	if len(call.Args) != 2 {
		t.Fatalf("call.Args = %d, want 2", len(call.Args))
	}
	member, ok := call.Args[0].(*ast.MemberExpr)
	if !ok {
		t.Fatalf("call.Args[0] = %T, want *ast.MemberExpr", call.Args[0])
	}
	if !member.Safe {
		t.Fatalf("argument member.Safe = false, want true")
	}
}

func TestParserRejectsSafeNavigationAssignmentTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{name: "direct_member", target: `user&.name`},
		{name: "nested_member", target: `user&.profile.name`},
		{name: "indexed", target: `user&.items[0]`},
		{name: "call_in_chain", target: `user&.fetch().name`},
		{name: "compound_assign", target: `user&.count`},
		{name: "safe_in_middle", target: `user.profile&.name`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			operator := "="
			if tt.name == "compound_assign" {
				operator = "+="
			}
			_, errs := parseSource(t, "def run\n  "+tt.target+" "+operator+" 1\nend")
			if len(errs) != 1 {
				t.Fatalf("expected 1 parse error, got %d: %v", len(errs), errs)
			}
			if got, want := errs[0].Error(), "safe navigation cannot be used as an assignment target"; !strings.Contains(got, want) {
				t.Fatalf("error = %q, want substring %q", got, want)
			}
		})
	}
}

func TestParserRejectsSafeNavigationDestructureTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		targets string
	}{
		{name: "first_member", targets: `user&.name, x`},
		{name: "later_indexed", targets: `x, user&.items[0]`},
		{name: "nested_member", targets: `user&.profile.name, x`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, "def run\n  "+tt.targets+" = 1, 2\nend")
			if len(errs) == 0 {
				t.Fatalf("expected a parse error, got none")
			}
			if got, want := errs[0].Error(), "safe navigation cannot be used as an assignment target"; !strings.Contains(got, want) {
				t.Fatalf("error = %q, want substring %q", got, want)
			}
		})
	}
}

func TestParserAcceptsOrdinaryAssignmentTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{name: "member", target: `user.name`},
		{name: "nested_member", target: `user.profile.name`},
		{name: "indexed", target: `user.items[0]`},
		{name: "identifier", target: `value`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, "def run\n  "+tt.target+" = 1\nend")
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tt.target, errs)
			}
		})
	}
}

func firstFunction(t *testing.T, source string) *ast.FunctionStmt {
	t.Helper()
	program, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("program.Statements[0] = %T, want *ast.FunctionStmt", program.Statements[0])
	}
	return fn
}

func firstStatementExpr(t *testing.T, source string) ast.Expression {
	t.Helper()
	fn := firstFunction(t, source)
	stmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("fn.Body[0] = %T, want *ast.ExprStmt", fn.Body[0])
	}
	return stmt.Expr
}
