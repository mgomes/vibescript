package parser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

// walkASTNodes visits every struct pointer reachable from root through
// exported fields, slices, and interfaces, calling visit for each. It keeps
// these tests independent of AST shape details.
func walkASTNodes(root any, visit func(any)) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Elem().Kind() == reflect.Struct {
				ptr := v.Pointer()
				if seen[ptr] {
					return
				}
				seen[ptr] = true
				visit(v.Interface())
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, key := range v.MapKeys() {
				walk(v.MapIndex(key))
			}
		}
	}
	walk(reflect.ValueOf(root))
}

func TestParserRegexLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		pattern string
		flags   string
	}{
		{
			name:    "statement position",
			source:  "def run\n  /ID-[0-9]+/\nend",
			pattern: "ID-[0-9]+",
		},
		{
			name:    "call argument",
			source:  "def run\n  check(/ID-[0-9]+/, text)\nend",
			pattern: "ID-[0-9]+",
		},
		{
			name:    "escaped slash",
			source:  "def run\n  check(/a\\/b/, text)\nend",
			pattern: `a\/b`,
		},
		{
			name:    "slash in character class",
			source:  "def run\n  check(/[a/b]/, text)\nend",
			pattern: "[a/b]",
		},
		{
			name:    "leading bracket is literal class member",
			source:  "def run\n  check(/[]/]/, text)\nend",
			pattern: "[]/]",
		},
		{
			name:    "leading bracket in negated class",
			source:  "def run\n  check(/[^]/]/, text)\nend",
			pattern: "[^]/]",
		},
		{
			name:    "posix class",
			source:  "def run\n  check(/[[:alpha:]]/, text)\nend",
			pattern: "[[:alpha:]]",
		},
		{
			name:    "posix class with slash member",
			source:  "def run\n  check(/[[:alpha:]/]/, text)\nend",
			pattern: "[[:alpha:]/]",
		},
		{
			name:    "negated posix class",
			source:  "def run\n  check(/[[:^digit:]]/, text)\nend",
			pattern: "[[:^digit:]]",
		},
		{
			name:    "literal bracket-colon is not a posix class",
			source:  "def run\n  check(/[[:/]/, text)\nend",
			pattern: "[[:/]",
		},
		{
			name:    "case-insensitive flag",
			source:  "def run\n  check(/id/i, text)\nend",
			pattern: "id",
			flags:   "i",
		},
		{
			name:    "flags normalize to canonical order",
			source:  "def run\n  check(/id/mi, text)\nend",
			pattern: "id",
			flags:   "im",
		},
		{
			name:    "after assignment",
			source:  "def run\n  re = /a+/\nend",
			pattern: "a+",
		},
		{
			name:    "match operator operand",
			source:  "def run\n  text =~ /a+/\nend",
			pattern: "a+",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			var found *ast.RegexLiteral
			walkASTNodes(program, func(node any) {
				if re, ok := node.(*ast.RegexLiteral); ok && found == nil {
					found = re
				}
			})
			if found == nil {
				t.Fatalf("parseSource(%q) produced no regex literal", tc.source)
			}
			if found.Pattern != tc.pattern || found.Flags != tc.flags {
				t.Fatalf("regex literal = /%s/%s, want /%s/%s", found.Pattern, found.Flags, tc.pattern, tc.flags)
			}
		})
	}
}

func TestParserRegexLiteralErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "unterminated literal",
			source: "def run\n  x = /abc\nend",
			want:   "unterminated regex literal",
		},
		{
			name:   "unsupported flag",
			source: "def run\n  /a/x\nend",
			want:   `unsupported regex flag "x"; supported flags are i and m`,
		},
		{
			name:   "repeated flag",
			source: "def run\n  /a/ii\nend",
			want:   `repeated regex flag "i"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = none, want %q", tc.source, tc.want)
			}
			if got := errs[0].Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestParserMatchOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  a = text =~ /a+/
  b = text !~ /b+/
  c = text =~ /a/ == 1
end`

	program, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	operators := []ast.TokenType{}
	walkASTNodes(program, func(node any) {
		if bin, ok := node.(*ast.BinaryExpr); ok {
			operators = append(operators, bin.Operator)
		}
	})
	wantMatch, wantNotMatch, wantEQ := 0, 0, 0
	for _, op := range operators {
		switch op {
		case ast.TokenMatch:
			wantMatch++
		case ast.TokenNotMatch:
			wantNotMatch++
		case ast.TokenEQ:
			wantEQ++
		}
	}
	if wantMatch != 2 || wantNotMatch != 1 || wantEQ != 1 {
		t.Fatalf("operators = %v, want two =~, one !~, one ==", operators)
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

// TestParserSlashDisambiguation pins the division-vs-regex rule: a slash after
// a value token is division, a slash in prefix position starts a regex.
func TestParserSlashDisambiguation(t *testing.T) {
	t.Parallel()

	divisions := []string{
		"def run\n  a = 4\n  a / 2\nend",
		"def run\n  (1 + 3) / 2\nend",
		"def run\n  xs = [8]\n  xs[0] / 2\nend",
		"def run\n  a = 8.0\n  a /= 2\n  a\nend",
		// A slash after a known local stays division in every spacing: the
		// parser's local table keeps the command-argument regex reading for
		// non-local callees only, so these #882 shapes must never regress.
		"def run\n  total = 4\n  total /2\nend",
		"def run\n  total = 4\n  n = 1\n  total /(n + 1)\nend",
		"def run\n  total = 4\n  n = 1\n  total /-n\nend",
		// Parameters and assigned uppercase constants are locals too.
		"def run(total)\n  total /2\nend",
		"def run\n  Total = 4\n  Total /2\nend",
		// The implicit `it` block parameter is pre-declared like the
		// numbered candidates, so a slash after either divides in brace,
		// do/end, and lambda bodies alike, and a nested block still infers
		// its own `it`.
		"def run\n  [8, 4].map { it /2 }\nend",
		"def run\n  [8, 4].map do\n    it /2\n  end\nend",
		"def run\n  f = -> { it /2 }\n  f\nend",
		"def run\n  [8, 4].map { _1 /2 }\nend",
		"def run\n  x = 2\n  [8, 4].map { it /2/ x }\nend",
		"def run\n  [[8]].map { it.map { it /2 } }\nend",
		// Class and module body assignments are the class's constants,
		// which method bodies (and blocks inside them) resolve at runtime,
		// so they divide across the def boundary — including when a second
		// slash follows on the same line.
		"class C\n  X = 4\n  def m\n    X /2\n  end\nend",
		"class C\n  X = 8\n  def m\n    X /2/ 2\n  end\nend",
		"class C\n  X = 8\n  def m\n    [1].map { X /2 }\n  end\nend",
		"class C\n  x = 4\n  def m\n    x /2\n  end\nend",
		"module A\n  Y = 6\n  def self.m\n    Y /2\n  end\nend",
		// A non-local callee keeps division when the slash is spaced on both
		// sides or flush: only the space-before/none-after shape opens a
		// command-argument regex.
		"def run\n  f / 2\nend",
		"def run\n  f/2\nend",
		// "/=" keeps compound-assignment priority even after a non-local
		// name, matching Ruby's op-assign rule.
		"def run\n  f /= 2\nend",
	}
	for _, source := range divisions {
		program, errs := parseSource(t, source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want division to parse", source, errs)
		}
		// None of these forms should lex as a regex literal.
		walkASTNodes(program, func(node any) {
			if _, ok := node.(*ast.RegexLiteral); ok {
				t.Fatalf("parseSource(%q) produced a regex literal, want division", source)
			}
		})
	}

	regexes := []string{
		"def run\n  [/a/, /b/]\nend",
		"def run\n  return /a/\nend",
		"def run\n  f(/a/)\nend",
		// Names the runtime cannot resolve as variables keep the
		// command-argument regex reading: locals stop at the def boundary
		// (top-level and enclosing-def locals are invisible inside a def),
		// and a class's constants are not visible outside its body, in a
		// lexically outer module's, or before their body-level assignment
		// parses.
		"total = 4\nclass C\n  def m\n    total /2/i\n  end\nend",
		"def outer\n  total = 4\n  def inner\n    total /2/i\n  end\nend",
		"class C\n  X = 4\nend\ndef run\n  X /2/i\nend",
		"module A\n  Y = 6\n  module B\n    def self.m\n      Y /2/i\n    end\n  end\nend",
		"class C\n  def m\n    X /2/i\n  end\n  X = 4\nend",
		// Accessor names are method calls, not locals, so command spacing
		// opens a regex after them, exactly as Ruby reads an attr_reader
		// name in the same position.
		"class Cart\n  getter total\n  def half\n    total /2/i\n  end\nend",
	}
	for _, source := range regexes {
		program, errs := parseSource(t, source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want regex to parse", source, errs)
		}
		count := 0
		walkASTNodes(program, func(node any) {
			if _, ok := node.(*ast.RegexLiteral); ok {
				count++
			}
		})
		if count == 0 {
			t.Fatalf("parseSource(%q) produced no regex literal", source)
		}
	}
}

// callWithRegexArgument returns the CallExpr whose argument list contains
// (possibly nested inside a postfix such as `.source`) the program's regex
// literal, preferring the innermost such call when commands nest.
func callWithRegexArgument(t *testing.T, program *ast.Program) *ast.CallExpr {
	t.Helper()
	var found *ast.CallExpr
	walkASTNodes(program, func(node any) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return
		}
		for _, arg := range call.Args {
			hasRegex := false
			walkASTNodes(arg, func(inner any) {
				if _, ok := inner.(*ast.RegexLiteral); ok {
					hasRegex = true
				}
			})
			if hasRegex {
				// Later matches are deeper in the walk, so the innermost
				// enclosing command call wins.
				found = call
			}
		}
	})
	if found == nil {
		t.Fatal("no call has a regex literal argument")
	}
	return found
}

