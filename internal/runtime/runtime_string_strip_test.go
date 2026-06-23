package runtime

import "testing"

// TestStringStripRubyAlignment drives the strip-family methods through the
// interpreter to confirm Ruby-aligned whitespace semantics: ASCII whitespace is
// trimmed while Unicode spaces (NBSP, em space, BOM) are preserved, and the NUL
// asymmetry between lstrip and rstrip holds. Inputs are passed as arguments
// because the lexer cannot express NUL or \u escapes in a string literal.
func TestStringStripRubyAlignment(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def do_strip(text) text.strip end
    def do_lstrip(text) text.lstrip end
    def do_rstrip(text) text.rstrip end
  `)

	tests := []struct {
		name   string
		method string
		input  string
		want   string
	}{
		{
			name:   "strip_ascii",
			method: "do_strip",
			input:  " \thello\t ",
			want:   "hello",
		},
		{
			name:   "strip_nbsp_preserved",
			method: "do_strip",
			input:  "\u00a0hello\u00a0",
			want:   "\u00a0hello\u00a0",
		},
		{
			name:   "strip_em_preserved",
			method: "do_strip",
			input:  "\u2003hello\u2003",
			want:   "\u2003hello\u2003",
		},
		{
			name:   "strip_bom_preserved",
			method: "do_strip",
			input:  "\ufeffhello\ufeff",
			want:   "\ufeffhello\ufeff",
		},
		{
			name:   "strip_nul_asymmetry",
			method: "do_strip",
			input:  "\x00hello\x00",
			want:   "\x00hello",
		},
		{
			name:   "lstrip_ascii",
			method: "do_lstrip",
			input:  " \thello\t",
			want:   "hello\t",
		},
		{
			name:   "lstrip_nbsp_preserved",
			method: "do_lstrip",
			input:  "\u00a0hello",
			want:   "\u00a0hello",
		},
		{
			name:   "lstrip_keeps_leading_nul",
			method: "do_lstrip",
			input:  "\x00hello",
			want:   "\x00hello",
		},
		{
			name:   "rstrip_ascii",
			method: "do_rstrip",
			input:  "\thello\t ",
			want:   "\thello",
		},
		{
			name:   "rstrip_nbsp_preserved",
			method: "do_rstrip",
			input:  "hello\u00a0",
			want:   "hello\u00a0",
		},
		{
			name:   "rstrip_drops_trailing_nul",
			method: "do_rstrip",
			input:  "hello\x00",
			want:   "hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := callFunc(t, script, tc.method, []Value{NewString(tc.input)})
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("%s(%q) = %q, want %q", tc.method, tc.input, got, tc.want)
			}
		})
	}
}

// TestStringStripBangReturnsNil confirms the bang variants return nil when the
// receiver already has no strippable whitespace, mirroring Ruby. Unicode spaces
// must not trigger a mutation result because they are preserved.
func TestStringStripBangReturnsNil(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def do_strip(text) text.strip! end
    def do_lstrip(text) text.lstrip! end
    def do_rstrip(text) text.rstrip! end
  `)

	nilCases := []struct {
		name   string
		method string
		input  string
	}{
		{
			name:   "strip_no_change",
			method: "do_strip",
			input:  "hello",
		},
		{
			name:   "strip_nbsp_unchanged",
			method: "do_strip",
			input:  "\u00a0hello\u00a0",
		},
		{
			name:   "lstrip_no_change",
			method: "do_lstrip",
			input:  "hello\t",
		},
		{
			name:   "lstrip_leading_nbsp_unchanged",
			method: "do_lstrip",
			input:  "\u00a0hello",
		},
		{
			name:   "rstrip_no_change",
			method: "do_rstrip",
			input:  "\thello",
		},
		{
			name:   "rstrip_trailing_nbsp_unchanged",
			method: "do_rstrip",
			input:  "hello\u00a0",
		},
	}

	for _, tc := range nilCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := callFunc(t, script, tc.method, []Value{NewString(tc.input)})
			if result.Kind() != KindNil {
				t.Fatalf("%s(%q) = %v, want nil", tc.method, tc.input, result.Kind())
			}
		})
	}

	changeCases := []struct {
		name   string
		method string
		input  string
		want   string
	}{
		{
			name:   "strip_trims",
			method: "do_strip",
			input:  " hello ",
			want:   "hello",
		},
		{
			name:   "lstrip_trims",
			method: "do_lstrip",
			input:  " hello",
			want:   "hello",
		},
		{
			name:   "rstrip_trims",
			method: "do_rstrip",
			input:  "hello ",
			want:   "hello",
		},
		{
			name:   "rstrip_trims_trailing_nul",
			method: "do_rstrip",
			input:  "hello\x00",
			want:   "hello",
		},
	}

	for _, tc := range changeCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := callFunc(t, script, tc.method, []Value{NewString(tc.input)})
			if result.Kind() != KindString {
				t.Fatalf("%s(%q) kind = %v, want string", tc.method, tc.input, result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("%s(%q) = %q, want %q", tc.method, tc.input, got, tc.want)
			}
		})
	}
}
