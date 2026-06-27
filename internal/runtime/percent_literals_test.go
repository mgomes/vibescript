package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestPercentWordAndSymbolArrayLiterals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [%w[alpha beta], %i[open closed], %w[alpha\ beta literal\n]]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("alpha"), NewString("beta")}),
		NewArray([]Value{NewSymbol("open"), NewSymbol("closed")}),
		NewArray([]Value{NewString("alpha beta"), NewString(`literal\n`)}),
	})
}

// TestPercentInterpolatedWordAndSymbolArrayLiterals covers the uppercase %W/%I
// forms, which apply double-quoted string semantics to each entry: embedded
// #{...} is evaluated, \t/\n become control characters, and a space inside an
// interpolation does not split the word. %W yields strings and %I yields
// symbols, matching Ruby.
func TestPercentInterpolatedWordAndSymbolArrayLiterals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  name = "Ada"
  count = 2
  [
    %W[hello #{name} world],
    %I[hello #{name} world],
    %W[a #{count + 1} d],
    %W[tab\there a\ b],
    %W[lit\#{name} tail],
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("hello"), NewString("Ada"), NewString("world")}),
		NewArray([]Value{NewSymbol("hello"), NewSymbol("Ada"), NewSymbol("world")}),
		NewArray([]Value{NewString("a"), NewString("3"), NewString("d")}),
		NewArray([]Value{NewString("tab\there"), NewString("a b")}),
		NewArray([]Value{NewString("lit#{name}"), NewString("tail")}),
	})
}

// TestPercentInterpolatedArrayLiteralDelimiterInsideInterpolation confirms that
// a delimiter character appearing inside a %W/%I interpolation expression—even
// when nested in a quoted string—does not close the literal early, so the entry
// evaluates to the interpolated value rather than being truncated. The expected
// values match Ruby:
//
//	%W[#{"]"}]          => ["]"]
//	%W[#{"]"}foo bar]   => ["]foo", "bar"]
//	%I{#{"}"}}          => [:"}"]
//	%W(#{"("}x #{")"}y) => ["(x", ")y"]
//	%W[a#{"b#{x}c"}d e] => ["abzcd", "e"]
func TestPercentInterpolatedArrayLiteralDelimiterInsideInterpolation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  x = "z"
  [
    %W[#{"]"}],
    %W[#{"]"}foo bar],
    %I{#{"}"}},
    %W(#{"("}x #{")"}y),
    %W[a#{"b#{x}c"}d e],
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("]")}),
		NewArray([]Value{NewString("]foo"), NewString("bar")}),
		NewArray([]Value{NewSymbol("}")}),
		NewArray([]Value{NewString("(x"), NewString(")y")}),
		NewArray([]Value{NewString("abzcd"), NewString("e")}),
	})
}

// TestPercentInterpolatedArrayLiteralNestedPercentLiteral confirms that a
// nested percent-array literal inside a %W/%I (or double-quoted) interpolation
// expression is consumed whole, so a closing-delimiter character inside the
// nested literal's body does not truncate the outer literal. A bare "%" that is
// modulo is left untouched. The expected values use Vibescript's array
// stringification (a comma-separated list without per-element quoting); the
// significance is that each nested array is interpolated intact rather than
// being truncated at the inner "}" or "]":
//
//	%W[#{%w[}]}]             => ["[}]"]
//	%W[head #{%w[a b]} tail] => ["head", "[a, b]", "tail"]
//	"x#{%w[}]}"              => "x[}]"
//	a=10; w=3; %W[#{a % w}]  => ["1"]
func TestPercentInterpolatedArrayLiteralNestedPercentLiteral(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a = 10
  w = 3
  [
    %W[#{%w[}]}],
    %W[head #{%w[a b]} tail],
    "x#{%w[}]}",
    %W[#{a % w}],
  ]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("[}]")}),
		NewArray([]Value{NewString("head"), NewString("[a, b]"), NewString("tail")}),
		NewString("x[}]"),
		NewArray([]Value{NewString("1")}),
	})
}

