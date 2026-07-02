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

func TestBlockParameterDestructuringAnonymousRest(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  rows = [[1, 2, 3, 4], [5, 6]]
  trailing = rows.map do |(head, *)|
    head
  end
  middle = rows.map do |(head, *, tail)|
    [head, tail]
  end
  leading = rows.map do |(*, tail)|
    tail
  end
  [trailing, middle, leading]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(5)}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1), NewInt(4)}),
			NewArray([]Value{NewInt(5), NewInt(6)}),
		}),
		NewArray([]Value{NewInt(4), NewInt(6)}),
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

func TestBlockParameterDestructuringTypeAnnotations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  rows = [[1, "Ada"], [2, "Lin"]]
  typed = rows.map do |(id: int, name: string)|
    name + ":" + id.to_s
  end

  nested_rows = [[3, [4, 5]]]
  nested = nested_rows.map do |(head: int, (left: int, right: int))|
    [head, left, right]
  end

  rest_rows = [[6, 7, 8]]
  rest = rest_rows.map do |(head: int, *tail: array<int>)|
    [head, tail[0], tail[1]]
  end

  [typed, nested[0], rest[0]]
end

def bad
  [["wrong", "Ada"]].map do |(id: int, name: string)|
    name
  end
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("Ada:1"), NewString("Lin:2")}),
		NewArray([]Value{NewInt(3), NewInt(4), NewInt(5)}),
		NewArray([]Value{NewInt(6), NewInt(7), NewInt(8)}),
	})
	requireCallErrorContains(t, script, "bad", nil, CallOptions{}, "argument id expected int, got string")
}
