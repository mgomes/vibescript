package parser

import (
	"strings"
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

// A %W/%I interpolation expression may itself contain a nested percent-array
// literal whose delimiter or body holds the outer literal's closing delimiter,
// such as %W[#{%w[}]}]. The interpolation scan drives the lexer, so the nested
// literal is consumed as a single token and its "}" does not close the
// interpolation early. A bare "%" that the lexer reads as modulo is left alone,
// so %W[#{a % w}] still parses as an interpolated modulo expression. Both the
// direct-literal path and the double-quoted-string path are exercised. Verified
// against Ruby:
//
//	%W[#{%w[}]}]     => ["[\"}\"]"]
//	%I[#{%i[}]}]     => [:"[:\"}\"]"]
//	"x#{%w[}]}"      => "x[\"}\"]"
//	a=10; w=3; %W[#{a % w}] => ["1"]
func TestParserPercentInterpolatedArrayLiteralNestedPercentLiteral(t *testing.T) {
	t.Parallel()

	nestedWords := &ast.ArrayLiteral{Elements: []ast.Expression{
		&ast.StringLiteral{Value: "}"},
	}}
	nestedSymbols := &ast.ArrayLiteral{Elements: []ast.Expression{
		&ast.SymbolLiteral{Name: "}"},
	}}

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name:   "words_with_nested_words_holding_close_delimiter",
			source: "def run\n  %W[#{%w[}]}]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: nestedWords},
				}},
			}},
		},
		{
			name:   "symbols_with_nested_symbols_holding_close_delimiter",
			source: "def run\n  %I[#{%i[}]}]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedSymbol{Parts: []ast.StringPart{
					ast.StringExpr{Expr: nestedSymbols},
				}},
			}},
		},
		{
			name:   "words_with_leading_and_trailing_words",
			source: "def run\n  %W[head #{%w[a b]} tail]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "head"},
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "a"},
						&ast.StringLiteral{Value: "b"},
					}}},
				}},
				&ast.StringLiteral{Value: "tail"},
			}},
		},
		{
			name:   "double_quoted_string_with_nested_words",
			source: "def run\n  \"x#{%w[}]}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringText{Text: "x"},
				ast.StringExpr{Expr: nestedWords},
			}},
		},
		{
			name:   "parenless_argument_with_nested_words",
			source: "def run\n  collect %W[head #{%w[a b]} tail]\nend",
			wantExpr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args: []ast.Expression{
					&ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.StringLiteral{Value: "head"},
						&ast.InterpolatedString{Parts: []ast.StringPart{
							ast.StringExpr{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
								&ast.StringLiteral{Value: "a"},
								&ast.StringLiteral{Value: "b"},
							}}},
						}},
						&ast.StringLiteral{Value: "tail"},
					}},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name:   "interpolation_with_parenless_percent_array_argument",
			source: "def run\n  %W[#{collect %w[}]}]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.CallExpr{
						Callee: &ast.Identifier{Name: "collect"},
						Args: []ast.Expression{
							nestedWords,
						},
						KwArgs: []ast.KeywordArg{},
					}},
				}},
			}},
		},
		{
			name:   "bare_percent_stays_modulo",
			source: "def run\n  a = 10\n  w = 3\n  %W[#{a % w}]\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.InterpolatedString{Parts: []ast.StringPart{
					ast.StringExpr{Expr: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "a"},
						Operator: ast.TokenPercent,
						Right:    &ast.Identifier{Name: "w"},
					}},
				}},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			body := parsedFunctionBody(t, got)
			lastStmt := body[len(body)-1]
			wantStmt := &ast.ExprStmt{Expr: tc.wantExpr}
			if diff := cmp.Diff(wantStmt, lastStmt, astCmpOpts); diff != "" {
				t.Fatalf("final statement mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A parenless percent-array call argument inside an interpolation may carry the
// interpolation's own closing delimiter inside any delimiter pairing, not just
// the bracket form. The interpolation-end scan must consume the whole argument
// literal so that "}" is never mistaken for the interpolation close. Verified
// against Ruby (def collect(a); a; end):
//
//	"#{collect %w{}}"   => "[]"
//	"#{collect %w(})}"  => "[\"}\"]"
//	"#{collect %i[}]}"  => "[:\"}\"]"
//	"#{collect %W[}]}"  => "[\"}\"]"
func TestParserParenlessPercentArrayArgumentInInterpolationDelimiters(t *testing.T) {
	t.Parallel()

	wordsClose := func() ast.Expression {
		return &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.StringLiteral{Value: "}"},
		}}
	}
	symbolsClose := func() ast.Expression {
		return &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.SymbolLiteral{Name: "}"},
		}}
	}
	call := func(arg ast.Expression) ast.Expression {
		return &ast.CallExpr{
			Callee: &ast.Identifier{Name: "collect"},
			Args:   []ast.Expression{arg},
			KwArgs: []ast.KeywordArg{},
		}
	}

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name:   "brace_argument_holds_close_delimiter",
			source: "def run\n  \"#{collect %w{}}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(&ast.ArrayLiteral{Elements: []ast.Expression{}})},
			}},
		},
		{
			name:   "paren_argument_holds_close_delimiter",
			source: "def run\n  \"#{collect %w(})}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(wordsClose())},
			}},
		},
		{
			name:   "symbol_argument_holds_close_delimiter",
			source: "def run\n  \"#{collect %i[}]}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(&ast.ArrayLiteral{Elements: []ast.Expression{
					&ast.SymbolLiteral{Name: "}"},
				}})},
			}},
		},
		{
			name:   "interpolating_argument_holds_close_delimiter",
			source: "def run\n  \"#{collect %W[}]}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(wordsClose())},
			}},
		},
		{
			name:   "interpolating_argument_bang_delimiter_holds_close_delimiter",
			source: "def run\n  \"#{collect %W!}!}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(wordsClose())},
			}},
		},
		{
			name:   "interpolating_symbol_argument_bang_delimiter_holds_close_delimiter",
			source: "def run\n  \"#{collect %I!}!}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(symbolsClose())},
			}},
		},
		{
			name:   "interpolating_argument_hash_delimiter_holds_close_delimiter",
			source: "def run\n  \"#{collect %W#}#}\"\nend",
			wantExpr: &ast.InterpolatedString{Parts: []ast.StringPart{
				ast.StringExpr{Expr: call(wordsClose())},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			body := parsedFunctionBody(t, got)
			lastStmt := body[len(body)-1]
			wantStmt := &ast.ExprStmt{Expr: tc.wantExpr}
			if diff := cmp.Diff(wantStmt, lastStmt, astCmpOpts); diff != "" {
				t.Fatalf("final statement mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A local-variable callee keeps the modulo reading even though the
// interpolation-end scan optimistically skips the percent-array shape to locate
// the close brace: the scope-aware sub-parser that reparses the located body
// restores the correct interpretation. Both readings agree on the close brace
// here because the modulo expression contains no "}". This guards the
// correctness premise of the optimistic skip. Verified against Ruby
// (a = 10; w = [3]):
//
//	"#{a %w[0]}"  => "1"   (a % w[0])
func TestParserLocalModuloSurvivesInterpolationScan(t *testing.T) {
	t.Parallel()

	source := "def run\n  a = 10\n  w = [3]\n  \"#{a %w[0]}\"\nend"

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantExpr := &ast.InterpolatedString{Parts: []ast.StringPart{
		ast.StringExpr{Expr: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "a"},
			Operator: ast.TokenPercent,
			Right: &ast.IndexExpr{
				Object:  &ast.Identifier{Name: "w"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
			},
		}},
	}}

	body := parsedFunctionBody(t, got)
	lastStmt := body[len(body)-1]
	wantStmt := &ast.ExprStmt{Expr: wantExpr}
	if diff := cmp.Diff(wantStmt, lastStmt, astCmpOpts); diff != "" {
		t.Fatalf("final statement mismatch (-want +got):\n%s", diff)
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

// When '#' is the delimiter, a percent array passed as a parenless call
// argument must close at the first unescaped '#' just like the direct-literal
// form. Verified against Ruby: collect %W#hi there# => ["hi", "there"],
// collect %i#a b# => [:a, :b], collect %W#a\#b c# => ["a#b", "c"].
func TestParserPercentArrayParenlessArgumentHashDelimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name:   "uppercase_words_plain",
			source: "def run\n  collect %W#hi there#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "hi"},
				&ast.StringLiteral{Value: "there"},
			}},
		},
		{
			name:   "lowercase_symbols_plain",
			source: "def run\n  collect %i#a b#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.SymbolLiteral{Name: "a"},
				&ast.SymbolLiteral{Name: "b"},
			}},
		},
		{
			name:   "uppercase_words_escaped_hash",
			source: "def run\n  collect %W#a\\#b c#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a#b"},
				&ast.StringLiteral{Value: "c"},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{&ast.ExprStmt{Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "collect"},
				Args:   []ast.Expression{tc.wantExpr},
				KwArgs: []ast.KeywordArg{},
			}}}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// scanPercentArrayLiteralAt drives the parenless-argument scan path. When '#' is
