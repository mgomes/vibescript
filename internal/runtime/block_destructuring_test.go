package runtime

import (
	"context"
	"testing"
)

func TestBlockParameterDestructuring(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  pairs = [[1, 2], [3]]
  pairs.map do |(a, b)|
    [a, b]
  end
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
		NewArray([]Value{NewInt(3), NewNil()}),
	})
}

func TestBlockParameterDestructuringRestAndNestedTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  rest_rows = [[1, 2, 3]]
  nested_rows = [[4, [5, 6]]]
  rest_result = rest_rows.map do |(first, *rest)|
    [first, rest]
  end
  nested_result = nested_rows.map do |(left, (middle, right))|
    [left, middle, right]
  end
  [rest_result[0], nested_result[0]]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewArray([]Value{NewInt(2), NewInt(3)})}),
		NewArray([]Value{NewInt(4), NewInt(5), NewInt(6)}),
	})
}
