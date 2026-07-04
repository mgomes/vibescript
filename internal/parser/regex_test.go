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
	}
	for _, source := range divisions {
		if _, errs := parseSource(t, source); len(errs) > 0 {
			t.Fatalf("parseSource(%q) errors = %v, want division to parse", source, errs)
		}
	}

	regexes := []string{
		"def run\n  [/a/, /b/]\nend",
		"def run\n  return /a/\nend",
		"def run\n  f(/a/)\nend",
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
