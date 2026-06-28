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

func TestExplicitEmptyBlockParamsDisableImplicitParams(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [1].map { || _1 }
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "undefined variable _1")
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

func TestImplicitBlockItCalleeWithPercentArrayStaysCallable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def it(values)
  values.join("-")
end

def run
  [1].map { it %w[a b] }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewString("a-b")})
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

func TestImplicitBlockParamsInAssignmentTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  records = [{seen: false}]
  records.each { _1[:seen] = true }
  records[0][:seen]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewBool(true)) {
		t.Fatalf("run = %#v, want true", got)
	}
}

func TestImplicitBlockParamsAreLocalsForPercentModuloParsing(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  w = [2]
  [5].map { _1 %w[0] }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1)})
}

func TestImplicitBlockParamsIgnoreRescueBindingOutsideHandler(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [7].map { begin; raise "x"; rescue => it; nil; end; it }
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(7)})
}
