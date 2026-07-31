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
	for b := range 0x100 {
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

// Every kind costs one step per node in a bounded sizing walk, plus whatever is
// proportional to the work its own rendering needs. A regex charges for its
// source, and that charge is on top of the per-node step -- not instead of one,
// and not in addition to a second.
//
// The recursive walkers take their per-node step at entry, so the regex case
// must not take another: doing so billed a nested regex one step more than a
// nested string of the same size, on a path where the whole point of the case is
// that the regex is cheaper to size than to render.
func TestRegexSizingChargesOneStepPerNode(t *testing.T) {
	counted := func(v Value, walk func(Value, func() error) (int, error)) int {
		steps := 0
		if _, err := walk(v, func() error { steps++; return nil }); err != nil {
			t.Fatalf("sizing %v: %v", v.Kind(), err)
		}
		return steps
	}
	recursive := map[string]func(Value, func() error) (int, error){
		"rune walk": func(v Value, step func() error) (int, error) {
			return v.stringRuneLenBoundedWithState(newValueStringState(), step)
		},
		"byte walk": func(v Value, step func() error) (int, error) {
			return v.stringByteLenBoundedWithState(newValueStringState(), step)
		},
	}
	public := map[string]func(Value, func() error) (int, error){
		"StringRuneLenBounded": Value.StringRuneLenBounded,
		"StringByteLenBounded": Value.StringByteLenBounded,
	}

	// A source under one step's worth of bytes charges nothing proportional, so
	// the total is the per-node step alone -- exactly what a plain string costs.
	tiny := NewRegex(Regex{Source: "a"})
	plain := NewString("a")
	for name, walk := range recursive {
		if got, want := counted(tiny, walk), counted(plain, walk); got != want {
			t.Errorf("%s: a one-byte regex source cost %d steps and a one-byte string "+
				"cost %d; sizing a regex reads its source without rendering it, so a "+
				"node of either kind is one step", name, got, want)
		}
	}
	for name, walk := range public {
		if got, want := counted(tiny, walk), counted(plain, walk); got != want {
			t.Errorf("%s: a one-byte regex source cost %d steps and a one-byte string "+
				"cost %d; the public entry takes no step of its own, so each kind "+
				"charges exactly one here too", name, got, want)
		}
	}

	// Over that threshold the charge is the node plus the source walk. Checked
	// against the string of the same length, which is charged by its caller
	// rather than here, to keep this about the regex's own increment.
	chunks := 4
	big := NewRegex(Regex{Source: strings.Repeat("a", chunks*RegexSourceStepBytesForTest())})
	for name, walk := range recursive {
		if got, want := counted(big, walk), 1+chunks; got != want {
			t.Errorf("%s: a %d-chunk regex source cost %d steps, want %d (one node plus "+
				"one per chunk of source read)", name, chunks, got, want)
		}
	}
}
