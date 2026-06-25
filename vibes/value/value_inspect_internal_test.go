package value

import (
	"errors"
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

// TestAppendQuotedStringBoundedMatchesQuoteString verifies the streaming quoter
// (which never builds the full escaped form) produces byte-for-byte the same
// output as the materializing quoteString when run unbounded. The materializing
// form stays the canonical escape oracle, so any divergence between the streamed
// and materialized escaping surfaces here.
func TestAppendQuotedStringBoundedMatchesQuoteString(t *testing.T) {
	t.Parallel()
	for _, s := range inspectByteLenInputs {
		var buf strings.Builder
		if err := appendQuotedStringBounded(&buf, s, 0); err != nil {
			t.Fatalf("appendQuotedStringBounded(%q, 0) error = %v, want nil", s, err)
		}
		if got, want := buf.String(), quoteString(s); got != want {
			t.Errorf("appendQuotedStringBounded(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestAppendInspectSymbolBoundedMatchesInspectSymbol(t *testing.T) {
	t.Parallel()
	names := append([]string{"ok", "_id", "a b", "1up", ""}, inspectByteLenInputs...)
	for _, name := range names {
		var buf strings.Builder
		if err := appendInspectSymbolBounded(&buf, name, 0); err != nil {
			t.Fatalf("appendInspectSymbolBounded(%q, 0) error = %v, want nil", name, err)
		}
		if got, want := buf.String(), inspectSymbol(name); got != want {
			t.Errorf("appendInspectSymbolBounded(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAppendInspectHashKeyBoundedMatchesInspectHashKey(t *testing.T) {
	t.Parallel()
	keys := append([]string{"name", "a b", "_id"}, inspectByteLenInputs...)
	for _, key := range keys {
		var buf strings.Builder
		if err := appendInspectHashKeyBounded(&buf, key, 0); err != nil {
			t.Fatalf("appendInspectHashKeyBounded(%q, 0) error = %v, want nil", key, err)
		}
		if got, want := buf.String(), inspectHashKey(key); got != want {
			t.Errorf("appendInspectHashKeyBounded(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestAppendQuotedStringBoundedStopsAtLimit pins the streaming guard that the
// Codex finding asked for: a hostile string whose quoted form is far larger than
// the budget must trip the limit with the buffer never exceeding it, and the
// partial output must be a byte-exact prefix of the full quoted form. Because the
// quoter escapes one fragment at a time and checks the budget after each, the
// peak buffer stays bounded even when the full quoted form would be roughly twice
// the (already oversized) input.
func TestAppendQuotedStringBoundedStopsAtLimit(t *testing.T) {
	t.Parallel()
	// Every byte escapes to two bytes, so the full quoted form is ~2x the input.
	s := strings.Repeat("\"", 1<<16)
	full := quoteString(s)
	for _, limit := range []int{1, 2, 3, 7, 64, 1024} {
		var buf strings.Builder
		err := appendQuotedStringBounded(&buf, s, limit)
		if !errors.Is(err, ErrStringRenderTruncated) {
			t.Fatalf("appendQuotedStringBounded(limit=%d) error = %v, want ErrStringRenderTruncated", limit, err)
		}
		got := buf.String()
		if len(got) > limit {
			t.Fatalf("appendQuotedStringBounded(limit=%d) buffer = %d bytes, want <= %d", limit, len(got), limit)
		}
		if !strings.HasPrefix(full, got) {
			t.Fatalf("appendQuotedStringBounded(limit=%d) = %q, not a prefix of the full quoted form", limit, got)
		}
	}
}
