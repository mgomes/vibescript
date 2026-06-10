package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

type positionedError interface {
	error
	Pos() ast.Position
	End() ast.Position
	Message() string
}

func TestParseErrorsExposeStructuredPositions(t *testing.T) {
	t.Parallel()
	_, errs := Parse("def 123()\n  1\nend\n")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for invalid function name")
	}

	first, ok := errs[0].(positionedError)
	if !ok {
		t.Fatalf("errs[0] = %T, want positioned parse error", errs[0])
	}
	if got, want := first.Pos(), (ast.Position{Line: 1, Column: 5}); got != want {
		t.Fatalf("Pos() = %v, want %v", got, want)
	}
	// The offending token is the three-character literal "123", so the
	// exclusive end lands three columns after the start.
	if got, want := first.End(), (ast.Position{Line: 1, Column: 8}); got != want {
		t.Fatalf("End() = %v, want %v", got, want)
	}
	if got := first.Message(); got != "expected function name, got integer" {
		t.Fatalf("Message() = %q, want bare message", got)
	}
	if strings.Contains(first.Message(), "parse error at") {
		t.Fatalf("Message() %q must not embed the position prefix", first.Message())
	}
	if !strings.Contains(first.Error(), "parse error at 1:5") {
		t.Fatalf("Error() = %q, want position-prefixed rendering", first.Error())
	}
}

func TestParseErrorEndIsZeroAtEndOfInput(t *testing.T) {
	t.Parallel()
	_, errs := Parse("def run()\n  x = [1,\nend\n")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for unterminated array literal")
	}

	var sawUnknownSpan bool
	for _, err := range errs {
		pe, ok := err.(positionedError)
		if !ok {
			t.Fatalf("error %T does not expose positions", err)
		}
		if pe.End() == (ast.Position{}) {
			sawUnknownSpan = true
			if pe.Pos() == (ast.Position{}) {
				t.Fatalf("error %q has neither position nor span", pe.Message())
			}
		}
	}
	if !sawUnknownSpan {
		t.Fatal("expected at least one end-of-input error with an unknown span")
	}
}

func TestTokenEndSpansSingleLineLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tok  ast.Token
		want ast.Position
	}{
		{
			name: "identifier",
			tok:  ast.Token{Literal: "total", Pos: ast.Position{Line: 4, Column: 7}},
			want: ast.Position{Line: 4, Column: 12},
		},
		{
			name: "empty_literal",
			tok:  ast.Token{Literal: "", Pos: ast.Position{Line: 2, Column: 1}},
			want: ast.Position{},
		},
		{
			name: "multiline_literal",
			tok:  ast.Token{Literal: "a\nb", Pos: ast.Position{Line: 2, Column: 1}},
			want: ast.Position{},
		},
		{
			name: "multibyte_runes_counted_once",
			tok:  ast.Token{Literal: "héllo", Pos: ast.Position{Line: 1, Column: 3}},
			want: ast.Position{Line: 1, Column: 8},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenEnd(tc.tok); got != tc.want {
				t.Fatalf("tokenEnd(%q) = %v, want %v", tc.tok.Literal, got, tc.want)
			}
		})
	}
}
