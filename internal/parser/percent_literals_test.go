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

func TestParserPercentInterpolatedWordArrayLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  %W[hello #{name} world]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.StringLiteral{Value: "hello"},
			&ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: &ast.Identifier{Name: "name"}},
			}},
			&ast.StringLiteral{Value: "world"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentInterpolatedSymbolArrayLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  %I[hello #{name} world]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.SymbolLiteral{Name: "hello"},
			&ast.InterpolatedSymbol{Parts: []ast.StringPart{
				ast.StringExpr{Expr: &ast.Identifier{Name: "name"}},
			}},
			&ast.SymbolLiteral{Name: "world"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// %W/%I entries without interpolation collapse to the same plain literal nodes
// the lowercase forms produce, so downstream consumers see no difference.
func TestParserPercentInterpolatedArrayLiteralPlainEntries(t *testing.T) {
	t.Parallel()

	source := `def run
  [%W[alpha beta], %I[gamma delta]]
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
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// A space inside #{...} must not split a %W/%I word, and an escaped #{ is a
// literal rather than an interpolation marker.
func TestParserPercentInterpolatedArrayLiteralWordSplitting(t *testing.T) {
	t.Parallel()

	source := `def run
  [%W[a #{b + c} d], %W[lit\#{x} tail]]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a"},
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "b"},
						Operator: ast.TokenPlus,
						Right:    &ast.Identifier{Name: "c"},
					}},
				}},
				&ast.StringLiteral{Value: "d"},
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "lit#{x}"},
				&ast.StringLiteral{Value: "tail"},
			}},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// %W applies double-quoted escape semantics: \t becomes a tab and \  becomes a
// literal space that does not split the word, unlike the lowercase %w form.
func TestParserPercentInterpolatedArrayLiteralEscapes(t *testing.T) {
	t.Parallel()

	source := `def run
  %W[tab\there a\ b]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.StringLiteral{Value: "tab\there"},
			&ast.StringLiteral{Value: "a b"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPercentInterpolatedArrayLiteralEmptyAndAlternativeDelimiters(t *testing.T) {
	t.Parallel()

	source := `def run
  [%W[], %I{}, %W(a #{x}), %I<b #{x}>]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	interpString := &ast.InterpolatedString{Parts: []ast.StringPart{
		ast.StringExpr{Expr: &ast.Identifier{Name: "x"}},
	}}
	interpSymbol := &ast.InterpolatedSymbol{Parts: []ast.StringPart{
		ast.StringExpr{Expr: &ast.Identifier{Name: "x"}},
	}}
	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.ArrayLiteral{Elements: []ast.Expression{}},
			&ast.ArrayLiteral{Elements: []ast.Expression{}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a"},
				interpString,
			}},
			&ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.SymbolLiteral{Name: "b"},
				interpSymbol,
			}},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// A delimiter that appears inside a %W/%I interpolation expression—including one
// nested in a quoted string such as %W[#{"]"}]—must not close the literal early.
// The scanner skips over #{...} spans the same way ordinary double-quoted string
// interpolation does, so these forms parse as a single interpolated entry rather
// than truncating at the inner delimiter. Both the direct-literal lexer path and
// the parenless-argument scan path are exercised. Verified against Ruby:
// %W[#{"]"}] => ["]"], %W[#{"]"}foo bar] => ["]foo", "bar"], %I{#{"}"}} => [:"}"].
func TestParserPercentInterpolatedArrayLiteralDelimiterInsideInterpolation(t *testing.T) {
	t.Parallel()

	interpClose := &ast.InterpolatedString{Parts: []ast.StringPart{
		ast.StringExpr{Expr: &ast.StringLiteral{Value: "]"}},
	}}

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name:   "bracket_in_string_only_entry",
			source: "def run\n  %W[#{\"]\"}]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				interpClose,
			}},
		},
		{
			name:   "bracket_in_string_with_trailing_words",
			source: "def run\n  %W[#{\"]\"}foo bar]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.StringLiteral{Value: "]"}},
					ast.StringText{Text: "foo"},
				}},
				&ast.StringLiteral{Value: "bar"},
			}},
		},
		{
			name:   "brace_delimiter_with_brace_in_string",
			source: "def run\n  %I{#{\"}\"}}\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedSymbol{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.StringLiteral{Value: "}"}},
				}},
			}},
		},
		{
			name:   "paren_delimiter_with_parens_in_strings",
			source: "def run\n  %W(#{\"(\"}x #{\")\"}y)\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.StringLiteral{Value: "("}},
					ast.StringText{Text: "x"},
				}},
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.StringLiteral{Value: ")"}},
					ast.StringText{Text: "y"},
				}},
			}},
		},
		{
			name:   "parenless_argument_bracket_in_string",
			source: "def run\n  collect %W[#{\"]\"}foo bar]\nend",
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.InterpolatedString{Parts: []ast.StringPart{
							ast.StringExpr{Expr: &ast.StringLiteral{Value: "]"}},
							ast.StringText{Text: "foo"},
						}},
						&ast.StringLiteral{Value: "bar"},
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

// A %W/%I entry may interpolate an expression whose body contains a nested
// double-quoted string with its own #{...} interpolation; the delimiter scan
// must descend through both before accepting the closing delimiter. Verified
// against Ruby: x="z"; %W[a#{"b#{x}c"}d e] => ["abzcd", "e"].
func TestParserPercentInterpolatedArrayLiteralNestedInterpolation(t *testing.T) {
	t.Parallel()

	source := "def run\n  %W[a#{\"b#{x}c\"}d e]\nend"

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringText{Text: "a"},
				ast.StringExpr{Expr: &ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringText{Text: "b"},
					ast.StringExpr{Expr: &ast.Identifier{Name: "x"}},
					ast.StringText{Text: "c"},
				}}},
				ast.StringText{Text: "d"},
			}},
			&ast.StringLiteral{Value: "e"},
		}}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// %W/%I parse as parenless call arguments after a non-local callee, mirroring
