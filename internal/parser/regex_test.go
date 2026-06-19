package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserRejectsRegexLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  /ID-[0-9]+/
end`

	requireSingleRegexLiteralError(t, source)
}

func TestParserRejectsRegexLiteralInCallArgument(t *testing.T) {
	t.Parallel()

	source := `def run
  Regex.match(/ID-[0-9]+/, text)
end`

	requireSingleRegexLiteralError(t, source)
}

func TestParserRejectsRegexLiteralWithEscapedSlash(t *testing.T) {
	t.Parallel()

	source := `def run
  Regex.match(/a\/b/, text)
end`

	requireSingleRegexLiteralError(t, source)
}

func TestParserRejectsRegexLiteralWithSlashInCharacterClass(t *testing.T) {
	t.Parallel()

	source := `def run
  Regex.match(/[a/b]/, text)
end`

	requireSingleRegexLiteralError(t, source)
}

func TestParserRejectsRegexLiteralWithFlags(t *testing.T) {
	t.Parallel()

	source := `def run
  Regex.match(/id/i, text)
end`

	requireSingleRegexLiteralError(t, source)
}

func requireSingleRegexLiteralError(t *testing.T, source string) {
	t.Helper()

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "regex literals are not supported"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}

func TestParserDivisionStillUsesSlashOperator(t *testing.T) {
	t.Parallel()

	source := `def run
  10 / 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 10},
				Operator: ast.TokenSlash,
				Right:    &ast.IntegerLiteral{Value: 2},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
