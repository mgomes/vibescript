package runtime

import "testing"

// TestRubyStripHelpers exercises the strip-family trimming helpers directly so
// byte-level cases that the lexer cannot express in a string literal (NUL,
// invalid UTF-8) are covered. The expected outputs mirror Ruby 3.x's
// String#strip / #lstrip / #rstrip, where NUL is whitespace at both edges.
func TestRubyStripHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		wantStrip  string
		wantLstrip string
		wantRstrip string
	}{
		{
			name:       "empty",
			text:       "",
			wantStrip:  "",
			wantLstrip: "",
			wantRstrip: "",
		},
		{
			name:       "no_whitespace",
			text:       "hello",
			wantStrip:  "hello",
			wantLstrip: "hello",
			wantRstrip: "hello",
		},
		{
			name:       "ascii_whitespace_both_ends",
			text:       " \t\n\v\f\rhello \t\n\v\f\r",
			wantStrip:  "hello",
			wantLstrip: "hello \t\n\v\f\r",
			wantRstrip: " \t\n\v\f\rhello",
		},
		{
			name:       "all_ascii_whitespace",
			text:       " \t\n\v\f\r",
			wantStrip:  "",
			wantLstrip: "",
			wantRstrip: "",
		},
		{
			name:       "nbsp_preserved",
			text:       "\u00a0hello\u00a0",
			wantStrip:  "\u00a0hello\u00a0",
			wantLstrip: "\u00a0hello\u00a0",
			wantRstrip: "\u00a0hello\u00a0",
		},
		{
			name:       "ogham_space_mark_preserved",
			text:       "\u1680hello\u1680",
			wantStrip:  "\u1680hello\u1680",
			wantLstrip: "\u1680hello\u1680",
			wantRstrip: "\u1680hello\u1680",
		},
		{
			name:       "em_space_preserved",
			text:       "\u2003hello\u2003",
			wantStrip:  "\u2003hello\u2003",
			wantLstrip: "\u2003hello\u2003",
			wantRstrip: "\u2003hello\u2003",
		},
		{
			name:       "bom_preserved",
			text:       "\ufeffhello\ufeff",
			wantStrip:  "\ufeffhello\ufeff",
			wantLstrip: "\ufeffhello\ufeff",
			wantRstrip: "\ufeffhello\ufeff",
		},
		{
			name: "ascii_around_unicode_space",
			text: " \u00a0 hello \u00a0 ",
			// Ruby trims the ASCII spaces but stops at the NBSP boundary.
			wantStrip:  "\u00a0 hello \u00a0",
			wantLstrip: "\u00a0 hello \u00a0 ",
			wantRstrip: " \u00a0 hello \u00a0",
		},
		{
			name: "nul_both_ends_stripped",
			text: "\x00hello\x00",
			// Ruby treats NUL as whitespace at both edges, so lstrip, rstrip,
			// and strip all drop it.
			wantStrip:  "hello",
			wantLstrip: "hello\x00",
			wantRstrip: "\x00hello",
		},
		{
			name: "trailing_nul_among_spaces",
			text: "hello \x00 \x00",
			// rstrip treats NUL like trailing whitespace.
			wantStrip:  "hello",
			wantLstrip: "hello \x00 \x00",
			wantRstrip: "hello",
		},
		{
			name: "leading_nul_then_spaces",
			text: "\x00 \thello",
			// lstrip treats the leading NUL like whitespace and continues past
			// it, trimming the run of NUL and ASCII spaces.
			wantStrip:  "hello",
			wantLstrip: "hello",
			wantRstrip: "\x00 \thello",
		},
		{
			name:       "invalid_utf8_preserved",
			text:       " \xffhello\xff ",
			wantStrip:  "\xffhello\xff",
			wantLstrip: "\xffhello\xff ",
			wantRstrip: " \xffhello\xff",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rubyStrip(tc.text); got != tc.wantStrip {
				t.Errorf("rubyStrip(%q) = %q, want %q", tc.text, got, tc.wantStrip)
			}
			if got := rubyLstrip(tc.text); got != tc.wantLstrip {
				t.Errorf("rubyLstrip(%q) = %q, want %q", tc.text, got, tc.wantLstrip)
			}
			if got := rubyRstrip(tc.text); got != tc.wantRstrip {
				t.Errorf("rubyRstrip(%q) = %q, want %q", tc.text, got, tc.wantRstrip)
			}
		})
	}
}
