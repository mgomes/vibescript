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
		// Rejected entries whose key candidate itself begins with an opener
		// delimiter. Recovery must treat the cursor as already inside that
		// delimiter so its matching closer is not mistaken for the outer hash
		// boundary, otherwise parsing resumes mid-entry and cascades errors.
		`{ {a: 1} => v }`,
		`{ [a, b] => v }`,
		`{ (a) => v }`,
		`{ {a: 1} => v, ok: 1 }`,
		`{ ok: 1, [a, b] => v }`,
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

// TestParserHashValueOmission verifies that a label key with no value expands
// to a reference to the local variable of the same name, matching Ruby's hash
// value omission shorthand ({name:} is sugar for {name: name}).
func TestParserHashValueOmission(t *testing.T) {
	t.Parallel()

	source := `def run
  {name:, age:, role: "dev"}
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
						Key:   &ast.SymbolLiteral{Name: "name"},
						Value: &ast.Identifier{Name: "name"},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "age"},
						Value: &ast.Identifier{Name: "age"},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "role"},
						Value: &ast.StringLiteral{Value: "dev"},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserHashStringKeyValueOmissionRejected verifies that value omission is
// limited to label keys. Quoted keys such as {"name":} have no matching local
// to read, so they keep the missing-value diagnostic, matching Ruby.
func TestParserHashStringKeyValueOmissionRejected(t *testing.T) {
	t.Parallel()

	sources := []string{
		`{"name":}`,
		`{"name":, other: 1}`,
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

// TestParserKeywordHashLabels verifies that reserved-word tokens are accepted
// as hash labels when followed by an explicit value, matching Ruby's uniform
// treatment of keyword-shaped labels (e.g. `{rescue: 1}`).
func TestParserKeywordHashLabels(t *testing.T) {
	t.Parallel()

	keywords := []string{
		"begin", "rescue", "ensure", "raise", "export",
		"return", "class", "def", "enum", "end", "yield",
		"if", "unless", "while", "until", "case", "when",
		"true", "false", "nil", "self",
	}

	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  {" + keyword + ": 1}\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{
					Expr: &ast.HashLiteral{
						Pairs: []ast.HashPair{
							{
								Key:   &ast.SymbolLiteral{Name: keyword},
								Value: &ast.IntegerLiteral{Value: 1},
							},
						},
					},
				},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserKeywordHashLabelsNoSpaceStringValue verifies that a label-capable
// keyword whose colon abuts a quoted-string value (the no-space form
// {rescue:"x"}) parses as a string-valued label rather than being misread as a
// keyword followed by a quoted symbol. This guards the regression where quoted
// symbol scanning consumed the separator colon after a reserved-word label.
func TestParserKeywordHashLabelsNoSpaceStringValue(t *testing.T) {
	t.Parallel()

	keywords := []string{
		"begin", "rescue", "ensure", "raise", "export",
		"return", "class", "def", "enum", "end", "yield",
		"if", "unless", "while", "until", "case", "when",
	}

	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  {" + keyword + ":\"x\"}\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{
					Expr: &ast.HashLiteral{
						Pairs: []ast.HashPair{
							{
								Key:   &ast.SymbolLiteral{Name: keyword},
								Value: &ast.StringLiteral{Value: "x"},
							},
						},
					},
				},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
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
