package value

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// StringLen and StringRuneLen exist so a caller can size a regex without
// rendering it, and they mirror escapeRegexLiteralSource case for case -- the
// kind of duplication that drifts. This holds all three together.
//
// The source set deliberately includes invalid UTF-8. A regex source may hold
// any bytes, and an earlier version of StringRuneLen skipped continuation bytes
// on the assumption they belonged to a valid multibyte rune: a stray one is a
// single RuneError that String preserves, so a source of them was undercounted
// by its whole length. Verifying against ASCII and valid multibyte alone missed
// it.
func TestRegexLenMatchesRendering(t *testing.T) {
	t.Parallel()

	sources := []string{
		"", "abc", "a/b", `a\/b`, `\\`, `\\/`, "a\nb", "a\tb", "a\ab", "a\vb", "a\fb", "a\rb",
		"héllo", "日本語", "\x01\x1f\x7f", "\x00",
	}
	// Every single byte, in isolation and embedded: lone continuation bytes and
	// invalid leads included.
	for b := 0; b < 0x100; b++ {
		sources = append(sources, string([]byte{byte(b)}), "a"+string([]byte{byte(b)})+"z")
	}
	// Runs of stray continuation bytes, and truncated multibyte sequences.
	sources = append(sources,
		strings.Repeat("\x80", 8), strings.Repeat("\xbf", 5),
		"\xe6\x97", "\xf0\x9f\x98", "a\x80\x80b", "\xff\xfe", "\xed\xa0\x80",
	)

	for _, flags := range []string{"", "i", "im", "x"} {
		for _, source := range sources {
			re := Regex{Source: source, Flags: flags}
			rendered := re.String()
			if got, want := re.StringLen(), len(rendered); got != want {
				t.Errorf("StringLen for source %q flags %q = %d, want %d", source, flags, got, want)
			}
			if got, want := re.StringRuneLen(), utf8.RuneCountInString(rendered); got != want {
				t.Errorf("StringRuneLen for source %q flags %q = %d, want %d", source, flags, got, want)
			}
		}
	}
}