// TestParserCommandArgumentRegex pins Ruby's parenless command-argument regex
// form: after a non-local callee, a slash detached from the callee but flush
// against its pattern opens a regex literal, so `match /id/` == `match(/id/)`.
func TestParserCommandArgumentRegex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		pattern  string
		flags    string
		wantArgs int
	}{
		{
			name:     "function callee",
			source:   "def run\n  match /id/\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "builtin callee",
			source:   "def run\n  puts /id/\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "member callee",
			source:   "def run(text)\n  text.scan /id/\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "flags normalize like the parenthesized form",
			source:   "def run\n  match /id/mi\nend",
			pattern:  "id",
			flags:    "im",
			wantArgs: 1,
		},
		{
			name:     "escaped slash",
			source:   "def run\n  match /a\\/b/\nend",
			pattern:  `a\/b`,
			wantArgs: 1,
		},
		{
			name:     "slash in character class",
			source:   "def run\n  match /[a/b]/\nend",
			pattern:  "[a/b]",
			wantArgs: 1,
		},
		{
			name: "hash mark in pattern is not a comment",
			// The lexer's speculative division reading swallowed "#b/" as a
			// comment while filling the lookahead; the raw-source spacing
			// check and the re-scan must not trust those tokens.
			source:   "def run\n  match /a#b/\nend",
			pattern:  "a#b",
			wantArgs: 1,
		},
		{
			name:     "regex plus further arguments",
			source:   "def run(text)\n  scan /a+/, text\nend",
			pattern:  "a+",
			wantArgs: 2,
		},
		{
			name:     "inside a condition",
			source:   "def run\n  if match /id/\n    1\n  end\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "inside a brace block",
			source:   "def run(xs)\n  xs.each { |x| match /id/ }\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "nested command argument",
			source:   "def run\n  puts match /id/\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "postfix binds to the literal",
			source:   "def run\n  match /id/.source\nend",
			pattern:  "id",
			wantArgs: 1,
		},
		{
			name:     "statement modifier stays outside the call",
			source:   "def run\n  match /id/ if true\nend",
			pattern:  "id",
			wantArgs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			var found *ast.RegexLiteral
			count := 0
			walkASTNodes(program, func(node any) {
				if re, ok := node.(*ast.RegexLiteral); ok {
					found = re
					count++
				}
			})
			if count != 1 {
				t.Fatalf("parseSource(%q) produced %d regex literals, want 1", tc.source, count)
			}
			if found.Pattern != tc.pattern || found.Flags != tc.flags {
				t.Fatalf("regex literal = /%s/%s, want /%s/%s", found.Pattern, found.Flags, tc.pattern, tc.flags)
			}
			call := callWithRegexArgument(t, program)
			if len(call.Args) != tc.wantArgs {
				t.Fatalf("call args = %d, want %d", len(call.Args), tc.wantArgs)
			}
		})
	}
}

