package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserQuotedHashKeys(t *testing.T) {
	t.Parallel()

	source := `def run
  {"name": "Ada", "first-name": "Lovelace", active: true}
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.HashLiteral{
				Pairs: []ast.HashPair{
					{
						Key:   &ast.StringLiteral{Value: "name"},
						Value: &ast.StringLiteral{Value: "Ada"},
					},
					{
						Key:   &ast.StringLiteral{Value: "first-name"},
						Value: &ast.StringLiteral{Value: "Lovelace"},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "active"},
						Value: &ast.BoolLiteral{Value: true},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserHashRocketsRejected(t *testing.T) {
	t.Parallel()

	sources := []string{
		`def run
  {:name => "Ada"}
end`,
		`def run
  {"first-name" => "Lovelace"}
end`,
		`def run
  {key => 1}
end`,
	}

	for _, source := range sources {
		_, errs := parseSource(t, source)
		if len(errs) == 0 {
			t.Fatalf("parseSource(%q) errors = none, want hash rocket rejection", source)
		}
		if got, want := errs[0].Error(), invalidHashPairMessage; !strings.Contains(got, want) {
			t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
		}
	}
}

// TestParserHashRocketSingleError verifies that rejecting the removed hash
// rocket syntax recovers to the next comma or closing brace so a single
// actionable hash-pair error is reported instead of cascading diagnostics
// (the parser previously left the cursor on the key and produced several
// unrelated errors for mixed literals such as `{:name => "Ada", good: 1}`).
func TestParserHashRocketSingleError(t *testing.T) {
	t.Parallel()

	sources := []string{
		`{:name => "Ada"}`,
		`{:name => "Ada", good: 1}`,
		`{good: 1, :name => "Ada"}`,
		`{1 => 2}`,
		`{name: 1, 2 => 3}`,
		`{name: [1, 2] => 3}`,
		`{a => {b => c}}`,
	}

	for _, source := range sources {
		_, errs := parseSource(t, source)
		if len(errs) != 1 {
			t.Fatalf("parseSource(%q) errors = %v, want exactly one", source, errs)
		}
		if got, want := errs[0].Error(), invalidHashPairMessage; !strings.Contains(got, want) {
			t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
		}
	}
}

// TestParserHashMissingValueSingleError verifies that a hash entry with a
// label key but no value recovers cleanly and continues parsing the remaining
// pairs, yielding only the missing-value diagnostic.
func TestParserHashMissingValueSingleError(t *testing.T) {
	t.Parallel()

	sources := []string{
		`{name:}`,
		`{name:, other: 1}`,
	}

	for _, source := range sources {
		_, errs := parseSource(t, source)
		if len(errs) != 1 {
			t.Fatalf("parseSource(%q) errors = %v, want exactly one", source, errs)
		}
		if got, want := errs[0].Error(), "missing value for hash key name"; !strings.Contains(got, want) {
			t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
		}
	}
}

func TestParserWordBooleanHashKeys(t *testing.T) {
	t.Parallel()

	source := `def run
  {and: 1, or: 2, not: 3}
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.HashLiteral{
				Pairs: []ast.HashPair{
					{
						Key:   &ast.SymbolLiteral{Name: "and"},
						Value: &ast.IntegerLiteral{Value: 1},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "or"},
						Value: &ast.IntegerLiteral{Value: 2},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "not"},
						Value: &ast.IntegerLiteral{Value: 3},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
