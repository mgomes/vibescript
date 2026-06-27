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