// TestParserCommandArgumentRegexErrors pins the loud failure mode: once the
// command-argument spacing commits to a regex, a missing closing slash or a
// bad flag reports the same diagnostics as the parenthesized form.
func TestParserCommandArgumentRegexErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "unterminated command argument",
			source: "def run\n  match /abc\nend",
			want:   "unterminated regex literal",
		},
		{
			// The pre-#883 division reading of `f /2` (dividing a zero-arg
			// function's result) now fails loudly rather than silently
			// changing meaning; `f / 2`, `f/2`, and `f() / 2` keep division.
			name:   "former division of a call result",
			source: "def run\n  f /2\nend",
			want:   "unterminated regex literal",
		},
		{
			name:   "unsupported flag",
			source: "def run\n  match /id/x\nend",
			want:   `unsupported regex flag "x"; supported flags are i and m`,
		},
		{
			// A block with explicit parameters never binds the implicit
			// `it`, so the name keeps the method-call reading there and
			// command spacing opens a regex, matching Ruby. (The former
			// division parsed but always failed at runtime with
			// "undefined variable it".)
			name:   "explicit-params block keeps it a callee",
			source: "def run\n  [8].map { |x| it /2 }\nend",
			want:   "unterminated regex literal",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = none, want %q", tc.source, tc.want)
			}
			if got := errs[0].Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", tc.source, got, tc.want)
			}
		})
	}
}

// TestParserCommandArgumentRegexMeaningChange documents the Ruby-inherited
// hazard the changelog calls out: with a non-local callee and command
// spacing, a second slash later on the line closes the literal, so a former
// division chain parses cleanly with a new meaning instead of erroring.
func TestParserCommandArgumentRegexMeaningChange(t *testing.T) {
	t.Parallel()

	sources := []string{
		// Previously ((f / 2) + g) / i; now f(/2 + g/i).
		"def run\n  x = f /2 + g/i\nend",
		// Previously ((f / 2) + g) / 3; now f(/2 + g/) followed by a bare 3.
		"def run\n  x = f /2 + g/ 3\nend",
		// Previously a division continued onto the next line; now f(/2 + g/)
		// followed by a bare 1.
		"def run\n  x = f /2 + g/\n  1\nend",
	}
	for _, source := range sources {
		program, errs := parseSource(t, source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
		}
		var found *ast.RegexLiteral
		walkASTNodes(program, func(node any) {
			if re, ok := node.(*ast.RegexLiteral); ok {
				found = re
			}
		})
		if found == nil {
			t.Fatalf("parseSource(%q) produced no regex literal", source)
		}
		if found.Pattern != "2 + g" {
			t.Fatalf("parseSource(%q) regex pattern = %q, want %q", source, found.Pattern, "2 + g")
		}
	}
}

// TestParserSigilArmsShareSlashLocality pins that the splat, double-splat,
// and block-pass sigil arms share the slash arm's locality view: the
// implicit `it` block parameter and the enclosing class's constants read as
// locals, so command spacing keeps the operator meaning (multiplication,
// exponentiation, intersection) instead of opening a call argument. Before
// the shared view these shapes parsed as calls that always failed at
// runtime ("undefined variable it", "unknown member X").
func TestParserSigilArmsShareSlashLocality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source   string
		operator ast.TokenType
	}{
		{"def run\n  [8, 4].map { it *2 }\nend", ast.TokenAsterisk},
		{"def run\n  [3, 4].map { it **2 }\nend", ast.TokenPower},
		{"def run\n  b = 2\n  [8].map { it &b }\nend", ast.TokenAmpersand},
		{"class C\n  X = [4, 6]\n  def m\n    X *2\n  end\nend", ast.TokenAsterisk},
		{"class C\n  X = 4\n  def m\n    X **2\n  end\nend", ast.TokenPower},
		{"class C\n  X = 4\n  def m\n    y = 6\n    X &y\n  end\nend", ast.TokenAmpersand},
	}
	for _, tc := range tests {
		program, errs := parseSource(t, tc.source)
		if len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want operator parse", tc.source, errs)
		}
		found := false
		walkASTNodes(program, func(node any) {
			if bin, ok := node.(*ast.BinaryExpr); ok && bin.Operator == tc.operator {
				found = true
			}
			if _, ok := node.(*ast.SplatArg); ok {
				t.Fatalf("parseSource(%q) produced a splat argument, want operator %v", tc.source, tc.operator)
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if _, isIdent := call.BlockArg.(*ast.Identifier); isIdent {
					t.Fatalf("parseSource(%q) produced a block-pass argument, want operator %v", tc.source, tc.operator)
				}
			}
		})
		if !found {
			t.Fatalf("parseSource(%q) produced no %v binary expression", tc.source, tc.operator)
		}
	}
}
