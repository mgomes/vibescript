package runtime

import (
	"context"
	"testing"
)

func TestParallelAssignmentDestructuresArrays(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def pair
  [1, 2]
end

def run
  a, b = pair
  [a, b]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2)})
}

func TestParallelAssignmentHandlesMissingExtraAndScalarValues(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a, b, c = [1, 2]
  d, e = [3, 4, 5]
  f, g = 9
  [a, b, c, d, e, f, g]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewNil(), NewInt(3), NewInt(4), NewInt(9), NewNil()})
}

func TestParallelAssignmentRestTarget(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  first, *middle, last = [1, 2, 3, 4]
  empty_first, *empty_rest, empty_last = [9]
  [first, middle, last, empty_first, empty_rest, empty_last]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewInt(1),
		NewArray([]Value{NewInt(2), NewInt(3)}),
		NewInt(4),
		NewInt(9),
		NewArray(nil),
		NewNil(),
	})
}

func TestParallelAssignmentRestTargetPreservesSourceOrder(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  *a, a = [1, 2, 3]
  first = a
  a, *a = [4, 5, 6]
  [first, a]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewInt(3),
		NewArray([]Value{NewInt(5), NewInt(6)}),
	})
}

func TestParallelAssignmentNestedTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a, (b, c), d = [1, [2, 3], 4]
  x, (y, z) = [5]
  [a, b, c, d, x, y, z]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5), NewNil(), NewNil()})
}

func TestBlockParamDestructureMoreFixedTargetsThanValues(t *testing.T) {
	t.Parallel()

	// A destructuring block parameter with more fixed targets than the element
	// provides plus a rest target previously panicked the host with a slice
	// out-of-range (a sandbox DoS). The missing fixed targets must bind to nil
	// and the rest must be empty, matching Ruby.
	script := compileScript(t, `def run
  over = [[1, 2]].map do |(a, b, c, *rest)|
    [a, b, c, rest]
  end
  empty = [[]].map do |(k, *rest)|
    [k, rest]
  end
  [over[0], empty[0]]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewNil(), NewArray(nil)}),
		NewArray([]Value{NewNil(), NewArray(nil)}),
	})
}

func TestParallelAssignmentSupportsMutableTargetsAndEvaluatesRHSOnce(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def bump(record)
  record[:count] = record[:count] + 1
  [record[:count], record[:count] + 1]
end

def run
  values = [0, 0]
  record = {slot: 0, count: 0}
  values[1], record.slot = bump(record)
  [values[1], record[:slot], record[:count]]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(1)})
}