// the lowercase forms and exercising the argument scan path.
func TestParserPercentInterpolatedArrayParenlessCallArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name: "words",
			source: `def run
  collect %W[hi #{name}]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "hi"},
						&ast.InterpolatedString{Parts: []ast.StringPart{
							ast.StringExpr{Expr: &ast.Identifier{Name: "name"}},
						}},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name: "symbols",
			source: `def run
  collect %I[hi #{name}]
end`,
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.SymbolLiteral{Name: "hi"},
						&ast.InterpolatedSymbol{Parts: []ast.StringPart{
							ast.StringExpr{Expr: &ast.Identifier{Name: "name"}},
						}},
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

// A local named W/I keeps %W/%I parsing as modulo against an index/call, the
// same disambiguation the lowercase forms use.
func TestParserLocalModuloBeforeCompactUppercaseWIIdentifiers(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  W = [3]
  total %W[0]
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
			Target: &ast.Identifier{Name: "W"},
			Value: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.IntegerLiteral{Value: 3},
			}},
		},
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "total"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "W"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
		}},
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
					Object:  &ast.Identifier{Name: "w"},
					Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
					Object:  &ast.Identifier{Name: "w"},
					Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
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
			Object:  &ast.Identifier{Name: "w"},
			Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
		},
	}}
	if diff := cmp.Diff(wantStmt, got.Statements[1], astCmpOpts); diff != "" {
		t.Fatalf("post-rescue statement mismatch (-want +got):\n%s", diff)
	}
}

// A string interpolation inherits the enclosing local scope, so a percent
// literal inside #{...} disambiguates the same way it would inline.
func TestParserStringInterpolationInheritsLocalsForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  w = [3]
  "#{total %w[0]}"
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 3 {
		t.Fatalf("function body has %d statements, want 3", len(body))
	}
	interp, ok := body[2].(*ast.ExprStmt).Expr.(*ast.InterpolatedString)
	if !ok {
		t.Fatalf("body[2].Expr = %T, want *ast.InterpolatedString", body[2].(*ast.ExprStmt).Expr)
	}
	var exprPart *ast.StringExpr
	for i := range interp.Parts {
		if part, ok := interp.Parts[i].(ast.StringExpr); ok {
			exprPart = &part
			break
		}
	}
	if exprPart == nil {
		t.Fatalf("interpolation has no embedded expression part")
	}

	want := &ast.BinaryExpr{
		Left:     &ast.Identifier{Name: "total"},
		Operator: ast.TokenPercent,
		Right: &ast.IndexExpr{
			Object:  &ast.Identifier{Name: "w"},
			Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
		},
	}
	if diff := cmp.Diff(want, exprPart.Expr, astCmpOpts); diff != "" {
		t.Fatalf("interpolation expression mismatch (-want +got):\n%s", diff)
	}
}

// A parameter default may reference an earlier parameter, so the earlier
// parameter must be a known local when the default is parsed: the percent
// literal in the default is modulo, not a parenless call.
func TestParserFunctionParamDefaultSeesEarlierParamsForPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run(total, w, x = total %w[0])
  x
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(got.Statements) != 1 {
		t.Fatalf("parseSource returned %d statements, want 1", len(got.Statements))
	}
	fn, ok := got.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("statement[0] = %T, want *ast.FunctionStmt", got.Statements[0])
	}
	var xParam *ast.Param
	for i := range fn.Params {
		if fn.Params[i].Name == "x" {
			xParam = &fn.Params[i]
		}
	}
	if xParam == nil {
		t.Fatalf("parameter x not found")
	}

	want := &ast.BinaryExpr{
		Left:     &ast.Identifier{Name: "total"},
		Operator: ast.TokenPercent,
		Right: &ast.IndexExpr{
			Object:  &ast.Identifier{Name: "w"},
			Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
		},
	}
	if diff := cmp.Diff(want, xParam.DefaultVal, astCmpOpts); diff != "" {
		t.Fatalf("parameter default mismatch (-want +got):\n%s", diff)
	}
}

// A postfix on a percent-array call argument binds to the literal, not to
// the whole call: `collect %w[a][0]` is `collect((%w[a])[0])`.
func TestParserPercentArrayArgumentBindsTrailingPostfix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantArg ast.Expression
	}{
		{
			name:   "index",
			source: "def run\n  collect %w[a][0]\nend",
			wantArg: &ast.IndexExpr{
				Object:  &ast.ArrayLiteral{Elements: []ast.Expression{&ast.StringLiteral{Value: "a"}}},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
			},
		},
		{
			name:   "member",
			source: "def run\n  collect %w[a].size\nend",
			wantArg: &ast.MemberExpr{
				Object:   &ast.ArrayLiteral{Elements: []ast.Expression{&ast.StringLiteral{Value: "a"}}},
				Property: "size",
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
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Callee: &ast.Identifier{Name: "collect"},
					Args:   []ast.Expression{tc.wantArg},
					KwArgs: []ast.KeywordArg{},
				}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
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
