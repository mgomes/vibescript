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

func TestParallelAssignmentAnonymousRestTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		assignment string
		result     string
		want       []Value
	}{
		{
			name:       "trailing discards tail",
			assignment: "first, * = [1, 2, 3]",
			result:     "[first]",
			want:       []Value{NewInt(1)},
		},
		{
			name:       "leading discards head",
			assignment: "*, last = [1, 2, 3]",
			result:     "[last]",
			want:       []Value{NewInt(3)},
		},
		{
			name:       "middle discards interior",
			assignment: "first, *, last = [1, 2, 3, 4]",
			result:     "[first, last]",
			want:       []Value{NewInt(1), NewInt(4)},
		},
		{
			name:       "middle with short array",
			assignment: "first, *, last = [1]",
			result:     "[first, last]",
			want:       []Value{NewInt(1), NewNil()},
		},
		{
			name:       "trailing with empty array",
			assignment: "first, * = []",
			result:     "[first]",
			want:       []Value{NewNil()},
		},
		{
			name:       "scalar right-hand side",
			assignment: "first, * = 9",
			result:     "[first]",
			want:       []Value{NewInt(9)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.assignment+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestParallelAssignmentNamedRestHandlesShortArrays(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  first, *rest = []
  a, *middle, last = [1]
  [first, rest, a, middle, last]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewNil(),
		NewArray(nil),
		NewInt(1),
		NewArray(nil),
		NewNil(),
	})
}

func TestParallelAssignmentNestedAnonymousRestTarget(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  a, (b, *, c), d = [1, [2, 3, 4, 5], 6]
  [a, b, c, d]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(5), NewInt(6)})
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
