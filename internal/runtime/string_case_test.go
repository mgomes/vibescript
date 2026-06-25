package runtime

import "testing"

// TestStringCaseDefaultMapping verifies that upcase, downcase, capitalize, and
// swapcase apply full Unicode case mapping by default. The expected values are
// taken from Ruby 2.6 (the reference in issue #641), so "ß" expands to "SS",
// "İ" lowercases to "i" plus a combining dot, the "ﬁ"/"ﬃ" ligatures expand, and
// capitalize uses the titlecase mapping for the leading "ǆ" digraph.
func TestStringCaseDefaultMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "upcase expands sharp s", script: `def run() "Straße".upcase end`, want: "STRASSE"},
		{name: "upcase micro sign to greek mu", script: `def run() "µ".upcase end`, want: "Μ"},
		{name: "upcase fi ligature", script: `def run() "ﬁ".upcase end`, want: "FI"},
		{name: "upcase dotted capital i is idempotent", script: `def run() "İ".upcase end`, want: "İ"},
		{name: "downcase keeps sharp s", script: `def run() "STRAßE".downcase end`, want: "straße"},
		{name: "downcase dotted capital i adds combining dot", script: `def run() "İ".downcase end`, want: "i̇"},
		{name: "downcase greek keeps medial sigma", script: `def run() "ΟΔΟΣ".downcase end`, want: "οδοσ"},
		{name: "capitalize titlecases leading digraph", script: `def run() "ǆ".capitalize end`, want: "ǅ"},
		{name: "capitalize expands ligature and downcases rest", script: `def run() "ﬃ".capitalize end`, want: "Ffi"},
		{name: "capitalize only touches first character", script: `def run() "hello world".capitalize end`, want: "Hello world"},
		{name: "capitalize downcases trailing letters", script: `def run() "iSTANBUL".capitalize end`, want: "Istanbul"},
		{name: "swapcase expands sharp s on uppercase path", script: `def run() "Straße".swapcase end`, want: "sTRASSE"},
		{name: "swapcase mixed input", script: `def run() "Hello VIBE".swapcase end`, want: "hELLO vibe"},
		// Circled Latin letters (general category So) and Roman numerals
		// (category Nl) carry Unicode case mappings even though they are not
		// Lu/Ll/Lt letters, so swapcase must toggle them like Ruby does.
		{name: "swapcase circled capital letter", script: `def run() "Ⓐ".swapcase end`, want: "ⓐ"},
		{name: "swapcase circled small letter", script: `def run() "ⓐ".swapcase end`, want: "Ⓐ"},
		{name: "swapcase uppercase roman numeral", script: `def run() "Ⅰ".swapcase end`, want: "ⅰ"},
		{name: "swapcase lowercase roman numeral", script: `def run() "ⅰ".swapcase end`, want: "Ⅰ"},
		{name: "swapcase mixed non-letter case pairs", script: `def run() "Ⓐb Ⅹⅰ".swapcase end`, want: "ⓐB ⅹⅠ"},
		{name: "empty string upcase", script: `def run() "".upcase end`, want: ""},
		{name: "empty string capitalize", script: `def run() "".capitalize end`, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("case mapping mismatch: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStringCaseAsciiMode verifies the :ascii option, which restricts case
// mapping to ASCII letters and leaves every other byte (including non-ASCII
// runes) untouched. Expected values match Ruby's upcase(:ascii) family.
func TestStringCaseAsciiMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "upcase leaves sharp s alone", script: `def run() "Straße".upcase(:ascii) end`, want: "STRAßE"},
		{name: "upcase leaves accented letters alone", script: `def run() "àé".upcase(:ascii) end`, want: "àé"},
		{name: "downcase leaves sharp s alone", script: `def run() "STRAßE".downcase(:ascii) end`, want: "straße"},
		{name: "capitalize ascii only", script: `def run() "fOO".capitalize(:ascii) end`, want: "Foo"},
		{name: "capitalize leaves non-ascii leader alone", script: `def run() "ÀfOO".capitalize(:ascii) end`, want: "Àfoo"},
		{name: "swapcase ascii only", script: `def run() "Straße".swapcase(:ascii) end`, want: "sTRAßE"},
		{name: "swapcase leaves non-ascii alone", script: `def run() "Àbc".swapcase(:ascii) end`, want: "ÀBC"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("ascii mode mismatch: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStringCaseFoldMode verifies the :fold option on downcase, which applies
// Unicode case folding so that case-insensitive comparisons normalize forms such
// as "ß" to "ss" and "İ" to "i" plus a combining dot.
func TestStringCaseFoldMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "fold expands sharp s", script: `def run() "Straße".downcase(:fold) end`, want: "strasse"},
		{name: "fold greek keeps medial sigma", script: `def run() "ΟΔΟΣ".downcase(:fold) end`, want: "οδοσ"},
		{name: "fold dotted capital i", script: `def run() "İ".downcase(:fold) end`, want: "i̇"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("fold mode mismatch: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStringCaseRejectsBadOptions verifies that invalid case-mapping options
// raise clear errors: :fold is only allowed on downcase, unknown symbols are
// rejected, non-symbol arguments are rejected, and at most one option is allowed.
func TestStringCaseRejectsBadOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "upcase rejects fold", script: `def run() "x".upcase(:fold) end`, want: "string.upcase does not support the :fold option"},
		{name: "capitalize rejects fold", script: `def run() "x".capitalize(:fold) end`, want: "string.capitalize does not support the :fold option"},
		{name: "swapcase rejects fold", script: `def run() "x".swapcase(:fold) end`, want: "string.swapcase does not support the :fold option"},
		{name: "upcase rejects unknown symbol", script: `def run() "x".upcase(:bogus) end`, want: "string.upcase does not support the :bogus option"},
		{name: "downcase rejects unknown symbol", script: `def run() "x".downcase(:bogus) end`, want: "string.downcase does not support the :bogus option"},
		{name: "upcase rejects string argument", script: `def run() "x".upcase("ascii") end`, want: "string.upcase option must be a symbol"},
		{name: "upcase rejects two options", script: `def run() "x".upcase(:ascii, :ascii) end`, want: "string.upcase accepts at most one case-mapping option"},
		{name: "downcase bang rejects unknown symbol", script: `def run() "x".downcase!(:bogus) end`, want: "string.downcase! does not support the :bogus option"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestStringCaseBangReturnsNilWhenUnchanged verifies the bang variants return
// the mutated string when the value changes and nil when it is already in the
// requested case, matching Ruby's in-place mutator convention.
func TestStringCaseBangReturnsNilWhenUnchanged(t *testing.T) {
	t.Parallel()

	t.Run("returns value when changed", func(t *testing.T) {
		t.Parallel()
		cases := map[string]string{
			`def run() "straße".upcase! end`:          "STRASSE",
			`def run() "STRAßE".downcase! end`:        "straße",
			`def run() "hÉLLo wORLD".capitalize! end`: "Héllo world",
			`def run() "Hello VIBE".swapcase! end`:    "hELLO vibe",
			`def run() "STRAßE".downcase!(:fold) end`: "strasse",
		}
		for script, want := range cases {
			s := compileScript(t, script)
			result := callFunc(t, s, "run", nil)
			if result.Kind() != KindString || result.String() != want {
				t.Fatalf("%s = %v, want %q", script, result, want)
			}
		}
	})

	t.Run("returns nil when unchanged", func(t *testing.T) {
		t.Parallel()
		scripts := []string{
			`def run() "STRASSE".upcase! end`,
			`def run() "straße".downcase! end`,
			`def run() "Hello world".capitalize! end`,
			// swapcase has no cased letters to toggle, so it leaves "12-3" intact.
			`def run() "12-3".swapcase! end`,
		}
		for _, script := range scripts {
			s := compileScript(t, script)
			result := callFunc(t, s, "run", nil)
			if result.Kind() != KindNil {
				t.Fatalf("%s expected nil, got %v", script, result)
			}
		}
	})
}

// TestStringCaseHelpers exercises the pure mapping helpers directly so the
// invalid-UTF-8 fallback and the documented titlecase swapcase divergence are
// covered without round-tripping through the interpreter.
func TestStringCaseHelpers(t *testing.T) {
	t.Parallel()

	t.Run("invalid utf8 falls back to ascii only", func(t *testing.T) {
		t.Parallel()
		invalid := "abc\xffDEF"
		if got := stringUpcase(invalid, caseModeDefault); got != "ABC\xffDEF" {
			t.Fatalf("upcase invalid utf8 = %q, want %q", got, "ABC\xffDEF")
		}
		if got := stringDowncase(invalid, caseModeDefault); got != "abc\xffdef" {
			t.Fatalf("downcase invalid utf8 = %q, want %q", got, "abc\xffdef")
		}
		if got := stringSwapCase(invalid, caseModeDefault); got != "ABC\xffdef" {
			t.Fatalf("swapcase invalid utf8 = %q, want %q", got, "ABC\xffdef")
		}
		if got := stringCapitalize("ßabc\xff", caseModeDefault); got != "ßabc\xff" {
			t.Fatalf("capitalize invalid utf8 = %q, want %q", got, "ßabc\xff")
		}
	})

	t.Run("titlecase swapcase lowercases the digraph", func(t *testing.T) {
		t.Parallel()
		// Ruby toggles each component of "ǅ" to produce "dŽ"; Vibescript
		// deliberately lowercases the titlecase digraph instead (see the
		// stringSwapCase comment), yielding "ǆ".
		if got := stringSwapCase("ǅ", caseModeDefault); got != "ǆ" {
			t.Fatalf("swapcase titlecase digraph = %q, want %q", got, "ǆ")
		}
	})

	t.Run("swapcase toggles non-letter case pairs", func(t *testing.T) {
		t.Parallel()
		// These runes are cased (they have distinct upper/lower mappings) but
		// fall outside the Lu/Ll/Lt letter categories, so the Is{Upper,Lower}
		// predicates miss them. swapcase must still toggle them via the case
		// mapping, matching Ruby. The combining iota subscript (U+0345) only has
		// an uppercase mapping (to capital iota), so it is treated as lowercase.
		cases := map[string]string{
			"Ⓐ": "ⓐ", // circled capital A (So) -> circled small a
			"ⓐ": "Ⓐ", // circled small a (So) -> circled capital A
			"Ⅰ": "ⅰ", // Roman numeral one (Nl) -> small Roman numeral one
			"ⅰ": "Ⅰ", // small Roman numeral one (Nl) -> Roman numeral one
			"ͅ": "Ι", // combining iota subscript -> capital iota
		}
		for in, want := range cases {
			if got := stringSwapCase(in, caseModeDefault); got != want {
				t.Fatalf("swapcase(%q) = %q, want %q", in, got, want)
			}
		}
	})
}
