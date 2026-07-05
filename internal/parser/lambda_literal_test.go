package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// firstFunctionBody unwraps the body of the first function statement in a
// parsed program, the shape every lambda-literal test here uses.
func firstFunctionBody(t *testing.T, program *ast.Program) []ast.Statement {
	t.Helper()
	if len(program.Statements) == 0 {
		t.Fatal("program has no statements")
	}
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.FunctionStmt", program.Statements[0])
	}
	return fn.Body
}

func lambdaFromAssignment(t *testing.T, stmt ast.Statement) *ast.BlockLiteral {
	t.Helper()
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok {
		t.Fatalf("statement is %T, want *ast.AssignStmt", stmt)
	}
	block, ok := assign.Value.(*ast.BlockLiteral)
	if !ok {
		t.Fatalf("assigned value is %T, want *ast.BlockLiteral", assign.Value)
	}
	if !block.Lambda {
		t.Fatal("block literal is not marked Lambda")
	}
	return block
}

// TestParserStabbyLambdaLiteral pins the stabby lambda forms: parenthesized
// parameters, no parameters, and the do/end body.
func TestParserStabbyLambdaLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		wantParams []string
	}{
		{
			name: "paren_params_brace_body",
			source: `def run
  fn = ->(a, b) { a + b }
end`,
			wantParams: []string{"a", "b"},
		},
		{
			name: "empty_parens",
			source: `def run
  fn = ->() { 1 }
end`,
			wantParams: []string{},
		},
		{
			name: "no_parens",
			source: `def run
  fn = -> { 1 }
end`,
			wantParams: []string{},
		},
		{
			name: "do_end_body",
			source: `def run
  fn = ->(n) do
    n * 2
  end
end`,
			wantParams: []string{"n"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			program, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors: %v", tc.source, errs)
			}
			body := firstFunctionBody(t, program)
			block := lambdaFromAssignment(t, body[0])
			if len(block.Params) != len(tc.wantParams) {
				t.Fatalf("params = %d, want %d", len(block.Params), len(tc.wantParams))
			}
			for i, want := range tc.wantParams {
				if block.Params[i].Name != want {
					t.Fatalf("param %d = %q, want %q", i, block.Params[i].Name, want)
				}
			}
		})
	}
}

// TestParserStabbyLambdaImplicitParams confirms a lambda with no parameter
// list infers implicit block parameters, matching block literals.
func TestParserStabbyLambdaImplicitParams(t *testing.T) {
	t.Parallel()

	program, errs := parseSource(t, `def run
  fn = -> { it + _1 }
end`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	block := lambdaFromAssignment(t, firstFunctionBody(t, program)[0])
	if len(block.Params) != 0 {
		t.Fatalf("params = %d, want 0", len(block.Params))
	}
	if len(block.ImplicitParams) == 0 {
		t.Fatal("expected inferred implicit params")
	}
}

// TestParserStabbyLambdaErrors pins the diagnostics for malformed lambda
// literals.
func TestParserStabbyLambdaErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "missing_body",
			source: `def run
  fn = ->(a) + 1
end`,
			want: "lambda body opened with { or do",
		},
		{
			name: "trailing_comma",
			source: `def run
  fn = ->(a,) { a }
end`,
			want: "trailing comma in lambda parameter list",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) expected errors", tc.source)
			}
			if got := errs[0].Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want substring %q", got, tc.want)
			}
		})
	}
}

// TestParserReturnAnnotationStaysOnSignatureLine pins the disambiguation
// between the `-> Type` return annotation (same line as the def signature)
// and a stabby lambda literal opening the body on the next line.
func TestParserReturnAnnotationStaysOnSignatureLine(t *testing.T) {
	t.Parallel()

	program, errs := parseSource(t, `def annotated(x) -> Int
  x
end

def lambda_body
  ->(n) { n }
end`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	annotated, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok || annotated.ReturnTy == nil {
		t.Fatalf("annotated = %T (returnTy %v), want annotated function", program.Statements[0], annotated.ReturnTy)
	}
	lambdaFn, ok := program.Statements[1].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("second statement is %T, want *ast.FunctionStmt", program.Statements[1])
	}
	if lambdaFn.ReturnTy != nil {
		t.Fatal("lambda_body must not parse a return annotation")
	}
	expr, ok := lambdaFn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("lambda_body first statement is %T, want *ast.ExprStmt", lambdaFn.Body[0])
	}
	block, ok := expr.Expr.(*ast.BlockLiteral)
	if !ok || !block.Lambda {
		t.Fatalf("lambda_body expression is %T (lambda %v), want lambda literal", expr.Expr, ok && block.Lambda)
	}
}
