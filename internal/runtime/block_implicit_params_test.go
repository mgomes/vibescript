package runtime

import (
	"context"
	"testing"
)

func TestImplicitBlockNumberedParameters(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  doubled = [1, 2, 3].map { _1 * 2 }
  indexes = ["a", "b", "c"].map_with_index { _2 }
  [doubled, indexes]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(2), NewInt(4), NewInt(6)}),
		NewArray([]Value{NewInt(0), NewInt(1), NewInt(2)}),
	})
}

func TestImplicitBlockItParameter(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [1, 2, 3].map { it * 3 }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(3), NewInt(6), NewInt(9)})
}

func TestImplicitBlockItCalleeStaysCallable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def it(value)
  value + 1
end

def run
  [1, 2, 3].map { it(_1) }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(2), NewInt(3), NewInt(4)})
}

func TestImplicitBlockParamsDoNotLeakAcrossNestedBlocks(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [10].map { [1, 2].map { _1 * 2 } }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(2), NewInt(4)}),
	})
}

func TestImplicitBlockParamsDriveHashYieldArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  pair = []
  { b: 2, a: 1 }.each { pair = pair.push(_1) }
  keyed = []
  { b: 2, a: 1 }.each { keyed = keyed.push([_1, _2]) }
  [pair, keyed]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{
			NewArray([]Value{NewSymbol("a"), NewInt(1)}),
			NewArray([]Value{NewSymbol("b"), NewInt(2)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewSymbol("a"), NewInt(1)}),
			NewArray([]Value{NewSymbol("b"), NewInt(2)}),
		}),
	})
}
