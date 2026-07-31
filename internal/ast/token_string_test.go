package ast

import (
	"strings"
	"testing"
)

// Every token type must have a spelling in the string table. The table is
// indexed by the constants, so a constant added without an entry silently
// renders as its fallback form; this drifts only when someone appends to the
// const block, which is exactly when it should fail loudly.
func TestEveryTokenTypeHasASpelling(t *testing.T) {
	if len(tokenTypeStrings) != int(tokenTypeCount) {
		t.Errorf("tokenTypeStrings has %d entries for %d token types; a constant "+
			"appended after the table's last entry would render as its fallback form",
			len(tokenTypeStrings), int(tokenTypeCount))
	}
	for tt := TokenNone + 1; tt < tokenTypeCount; tt++ {
		if int(tt) >= len(tokenTypeStrings) || tokenTypeStrings[tt] == "" {
			t.Errorf("TokenType %d has no entry in tokenTypeStrings", int(tt))
		}
	}
	if got := TokenNone.String(); got != "" {
		t.Errorf("TokenNone renders as %q, want the empty string the zero token type has always rendered as", got)
	}
	// The fallback must identify an out-of-range value rather than panic.
	if got := TokenType(255).String(); !strings.HasPrefix(got, "token(") {
		t.Errorf("out-of-range TokenType renders as %q", got)
	}
}
