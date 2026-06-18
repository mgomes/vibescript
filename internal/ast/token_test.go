package ast

import (
	"slices"
	"testing"
)

func TestKeywords(t *testing.T) {
	t.Parallel()
	want := []string{
		"and",
		"begin",
		"break",
		"case",
		"class",
		"def",
		"do",
		"else",
		"elsif",
		"end",
		"ensure",
		"enum",
		"export",
		"false",
		"for",
		"getter",
		"if",
		"in",
		"next",
		"nil",
		"not",
		"or",
		"private",
		"property",
		"raise",
		"rescue",
		"return",
		"self",
		"setter",
		"true",
		"unless",
		"until",
		"when",
		"while",
		"yield",
	}

	got := Keywords()
	if !slices.Equal(got, want) {
		t.Fatalf("Keywords() = %#v, want %#v", got, want)
	}
	for _, keyword := range got {
		if LookupIdent(keyword) == TokenIdent {
			t.Fatalf("LookupIdent(%q) = TokenIdent, want reserved token", keyword)
		}
	}
	for _, ident := range []string{"require"} {
		if got := LookupIdent(ident); got != TokenIdent {
			t.Fatalf("LookupIdent(%q) = %s, want TokenIdent", ident, got)
		}
	}
}
