package runtime

import "testing"

// TestStringToSymbol covers String#to_sym and its alias String#intern, both of
// which return the symbol named by the receiver. The result must be a genuine
// symbol value: Vibescript symbol/string equality is kind-sensitive, so the
// converted value equals the matching symbol literal but not the source string.
func TestStringToSymbol(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   Value
	}{
		{
			name:   "to_sym on identifier name",
			script: `def run() "name".to_sym end`,
			want:   NewSymbol("name"),
		},
		{
			name:   "intern is an alias for to_sym",
			script: `def run() "name".intern end`,
			want:   NewSymbol("name"),
		},
		{
			name:   "to_sym preserves whitespace and punctuation",
			script: `def run() "a b!".to_sym end`,
			want:   NewSymbol("a b!"),
		},
		{
			name:   "to_sym on empty string yields the empty symbol",
			script: `def run() "".to_sym end`,
			want:   NewSymbol(""),
		},
		{
			name:   "converted value equals the matching symbol literal",
			script: `def run() "name".to_sym == :name end`,
			want:   NewBool(true),
		},
		{
			name:   "converted value is a symbol, not the source string",
			script: `def run() "name".to_sym == "name" end`,
			want:   NewBool(false),
		},
		{
			name:   "round trips through a percent-symbol-array name",
			script: `def run() syms = %i[draft]; syms[0] == "draft".to_sym end`,
			want:   NewBool(true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
			if tc.want.Kind() == KindSymbol && got.Kind() != KindSymbol {
				t.Fatalf("expected symbol kind, got %v", got.Kind())
			}
		})
	}
}

// TestSymbolToString covers Symbol#id2name and Symbol#to_s, which both return
// the symbol's name as a string, and Symbol#to_sym, which returns the receiver.
func TestSymbolToString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   Value
	}{
		{
			name:   "id2name returns the symbol name",
			script: `def run() :name.id2name end`,
			want:   NewString("name"),
		},
		{
			name:   "to_s returns the symbol name",
			script: `def run() :name.to_s end`,
			want:   NewString("name"),
		},
		{
			name:   "id2name on a percent-symbol-array name",
			script: `def run() syms = %i[draft]; syms[0].id2name end`,
			want:   NewString("draft"),
		},
		{
			name:   "id2name result is a string, not a symbol",
			script: `def run() :name.id2name == :name end`,
			want:   NewBool(false),
		},
		{
			name:   "id2name result equals the matching string",
			script: `def run() :name.id2name == "name" end`,
			want:   NewBool(true),
		},
		{
			name:   "to_sym on a symbol returns the receiver",
			script: `def run() :name.to_sym == :name end`,
			want:   NewBool(true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestSymbolRoundTrip proves the string/symbol conversion pair is reversible:
// converting a string to a symbol and back yields the original string, and the
// reverse for a symbol.
func TestSymbolRoundTrip(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
		def run()
			{
				str_round: "report".to_sym.to_s,
				str_round_id: "report".intern.id2name,
				sym_round: :report.id2name.to_sym == :report,
			}
		end
	`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", got.Kind())
	}
	hash := got.Hash()
	if !hash["str_round"].Equal(NewString("report")) {
		t.Fatalf("str_round: got %#v", hash["str_round"])
	}
	if !hash["str_round_id"].Equal(NewString("report")) {
		t.Fatalf("str_round_id: got %#v", hash["str_round_id"])
	}
	if !hash["sym_round"].Equal(NewBool(true)) {
		t.Fatalf("sym_round: got %#v", hash["sym_round"])
	}
}

// TestSymbolConversionHashLookup documents that converted symbols index symbol
// keys without collapsing them into string keys.
func TestSymbolConversionHashLookup(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
		def run()
			by_symbol = { name: "symbol", "name" => "string" }
			{
				symbol_key: by_symbol["name".to_sym],
				string_key: by_symbol["name"],
				name_round: by_symbol[:name.to_s.to_sym],
			}
		end
	`)
	got := callFunc(t, script, "run", nil)
	hash := got.Hash()
	if !hash["symbol_key"].Equal(NewString("symbol")) {
		t.Fatalf("symbol_key: got %#v", hash["symbol_key"])
	}
	if !hash["string_key"].Equal(NewString("string")) {
		t.Fatalf("string_key: got %#v", hash["string_key"])
	}
	if !hash["name_round"].Equal(NewString("symbol")) {
		t.Fatalf("name_round: got %#v", hash["name_round"])
	}
}

// TestSymbolConversionArgRejection verifies the conversion members reject
// positional arguments rather than silently ignoring them, matching the
// arity-strict style of the surrounding string members.
func TestSymbolConversionArgRejection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "to_sym rejects arguments",
			script: `def run() "name".to_sym(1) end`,
			want:   "string.to_sym does not take arguments",
		},
		{
			name:   "intern rejects arguments",
			script: `def run() "name".intern(1) end`,
			want:   "string.intern does not take arguments",
		},
		{
			name:   "id2name rejects arguments",
			script: `def run() :name.id2name(1) end`,
			want:   "symbol.id2name does not take arguments",
		},
		{
			name:   "symbol to_s rejects arguments",
			script: `def run() :name.to_s(1) end`,
			want:   "symbol.to_s does not take arguments",
		},
		{
			name:   "symbol to_sym rejects arguments",
			script: `def run() :name.to_sym(1) end`,
			want:   "symbol.to_sym does not take arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestSymbolUnknownMember confirms unknown symbol members surface a clear error
// with a did-you-mean suggestion, replacing the prior "unsupported member
// access on symbol" failure.
func TestSymbolUnknownMember(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run() :name.id2nam end`)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, `unknown symbol method id2nam (did you mean "id2name"?)`)
}
