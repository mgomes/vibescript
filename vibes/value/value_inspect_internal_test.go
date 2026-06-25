package value

import (
	"strings"
	"testing"
)

// inspectByteLenInputs exercises the escape and quoting rules the byte-length
// helpers share with their materializing counterparts, including the adversarial
// case of a string made entirely of escapable bytes (where the quoted form is
// roughly twice the input). The helpers must report the exact byte length of the
// rendered form without ever building it, so the projection guard can reject an
// oversized inspect result before allocating it.
var inspectByteLenInputs = []string{
	"",
	"hello",
	"a\nb",
	"a\tb",
	`say "hi"`,
	`a\b`,
	"x#{y}",
	"a # b",
	"a\rb",
	"#",
	"#{",
	"\\\"\n\t",
	strings.Repeat(`\`, 1024),
	strings.Repeat("#{", 512),
	strings.Repeat("ok", 4096),
}

func TestQuotedStringByteLenMatchesQuoteString(t *testing.T) {
	t.Parallel()
	for _, s := range inspectByteLenInputs {
		if got, want := quotedStringByteLen(s), len(quoteString(s)); got != want {
			t.Errorf("quotedStringByteLen(%q) = %d, want len(quoteString) = %d", s, got, want)
		}
	}
}

func TestInspectSymbolByteLenMatchesInspectSymbol(t *testing.T) {
	t.Parallel()
	names := append([]string{"ok", "_id", "a b", "1up"}, inspectByteLenInputs...)
	for _, name := range names {
		if got, want := inspectSymbolByteLen(name), len(inspectSymbol(name)); got != want {
			t.Errorf("inspectSymbolByteLen(%q) = %d, want len(inspectSymbol) = %d", name, got, want)
		}
	}
}

func TestInspectHashKeyByteLenMatchesInspectHashKey(t *testing.T) {
	t.Parallel()
	keys := append([]string{"name", "a b", "_id"}, inspectByteLenInputs...)
	for _, key := range keys {
		if got, want := inspectHashKeyByteLen(key), len(inspectHashKey(key)); got != want {
			t.Errorf("inspectHashKeyByteLen(%q) = %d, want len(inspectHashKey) = %d", key, got, want)
		}
	}
}
