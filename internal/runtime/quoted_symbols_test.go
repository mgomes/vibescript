package runtime

import "testing"

// TestQuotedSymbolValues verifies that quoted symbol literals evaluate to the
// same symbol values as bare symbols and string-to-symbol conversion, so they
// interoperate everywhere symbols are used.
func TestQuotedSymbolValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "to_s",
			source: `def run; :"foo-bar".to_s; end`,
			want:   NewString("foo-bar"),
		},
		{
			name:   "equals_self",
			source: `def run; :"foo-bar" == :"foo-bar"; end`,
			want:   NewBool(true),
		},
		{
			name:   "equals_string_to_sym",
			source: `def run; :"foo-bar" == "foo-bar".to_sym; end`,
			want:   NewBool(true),
		},
		{
			name:   "single_quoted_no_interpolation",
			source: `def run; :'a#{1}'.to_s; end`,
			want:   NewString("a#{1}"),
		},
		{
			name:   "empty_symbol_to_s",
			source: `def run; :"".to_s; end`,
			want:   NewString(""),
		},
		{
			name:   "index_string_keyed_hash",
			source: `def run; ({ "foo-bar": 1 })[:"foo-bar"]; end`,
			want:   NewInt(1),
		},
		{
			name:   "inspect_round_trips",
			source: `def run; :"foo-bar".inspect; end`,
			want:   NewString(`:"foo-bar"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("run() = %s, want %s", got.Inspect(), tc.want.Inspect())
			}
		})
	}
}

// TestQuotedSymbolHashKeyIsSymbol verifies that a quoted-string hash key parses
// to a symbol key, matching Ruby, so it can be read back with a quoted symbol.
func TestQuotedSymbolHashKeyIsSymbol(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run; ({ "foo-bar": 1 }).keys.first; end`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindSymbol {
		t.Fatalf("hash key kind = %v, want %v", got.Kind(), KindSymbol)
	}
	want := NewSymbol("foo-bar")
	if !got.Equal(want) {
		t.Fatalf("hash key = %s, want %s", got.Inspect(), want.Inspect())
	}
}
