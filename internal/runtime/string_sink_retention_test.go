package runtime

import (
	"context"
	"strings"
	"testing"
)

// whitespaceRetentionSeed is a padding-only seed, so `seed * 200` in
// retentionScript is a megabyte of whitespace that a strip reduces to whatever
// content is appended to it.
func whitespaceRetentionSeed() string {
	return strings.Repeat(" ", 5_000)
}

// TestStripFamilyDoesNotRetainItsBacking pins that a stripped string stops
// holding the string it was trimmed from.
//
// strip, lstrip and rstrip return a window onto the receiver, so padding that
// dwarfs the content leaves the result tiny while it pins the whole receiver: a
// one-character strip of a megabyte of whitespace was charged one byte and held
// the megabyte, and 200 of them retained 192.2 MiB under an 8 MiB quota.
//
// Not parallel: it measures process-wide heap.
func TestStripFamilyDoesNotRetainItsBacking(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"strip", `(big + "x").strip`},
		{"strip!", `(big + "x").strip!`},
		{"lstrip", `(big + "x").lstrip`},
		{"lstrip!", `(big + "x").lstrip!`},
		{"rstrip", `("x" + big).rstrip`},
		{"rstrip!", `("x" + big).rstrip!`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), whitespaceRetentionSeed(), 200)
			assertUnderRetentionLimit(t, "stripped strings", held)
		})
	}
}

// TestStripFamilyKeepsItsCharacters pins that detaching the result did not
// change what the strip family yields, including the NUL byte Ruby's strip
// treats as whitespace, an all-whitespace receiver, a receiver with nothing to
// trim, and the nil the mutator forms return when they change nothing.
//
// The results are compared as values rather than through Inspect: the receivers
// here are made of control characters, and an escaped rendering would compare
// the escaping as much as the trimming.
func TestStripFamilyKeepsItsCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.strip, s.lstrip, s.rstrip, s.strip!, s.lstrip!, s.rstrip!]
end`)
	// nil marks a mutator that must report "no change" rather than a string.
	const unchanged = "\x01no change\x01"
	for _, tc := range []struct {
		name string
		in   string
		want [6]string
	}{
		{
			"padded",
			" \t\na\x00b \r\n",
			[6]string{"a\x00b", "a\x00b \r\n", " \t\na\x00b", "a\x00b", "a\x00b \r\n", " \t\na\x00b"},
		},
		{"untrimmed", "a漢b", [6]string{"a漢b", "a漢b", "a漢b", unchanged, unchanged, unchanged}},
		{"all whitespace", " \t\n", [6]string{"", "", "", "", "", ""}},
		{"empty", "", [6]string{"", "", "", unchanged, unchanged, unchanged}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := script.Call(context.Background(), "run", []Value{NewString(tc.in)}, CallOptions{})
			if err != nil {
				t.Fatalf("strip family failed: %v", err)
			}
			results := got.Array()
			if len(results) != len(tc.want) {
				t.Fatalf("strip family over %q returned %d results, want %d", tc.in, len(results), len(tc.want))
			}
			for i, want := range tc.want {
				if want == unchanged {
					if !results[i].IsNil() {
						t.Fatalf("result %d over %q = %q, want nil", i, tc.in, results[i].String())
					}
					continue
				}
				if results[i].Kind() != KindString || results[i].String() != want {
					t.Fatalf("result %d over %q = %s, want %q", i, tc.in, results[i].Inspect(), want)
				}
			}
		})
	}
}