// TestCompactPercentModuloInsideInterpolationMatchesInlineForm guards the
// regression where a modulo expression embedded in a string interpolation, whose
// right-hand side indexes a local named like a percent-array prefix (w/i/W/I),
// was mis-scanned as a percent-array literal. Because `total` is a local, the
// `%w[...]` is `total % w[...]`, and the close delimiter (`]`) appearing inside a
// quoted string in the index must not end the index or the interpolation early.
// Both the compact interpolated form and the inline form must evaluate to the
// same modulo result. Verified against Ruby:
//
//	total = 10; w = [3, 7]
//	total %w[ "]" .length - 1 ]   # => 1   (10 % w["]".length - 1] == 10 % w[0])
//	"#{total %w[ "]" .length - 1 ]}" # => "1"
func TestCompactPercentModuloInsideInterpolationMatchesInlineForm(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  total = 10
  w = [3, 7]
  inline = total %w[ "]" .length - 1 ]
  interp = "#{total %w[ "]" .length - 1 ]}"
  [inline, interp]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewString("1")})
}

// TestPercentInterpolatedSymbolArrayWithNestedSymbolLiteral confirms the %I form
// also descends through a nested percent-symbol literal in its interpolation and
// produces a genuine symbol. Verified against Ruby: %I[#{%i[}]}] => [:"[:\"}\"]"].
func TestPercentInterpolatedSymbolArrayWithNestedSymbolLiteral(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  syms = %I[#{%i[}]}]
  [syms[0] == :draft, syms.length]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewBool(false), NewInt(1)})
}

// TestPercentArrayLiteralHashDelimiter confirms '#' works as a percent-array
// delimiter for every form. The interpolation handling for the uppercase
// forms must not consume the closing '#'. An escaped "\#" stays literal, and
// when '#' is the delimiter "#{" does not interpolate. Verified against Ruby:
//
//	%w#foo bar# => ["foo", "bar"]
//	%W#foo bar# => ["foo", "bar"]
//	%i#foo bar# => [:foo, :bar]
//	%I#foo bar# => [:foo, :bar]
//	%W#a\#b#    => ["a#b"]
func TestPercentArrayLiteralHashDelimiter(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [%w#foo bar#, %W#foo bar#, %i#foo bar#, %I#foo bar#, %W#a\#b#]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("foo"), NewString("bar")}),
		NewArray([]Value{NewString("foo"), NewString("bar")}),
		NewArray([]Value{NewSymbol("foo"), NewSymbol("bar")}),
		NewArray([]Value{NewSymbol("foo"), NewSymbol("bar")}),
		NewArray([]Value{NewString("a#b")}),
	})
}

// TestPercentInterpolatedSymbolArrayProducesSymbols guards that interpolated %I
// entries are genuine symbols, not strings: Vibescript symbol/string equality
// is kind-sensitive.
func TestPercentInterpolatedSymbolArrayProducesSymbols(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  name = "draft"
  syms = %I[#{name} review]
  [syms[0] == :draft, syms[0] == "draft", syms[1] == :review]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewBool(true), NewBool(false), NewBool(true)})
}

// TestPercentInterpolatedArrayLiteralBoundsMaterialization confirms a %W entry
// that interpolates a large value is subject to the string memory quota just
// like a double-quoted interpolation, so the interpolating forms cannot escape
// the sandbox.
func TestPercentInterpolatedArrayLiteralBoundsMaterialization(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 256}, `def run(big)
  %W[head #{big}]
end`)

	big := NewString(strings.Repeat("x", 4096))
	requireCallErrorContains(t, script, "run", []Value{big}, CallOptions{}, "memory")
}

// TestEvalInterpolatedSymbolLiteralBoundsMaterialization exercises the symbol
// path of buildInterpolatedString directly: building a %I entry from a large
// interpolated value must trip the memory quota rather than allocating it.
func TestEvalInterpolatedSymbolLiteralBoundsMaterialization(t *testing.T) {
	t.Parallel()

	lit := &InterpolatedSymbol{Parts: []StringPart{
		StringText{Text: strings.Repeat("0123456789abcdef", 64)},
	}}
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 20,
		memoryQuota: 256,
	}
	env := newEnv(nil)
	exec.pushEnv(env)

	_, err := exec.evalInterpolatedSymbolLiteral(lit, env)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}