// the delimiter the first unescaped '#' must close the literal, so the "#{"
// interpolation special-casing must not steal it. Without this guard the scanner
// consumed the "#{...}" span and ran to EOF, yielding a spurious interpolated
// entry (or failing to recognize the literal at all). The scanner is exercised
// directly because, after a '#'-closed literal, a trailing '{' would otherwise
// be parsed as a block attached to the array, obscuring the entries under test.
// Verified against Ruby, where %W#a#{b}# closes at the second '#' (=> ["a"]) and
// the trailing "#{...}" begins a comment.
func TestScanPercentArrayLiteralHashDelimiterClosesBeforeInterpolation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantKind    rune
		wantEntries []string
	}{
		{
			name:        "uppercase_words_hash_brace_does_not_interpolate",
			input:       "%W#a#{b}#",
			wantKind:    'W',
			wantEntries: []string{"a"},
		},
		{
			name:        "uppercase_symbols_hash_brace_does_not_interpolate",
			input:       "%I#a#{b}#",
			wantKind:    'I',
			wantEntries: []string{"a"},
		},
		{
			name:        "uppercase_words_trailing_words_after_interpolation_marker",
			input:       "%W#a #{b} c#",
			wantKind:    'W',
			wantEntries: []string{"a"},
		},
		{
			// Entries are returned with escapes intact; the parser resolves "\#"
			// to a literal '#' later. The escaped '#' must not close the literal,
			// but the following unescaped '#' (before "#{c}") must, so the "#{c}"
			// span is never mistaken for interpolation.
			name:        "escaped_hash_is_literal_then_first_hash_closes",
			input:       "%W#a\\#b#{c}#",
			wantKind:    'W',
			wantEntries: []string{"a\\#b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kind, entries, endOffset, ok := scanPercentArrayLiteralAt(tc.input, 0)
			if !ok {
				t.Fatalf("scanPercentArrayLiteralAt(%q) ok = false, want true", tc.input)
			}
			if kind != tc.wantKind {
				t.Fatalf("scanPercentArrayLiteralAt(%q) kind = %q, want %q", tc.input, kind, tc.wantKind)
			}
			if diff := cmp.Diff(tc.wantEntries, entries); diff != "" {
				t.Fatalf("scanPercentArrayLiteralAt(%q) entries mismatch (-want +got):\n%s", tc.input, diff)
			}
			// The literal must close at the first unescaped '#', leaving the
			// remaining "#{...}#" (or "{...}#") suffix unconsumed.
			if got := tc.input[endOffset:]; len(got) == 0 {
				t.Fatalf("scanPercentArrayLiteralAt(%q) consumed to EOF; want it to stop at the closing '#'", tc.input)
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
				Object:  &ast.Identifier{Name: "W"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
			},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// A '#' chosen as the delimiter still closes the literal for every percent
// form. The interpolation special-casing of "#{" must not steal the closing
// delimiter, which would otherwise scan the literal to EOF. Verified against
// Ruby: %w#foo bar# and %W#foo bar# => ["foo", "bar"], %i#foo bar# and
// %I#foo bar# => [:foo, :bar].
func TestParserPercentArrayLiteralHashDelimiter(t *testing.T) {
	t.Parallel()

	words := &ast.ArrayLiteral{Elements: []ast.Expression{
		&ast.StringLiteral{Value: "foo"},
		&ast.StringLiteral{Value: "bar"},
	}}
	symbols := &ast.ArrayLiteral{Elements: []ast.Expression{
		&ast.SymbolLiteral{Name: "foo"},
		&ast.SymbolLiteral{Name: "bar"},
	}}

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{name: "lowercase_words", source: "def run\n  %w#foo bar#\nend", wantExpr: words},
		{name: "uppercase_words", source: "def run\n  %W#foo bar#\nend", wantExpr: words},
		{name: "lowercase_symbols", source: "def run\n  %i#foo bar#\nend", wantExpr: symbols},
		{name: "uppercase_symbols", source: "def run\n  %I#foo bar#\nend", wantExpr: symbols},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{&ast.ExprStmt{Expr: tc.wantExpr}}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// When '#' is the delimiter, "#{" does not interpolate: the first unescaped '#'
// closes the literal. An escaped "\#" is a literal '#' that does not close.
// Verified against Ruby: %W#a\#b# => ["a#b"], %W#a#b# => ["a"].
func TestParserPercentArrayLiteralHashDelimiterEscapeAndEarlyClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantExpr ast.Expression
	}{
		{
			name:   "escaped_hash_is_literal",
			source: "def run\n  %W#a\\#b#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a#b"},
			}},
		},
		{
			name:   "lowercase_escaped_hash_is_literal",
			source: "def run\n  %w#a\\#b#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a#b"},
			}},
		},
		{
			name:   "first_hash_closes",
			source: "def run\n  %W#a#\nend",
			wantExpr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.StringLiteral{Value: "a"},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{&ast.ExprStmt{Expr: tc.wantExpr}}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A literal '#' inside a non-'#' delimiter is kept verbatim unless it begins a
// real "#{" interpolation. Verified against Ruby: %W[a#b] => ["a#b"].
func TestParserPercentInterpolatedArrayLiteralBareHash(t *testing.T) {
	t.Parallel()

	source := "def run\n  %W[a#b c]\nend"

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.StringLiteral{Value: "a#b"},
			&ast.StringLiteral{Value: "c"},
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

func TestParserInterpolatedPercentArrayLiteralReportsLexerDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "words unterminated literal",
			source: `def run
  %W[alpha
end`,
			want: "unterminated percent array literal",
		},
		{
			name: "symbols unterminated literal",
			source: `def run
  %I[alpha
end`,
			want: "unterminated percent array literal",
		},
		{
			name: "words unterminated interpolation",
			source: `def run
  %W[#{name]
end`,
			want: "unterminated string interpolation in percent array literal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, tt.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want diagnostic containing %q", tt.source, tt.want)
			}
			messages := make([]string, len(errs))
			for i, err := range errs {
				messages[i] = err.Error()
			}
			got := strings.Join(messages, "\n")
			if !strings.Contains(got, tt.want) {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", tt.source, got, tt.want)
			}
			if strings.Contains(got, "unexpected token invalid token") {
				t.Fatalf("parseSource(%q) errors = %s, want lexer diagnostic instead of generic invalid token", tt.source, got)
			}
		})
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

func TestParserStringInterpolationKeepsIndexedLocalPercentModulo(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 10
  w = [3]
  "#{total %w["]"]}"
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
	exprPart, ok := interp.Parts[0].(ast.StringExpr)
	if !ok {
		t.Fatalf("parts[0] = %T, want ast.StringExpr", interp.Parts[0])
	}

	want := &ast.BinaryExpr{
		Left:     &ast.Identifier{Name: "total"},
		Operator: ast.TokenPercent,
		Right: &ast.IndexExpr{
			Object:  &ast.Identifier{Name: "w"},
			Indices: []ast.Expression{&ast.StringLiteral{Value: "]"}},
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
