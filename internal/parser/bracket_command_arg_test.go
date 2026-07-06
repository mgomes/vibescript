package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestParserBracketDisambiguation pins Ruby's indexing-vs-argument bracket
// rule: a bracket flush against its callee indexes, a known local callee
// indexes in every spacing (including through an index assignment), and a
// bracket detached from a non-local callee in command position opens an
// array-literal argument, so `puts [3, 1, 2]` == `puts([3, 1, 2])`.
func TestParserBracketDisambiguation(t *testing.T) {
	t.Parallel()

	indexings := []string{
		"def run\n  a = [10, 20]\n  a[0]\nend",
		// A bracket after a known local stays indexing in every spacing:
		// the parser's local table keeps the array-argument reading for
		// non-local callees only, matching Ruby's local-variable rule.
		"def run\n  a = [10, 20]\n  a [0]\nend",
		"def run\n  a = [10, 20]\n  a [0, 1]\nend",
		// Locals index for assignment too, even with a space.
		"def run\n  a = [10, 20]\n  a [0] = 99\nend",
		// Parameters and assigned uppercase constants are locals too.
		"def run(xs)\n  xs [0]\nend",
		"def run\n  Total = [4]\n  Total [0]\nend",
		// The implicit `it` block parameter is pre-declared like the
		// numbered candidates, so a bracket after either indexes.
		"def run\n  [[5]].map { it [0] }\nend",
		"def run\n  [[5]].map { _1 [0] }\nend",
		// Class body assignments are the class's constants, which method
		// bodies resolve at runtime, so they index across the def boundary.
		"class C\n  X = [7]\n  def m\n    X [0]\n  end\nend",
		// A non-local callee keeps indexing when the bracket is flush:
		// only the detached bracket opens an array-literal argument.
		"def run\n  f[0]\nend",
	}
	for _, source := range indexings {
		program, errs := parseSource(t, source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want indexing to parse", source, errs)
		}
		count := 0
		walkASTNodes(program, func(node any) {
			if _, ok := node.(*ast.IndexExpr); ok {
				count++
			}
		})
		if count == 0 {
			t.Fatalf("parseSource(%q) produced no index expression", source)
		}
	}

	arguments := []string{
		"def run\n  puts [3, 1, 2]\nend",
		// Unlike the sigil arms, only the callee-side spacing matters: the
		// elements may sit apart from the bracket or on later lines.
		"def run\n  puts [ 1 ]\nend",
		"def run\n  puts [\n    1,\n    2,\n  ]\nend",
		// The argument accepts postfixes and trailing blocks.
		"def run\n  puts [3, 1, 2].sort\nend",
		"def run\n  f [5].map { it }\nend",
		"def run\n  f [[1], [2]]\nend",
		// Continuation arguments after a comma are unambiguous array
		// literals regardless of which argument opened the call.
		"def run\n  f [1], [2]\nend",
		"def run\n  f 1, [2]\nend",
		"def run\n  (f [1])\nend",
		// A member callee has no local reading, so a detached bracket is
		// always an argument, exactly as Ruby reads `obj.first [0]`.
		"def run(xs)\n  xs.first [0]\nend",
		// Locals stop at the def boundary: a top-level local is invisible
		// inside a method body, so the argument reading applies there.
		"total = [4]\nclass C\n  def m\n    total [0]\n  end\nend",
	}
	for _, source := range arguments {
		program, errs := parseSource(t, source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want array argument to parse", source, errs)
		}
		walkASTNodes(program, func(node any) {
			if _, ok := node.(*ast.IndexExpr); ok {
				t.Fatalf("parseSource(%q) produced an index expression, want array argument", source)
			}
		})
		if callWithArrayArgument(program) == nil {
			t.Fatalf("parseSource(%q) produced no call with an array-literal argument", source)
		}
	}
}

// callWithArrayArgument returns a CallExpr whose argument list contains
// (possibly nested inside a postfix such as `.sort`) an array literal.
func callWithArrayArgument(program *ast.Program) *ast.CallExpr {
	var found *ast.CallExpr
	walkASTNodes(program, func(node any) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return
		}
		for _, arg := range call.Args {
			walkASTNodes(arg, func(inner any) {
				if _, ok := inner.(*ast.ArrayLiteral); ok {
					found = call
				}
			})
		}
	})
	return found
}

// TestParserBracketCommandArgumentShapes pins the argument lists the bracket
// rule produces for the multi-argument and chained forms.
func TestParserBracketCommandArgumentShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantArgs int
	}{
		{
			name:     "single_array",
			source:   "def run\n  puts [3, 1, 2]\nend",
			wantArgs: 1,
		},
		{
			name:     "chained_postfix",
			source:   "def run\n  puts [3, 1, 2].sort\nend",
			wantArgs: 1,
		},
		{
			name:     "two_array_arguments",
			source:   "def run\n  f [1], [2]\nend",
			wantArgs: 2,
		},
		{
			name:     "array_as_later_argument",
			source:   "def run\n  f 1, [2]\nend",
			wantArgs: 2,
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
			call := callFromLastStatement(t, program)
			if len(call.Args) != tc.wantArgs {
				t.Fatalf("args = %d, want %d", len(call.Args), tc.wantArgs)
			}
		})
	}
}

// TestParserBracketArgumentBoundaries pins the shapes the bracket rule must
// not disturb: a bare array-literal statement, a bracket opening the next
// line, and an index assignment through a non-local callee, which loses its
// indexing reading and now fails to parse (Ruby rejects `f [0] = 5` too).
func TestParserBracketArgumentBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("statement_start_array_literal", func(t *testing.T) {
		t.Parallel()
		source := "def run\n  [1, 2]\n  puts \"ok\"\nend"
		program, errs := parseSource(t, source)
		if len(errs) != 0 {
			t.Fatalf("parseSource(%q) errors: %v", source, errs)
		}
		body := parsedFunctionBody(t, program)
		if len(body) != 2 {
			t.Fatalf("body statements = %d, want 2", len(body))
		}
		stmt, ok := body[0].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("first statement is %T, want *ast.ExprStmt", body[0])
		}
		if _, ok := stmt.Expr.(*ast.ArrayLiteral); !ok {
			t.Fatalf("first expression is %T, want *ast.ArrayLiteral", stmt.Expr)
		}
	})

	t.Run("bracket_on_next_line_stays_statement", func(t *testing.T) {
		t.Parallel()
		source := "def run\n  f\n  [1]\nend"
		program, errs := parseSource(t, source)
		if len(errs) != 0 {
			t.Fatalf("parseSource(%q) errors: %v", source, errs)
		}
		body := parsedFunctionBody(t, program)
		if len(body) != 2 {
			t.Fatalf("body statements = %d, want 2", len(body))
		}
	})

	t.Run("non_local_index_assignment_rejected", func(t *testing.T) {
		t.Parallel()
		source := "def run\n  f [0] = 5\nend"
		_, errs := parseSource(t, source)
		if len(errs) == 0 {
			t.Fatalf("parseSource(%q) parsed, want an error for assigning to a command argument", source)
		}
	})
}
