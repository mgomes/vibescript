package runtime

import (
	"context"
	"errors"
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

func TestParallelAssignmentLineInitialSplatAfterAssignment(t *testing.T) {
	t.Parallel()

	// A splat-assignment whose target list begins with "*" must parse as its
	// own statement even when it follows a line that could otherwise continue
	// onto a leading "*" as a multiplication. Each result was confirmed
	// against the reference Ruby implementation.
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "anonymous rest discards head",
			body:   "a = 3\n  *, last = [1, 2]",
			result: "[a, last]",
			want:   []Value{NewInt(3), NewInt(2)},
		},
		{
			name:   "named rest binds head",
			body:   "a = 3\n  *rest, last = [1, 2, 3]",
			result: "[a, rest, last]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)},
		},
		{
			name:   "named rest short array",
			body:   "a = 3\n  *rest, last = [1]",
			result: "[a, rest, last]",
			want:   []Value{NewInt(3), NewArray(nil), NewInt(1)},
		},
		{
			name:   "bare named rest",
			body:   "a = 3\n  *rest = [1, 2]",
			result: "[a, rest]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2)})},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestParallelAssignmentLineInitialSplatContinuesEqualsAcrossNewline(t *testing.T) {
	t.Parallel()

	// A splat-assignment whose target list begins with "*" and whose "=" sits
	// on the following line (Vibescript's newline-before-"=" continuation) must
	// still parse as its own destructuring statement even when it follows a line
	// that could otherwise continue onto a leading "*" as a multiplication.
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "bare named rest",
			body:   "a = 3\n  *rest\n    = [1, 2, 3]",
			result: "[a, rest]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
		},
		{
			name:   "anonymous rest discards head",
			body:   "a = 3\n  *, last\n    = [1, 2, 3]",
			result: "[a, last]",
			want:   []Value{NewInt(3), NewInt(3)},
		},
		{
			name:   "named rest binds head",
			body:   "a = 3\n  *rest, last\n    = [1, 2, 3]",
			result: "[a, rest, last]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestParallelAssignmentLineInitialSplatContinuesCommaAcrossNewline(t *testing.T) {
	t.Parallel()

	// A splat-assignment target list may be split across lines after a trailing
	// comma, just like a comma-split list with no leading "*". The split must
	// parse even when the assignment follows a line that could otherwise
	// continue onto a leading "*" as a multiplication. Each result was confirmed
	// against the reference Ruby implementation.
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "named rest before trailing target",
			body:   "a = 3\n  *rest,\n  last = [1, 2, 3]",
			result: "[a, rest, last]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)},
		},
		{
			name:   "anonymous rest before trailing target",
			body:   "a = 3\n  *,\n  last = [1, 2, 3]",
			result: "[a, last]",
			want:   []Value{NewInt(3), NewInt(3)},
		},
		{
			name:   "named rest with two trailing targets",
			body:   "a = 3\n  *mid, y,\n  z = [1, 2, 3, 4]",
			result: "[a, mid, y, z]",
			want: []Value{
				NewInt(3),
				NewArray([]Value{NewInt(1), NewInt(2)}),
				NewInt(3),
				NewInt(4),
			},
		},
		{
			name:   "nested paren sub-target spans newline",
			body:   "a = 3\n  *rest, (m,\n  n) = [1, 2, [3, 4]]",
			result: "[a, rest, m, n]",
			want: []Value{
				NewInt(3),
				NewArray([]Value{NewInt(1), NewInt(2)}),
				NewInt(3),
				NewInt(4),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestParallelAssignmentLineInitialSplatContinuesMemberAccessAcrossNewline(t *testing.T) {
	t.Parallel()

	// A destructuring target may split a member access across the newline
	// ("record\n  .field = values"), the same continuation the real target
	// parser allows for any line-limited member access. The split must parse
	// even when the assignment follows a line ("a = 3") that could otherwise
	// continue onto a leading "*" as a multiplication. Each result was confirmed
	// against the reference Ruby implementation.
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "named rest before split member target",
			body:   "*rest, b\n    .value = [1, 2, 3]",
			result: "[a, rest, b.value]",
			want:   []Value{NewInt(3), NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)},
		},
		{
			name:   "anonymous rest before split member target",
			body:   "*, b\n    .value = [1, 2, 3]",
			result: "[a, b.value]",
			want:   []Value{NewInt(3), NewInt(3)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "class Box\n  property value\nend\n" +
				"def run\n  b = Box.new\n  a = 3\n  " + tt.body +
				"\n  " + tt.result + "\nend"
			script := compileScript(t, source)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestMultiplicationContinuesAcrossNewline(t *testing.T) {
	t.Parallel()

	// A line ending in "*" and a line beginning with "*" must both continue
	// the previous expression as a multiplication; only a splat-assignment
	// target list breaks the continuation.
	tests := []struct {
		name string
		body string
		want int64
	}{
		{
			name: "trailing operator",
			body: "a = 3\n  b = 4\n  x = a *\n  b",
			want: 12,
		},
		{
			name: "leading operator",
			body: "a = 3\n  b = 4\n  x = a\n  * b",
			want: 12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  x\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if !got.Equal(NewInt(tt.want)) {
				t.Fatalf("run() = %v, want %d", got, tt.want)
			}
		})
	}
}

func TestParallelAssignmentRestTrailingTargetsBindLeftToRight(t *testing.T) {
	t.Parallel()

	// Each expectation was confirmed against the reference Ruby implementation.
	// When the input is shorter than the fixed targets, the trailing targets
	// after the rest must fill left-to-right with nil padding on the right
	// rather than binding in reverse order.
	tests := []struct {
		name       string
		assignment string
		result     string
		want       []Value
	}{
		{
			name:       "anonymous rest two trailing short by one",
			assignment: "a, *, y, z = [1, 2]",
			result:     "[a, y, z]",
			want:       []Value{NewInt(1), NewInt(2), NewNil()},
		},
		{
			name:       "anonymous rest two trailing exact",
			assignment: "a, *, y, z = [1, 2, 3]",
			result:     "[a, y, z]",
			want:       []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name:       "anonymous rest two trailing surplus",
			assignment: "a, *, y, z = [1, 2, 3, 4]",
			result:     "[a, y, z]",
			want:       []Value{NewInt(1), NewInt(3), NewInt(4)},
		},
		{
			name:       "anonymous rest two trailing very short",
			assignment: "a, *, y, z = [1]",
			result:     "[a, y, z]",
			want:       []Value{NewInt(1), NewNil(), NewNil()},
		},
		{
			name:       "named rest two trailing short by one",
			assignment: "a, *mid, y, z = [1, 2]",
			result:     "[a, mid, y, z]",
			want:       []Value{NewInt(1), NewArray(nil), NewInt(2), NewNil()},
		},
		{
			name:       "named rest two trailing exact",
			assignment: "a, *mid, y, z = [1, 2, 3]",
			result:     "[a, mid, y, z]",
			want:       []Value{NewInt(1), NewArray(nil), NewInt(2), NewInt(3)},
		},
		{
			name:       "named rest two trailing very short",
			assignment: "a, *mid, y, z = [1]",
			result:     "[a, mid, y, z]",
			want:       []Value{NewInt(1), NewArray(nil), NewNil(), NewNil()},
		},
		{
			name:       "named rest two trailing surplus",
			assignment: "a, *mid, y, z = [1, 2, 3, 4, 5]",
			result:     "[a, mid, y, z]",
			want: []Value{
				NewInt(1),
				NewArray([]Value{NewInt(2), NewInt(3)}),
				NewInt(4),
				NewInt(5),
			},
		},
		{
			name:       "leading rest two trailing short by one",
			assignment: "*, x, y = [1]",
			result:     "[x, y]",
			want:       []Value{NewInt(1), NewNil()},
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

func TestAssignDestructureAnonymousRestDoesNotCopyDiscardedSegment(t *testing.T) {
	// testing.AllocsPerRun must not run under t.Parallel(), so this test stays
	// sequential.

	// An anonymous rest target ("*, last = huge") discards its window, so the
	// interpreter must not allocate a second slice for the segment no binding
	// reads. Drive AssignDestructure directly so we can measure allocations on
	// just the destructure step, isolated from script setup.
	last := &Identifier{Name: "last"}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Rest: true},   // anonymous "*"
			{Target: last}, // trailing binding
		},
	}

	const size = 4096
	source := make([]Value, size)
	for i := range source {
		source[i] = NewInt(int64(i))
	}
	value := NewArray(source)

	var bound Value
	assign := func(expr Expression, v Value) error {
		if expr == last {
			bound = v
		}
		return nil
	}

	allocs := testing.AllocsPerRun(100, func() {
		if err := AssignDestructure(target, value, assign); err != nil {
			t.Fatalf("AssignDestructure returned error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("anonymous rest allocated %v times; expected 0 (discarded segment must not be copied)", allocs)
	}
	if bound.Kind() != KindInt || bound.Int() != size-1 {
		t.Fatalf("trailing target bound to %v; want %d", bound, size-1)
	}
}

func TestAssignDestructureNamedRestCopiesWindow(t *testing.T) {
	t.Parallel()

	// A named rest target must still receive a fresh array of its window, so the
	// optimization for anonymous rest does not leak into the named path.
	mid := &Identifier{Name: "mid"}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "first"}},
			{Target: mid, Rest: true},
			{Target: &Identifier{Name: "last"}},
		},
	}

	value := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})

	var bound Value
	assign := func(expr Expression, v Value) error {
		if expr == mid {
			bound = v
		}
		return nil
	}
	if err := AssignDestructure(target, value, assign); err != nil {
		t.Fatalf("AssignDestructure returned error: %v", err)
	}
	compareArrays(t, bound, []Value{NewInt(2), NewInt(3)})
}

func TestParallelAssignmentSnapshotsSourceBeforeLHSWrites(t *testing.T) {
	t.Parallel()

	// Ruby evaluates the right-hand side into an array before performing any
	// assignment, so a target that writes back into the source array must not be
	// visible to later reads. Each expectation was confirmed against the
	// reference Ruby implementation (e.g. `v = [1,2,3]; v[1], *rest = v` yields
	// rest == [2, 3], the original snapshot, not [1, 3] from the mutated array).
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "named rest after index write",
			body:   "v = [1, 2, 3]\n  v[1], *rest = v",
			result: "[v, rest]",
			want: []Value{
				NewArray([]Value{NewInt(1), NewInt(1), NewInt(3)}),
				NewArray([]Value{NewInt(2), NewInt(3)}),
			},
		},
		{
			name:   "fixed target after index write",
			body:   "v = [1, 2, 3]\n  v[1], y = v",
			result: "[v, y]",
			want: []Value{
				NewArray([]Value{NewInt(1), NewInt(1), NewInt(3)}),
				NewInt(2),
			},
		},
		{
			name:   "trailing fixed target after index write",
			body:   "v = [10, 20, 30]\n  v[2], *r = v",
			result: "[v, r]",
			want: []Value{
				NewArray([]Value{NewInt(10), NewInt(20), NewInt(10)}),
				NewArray([]Value{NewInt(20), NewInt(30)}),
			},
		},
		{
			name:   "leading rest then index write of trailing slot",
			body:   "v = [1, 2, 3, 4]\n  *rest, v[0], last = v",
			result: "[v, rest, last]",
			want: []Value{
				NewArray([]Value{NewInt(3), NewInt(2), NewInt(3), NewInt(4)}),
				NewArray([]Value{NewInt(1), NewInt(2)}),
				NewInt(4),
			},
		},
		{
			name:   "anonymous rest with index write of trailing",
			body:   "v = [5, 6, 7, 8]\n  v[3], *, last = v",
			result: "[v, last]",
			want: []Value{
				NewArray([]Value{NewInt(5), NewInt(6), NewInt(7), NewInt(5)}),
				NewInt(8),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestAssignDestructureSnapshotsSourceForWritingTargets(t *testing.T) {
	t.Parallel()

	// Drive AssignDestructure directly to prove the snapshot guards against an
	// index target that mutates the very array supplied as the value. The rest
	// binding must capture the original values, not the mutated source.
	source := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	index := &IndexExpr{}
	rest := &Identifier{Name: "rest"}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: index},
			{Target: rest, Rest: true},
		},
	}

	var bound Value
	assign := func(expr Expression, v Value) error {
		switch expr {
		case index:
			// Simulate "source[0] = v" by mutating the live backing array.
			source.Array()[0] = v
		case rest:
			bound = v
		}
		return nil
	}
	if err := AssignDestructure(target, source, assign); err != nil {
		t.Fatalf("AssignDestructure returned error: %v", err)
	}
	compareArrays(t, bound, []Value{NewInt(2), NewInt(3)})
}

func TestAssignDestructureSkipsSnapshotWhenWriteHasNoReadBack(t *testing.T) {
	// testing.AllocsPerRun must not run under t.Parallel(), so this test stays
	// sequential.

	// "values[0], * = values" writes the first slot and then discards the rest.
	// No surviving target reads the array after the write, so the source must be
	// aliased rather than snapshotted: the whole backing slice would otherwise be
	// copied for a mutation no binding can observe. The write is idempotent
	// (source[0] receives its own original value) so repeated AllocsPerRun
	// iterations stay stable.
	index := &IndexExpr{}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: index}, // "values[0]"
			{Rest: true},    // anonymous "*"
		},
	}

	const size = 4096
	backing := make([]Value, size)
	for i := range backing {
		backing[i] = NewInt(int64(i))
	}
	source := NewArray(backing)

	assign := func(expr Expression, v Value) error {
		if expr == index {
			source.Array()[0] = v
		}
		return nil
	}

	allocs := testing.AllocsPerRun(100, func() {
		if err := AssignDestructure(target, source, assign); err != nil {
			t.Fatalf("AssignDestructure returned error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("write-then-discard allocated %v times; expected 0 (no read-back means no snapshot)", allocs)
	}
	if got := source.Array()[0]; got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("source[0] = %v; want 0 (idempotent self-write)", got)
	}
}

func TestAssignDestructureSkipsSnapshotWhenFollowerOnlyDiscards(t *testing.T) {
	// testing.AllocsPerRun must not run under t.Parallel(), so this test stays
	// sequential.

	// "values[0], (*) = values" writes the first slot and then follows it with a
	// nested destructure pattern that only discards. No binding in that follower
	// reads the array, so it must be treated like a bare anonymous rest: the
	// source aliases instead of taking a full snapshot. The recursion in
	// destructureElementReads is what makes the all-discard nested pattern count
	// as a non-read; without it the whole backing slice would be copied for a
	// mutation no binding can observe.
	index := &IndexExpr{}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: index}, // "values[0]"
			{Target: &DestructureTarget{ // nested "(*)"
				Elements: []DestructureElement{{Rest: true}},
			}},
		},
	}

	const size = 4096
	backing := make([]Value, size)
	for i := range backing {
		backing[i] = NewInt(int64(i))
	}
	source := NewArray(backing)

	assign := func(expr Expression, v Value) error {
		if expr == index {
			source.Array()[0] = v
		}
		return nil
	}

	allocs := testing.AllocsPerRun(100, func() {
		if err := AssignDestructure(target, source, assign); err != nil {
			t.Fatalf("AssignDestructure returned error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("write-then-nested-discard allocated %v times; expected 0 (all-discard follower means no snapshot)", allocs)
	}
	if got := source.Array()[0]; got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("source[0] = %v; want 0 (idempotent self-write)", got)
	}
}

func TestAssignDestructureChargesSnapshotAgainstMemoryQuota(t *testing.T) {
	t.Parallel()

	// "values[1], *rest = values" must snapshot the source so rest reads the
	// original values, not the mutated array, and then materialize the rest
	// window as a second fresh slot array that coexists with that snapshot. Both
	// are allocations the post-assignment memory check would miss (the snapshot
	// is gone, and nothing re-checks the rest window before the next statement),
	// so assignDestructure charges each up front and fails fast. The true peak is
	// base + snapshot + rest, not base + snapshot alone.
	const size = 1024
	index := &IndexExpr{}
	rest := &Identifier{Name: "rest"}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: index}, // "values[1]"
			{Target: rest, Rest: true},
		},
	}

	// Each run mutates source[1] through the index write, so build a fresh source
	// per quota; sharing one across runs would let an earlier write corrupt the
	// original values a later run's snapshot is meant to preserve. bound captures
	// the rest target so the accept case can prove the snapshot was taken.
	var bound Value
	run := func(quota int) error {
		bound = NewNil()
		backing := make([]Value, size)
		for i := range backing {
			backing[i] = NewInt(int64(i))
		}
		source := NewArray(backing)
		assign := func(expr Expression, v Value) error {
			switch expr {
			case index:
				source.Array()[1] = v
			case rest:
				bound = v
			}
			return nil
		}
		exec := &Execution{ctx: context.Background(), memoryQuota: quota}
		return exec.assignDestructure(target, source, assign)
	}

	probe := &Execution{ctx: context.Background()}
	base := probe.estimateMemoryUsageBase(probe.memoryEstimatorForCheck())
	snapshotBytes := estimatedValueBytes + estimatedSliceBaseBytes + size*estimatedValueBytes
	// "values[1], *rest" leaves restStart=1 and restEnd=size, so the captured
	// window is size-1 slots. It is charged on top of the still-live snapshot.
	restBytes := estimatedValueBytes + estimatedSliceBaseBytes + (size-1)*estimatedValueBytes
	peakBytes := snapshotBytes + restBytes

	// One byte short of the snapshot's footprint: the source aliases fine, but the
	// snapshot copy pushes the projection over the quota.
	if err := run(base + snapshotBytes - 1); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("assignDestructure under tight quota = %v; want errMemoryQuotaExceeded", err)
	}

	// Room for the snapshot but not the rest window on top of it. This is the gap
	// the finding flagged: the snapshot check passes, but materializing the rest
	// array would push live memory past the quota, so it must reject before the
	// window is allocated rather than overshooting and discovering it too late.
	if err := run(base + peakBytes - 1); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("assignDestructure with room for the snapshot but not the rest window = %v; want errMemoryQuotaExceeded", err)
	}

	// Exactly enough room for the snapshot and the rest window together: the
	// assignment succeeds and rest captures the original slot values, proving the
	// snapshot was taken before the index write mutated source[1].
	if err := run(base + peakBytes); err != nil {
		t.Fatalf("assignDestructure with room for the snapshot and rest window returned error: %v", err)
	}
	if bound.Kind() != KindArray || len(bound.Array()) != size-1 {
		t.Fatalf("rest bound to %v; want a %d-element array", bound, size-1)
	}
	if first := bound.Array()[0]; first.Kind() != KindInt || first.Int() != 1 {
		t.Fatalf("rest[0] = %v; want 1 (original source[1], not the mutated value)", first)
	}
}

func TestAssignDestructureChargesRestWindowWithoutSnapshot(t *testing.T) {
	t.Parallel()

	// "a, *rest = values" only binds names, so destructureWriteIsReadBack is
	// false and the source aliases without a snapshot. The named rest window is
	// still a fresh slot array, and nothing re-checks it before the next
	// statement, so assignDestructure must charge it up front. A quota that fits
	// the aliased source but not the rest window must reject before the window is
	// allocated.
	const size = 1024
	backing := make([]Value, size)
	for i := range backing {
		backing[i] = NewInt(int64(i))
	}
	source := NewArray(backing)

	first := &Identifier{Name: "a"}
	rest := &Identifier{Name: "rest"}
	target := &DestructureTarget{
		Elements: []DestructureElement{
			{Target: first},
			{Target: rest, Rest: true},
		},
	}
	assign := func(Expression, Value) error { return nil }

	probe := &Execution{ctx: context.Background()}
	base := probe.estimateMemoryUsageBase(probe.memoryEstimatorForCheck())
	// "a, *rest" leaves restStart=1 and restEnd=size, so the window is size-1
	// slots. No snapshot is taken, so the source aliases the live RHS already
	// counted in base; only the rest window is charged on top.
	restBytes := estimatedValueBytes + estimatedSliceBaseBytes + (size-1)*estimatedValueBytes

	reject := &Execution{ctx: context.Background(), memoryQuota: base + restBytes - 1}
	if err := reject.assignDestructure(target, source, assign); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("assignDestructure under tight quota = %v; want errMemoryQuotaExceeded", err)
	}

	accept := &Execution{ctx: context.Background(), memoryQuota: base + restBytes}
	if err := accept.assignDestructure(target, source, assign); err != nil {
		t.Fatalf("assignDestructure with room for the rest window returned error: %v", err)
	}
}

func TestParallelAssignmentDiscardedRestAfterIndexWrite(t *testing.T) {
	t.Parallel()

	// A trailing anonymous rest after a writing index target discards everything
	// the write could touch, so the result must match Ruby without taking a
	// snapshot. Confirmed against the reference Ruby implementation:
	//   v=[10,20,30,40]; v[1], * = v  -> v == [10, 10, 30, 40]
	//   v=[10,20,30,40]; v[0], * = v  -> v == [10, 20, 30, 40]
	tests := []struct {
		name string
		body string
		want []Value
	}{
		{
			name: "write middle slot then discard",
			body: "v = [10, 20, 30, 40]\n  v[1], * = v",
			want: []Value{NewInt(10), NewInt(10), NewInt(30), NewInt(40)},
		},
		{
			name: "idempotent first-slot write then discard",
			body: "v = [10, 20, 30, 40]\n  v[0], * = v",
			want: []Value{NewInt(10), NewInt(20), NewInt(30), NewInt(40)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  v\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
}

func TestParallelAssignmentNestedDiscardFollowerAfterIndexWrite(t *testing.T) {
	t.Parallel()

	// A nested destructure follower that only discards, "(*)", reads nothing,
	// so a preceding index write has no surviving observer and the source can be
	// aliased without a snapshot. A nested follower that binds a value, "(b, *)",
	// must still snapshot so its read sees the original element, not the mutated
	// slot. Each expectation was confirmed against the reference Ruby
	// implementation:
	//   v=[10,20,30,40]; v[0], (*) = v     -> v == [10, 20, 30, 40]
	//   v=[10,20,30,40]; v[1], (*) = v     -> v == [10, 10, 30, 40]
	//   v=[10,20,30,40]; v[1], (b, *) = v  -> v == [10, 10, 30, 40], b == 20
	tests := []struct {
		name   string
		body   string
		result string
		want   []Value
	}{
		{
			name:   "nested discard follower idempotent write",
			body:   "v = [10, 20, 30, 40]\n  v[0], (*) = v",
			result: "v",
			want:   []Value{NewInt(10), NewInt(20), NewInt(30), NewInt(40)},
		},
		{
			name:   "nested discard follower middle write",
			body:   "v = [10, 20, 30, 40]\n  v[1], (*) = v",
			result: "v",
			want:   []Value{NewInt(10), NewInt(10), NewInt(30), NewInt(40)},
		},
		{
			name:   "nested reading follower snapshots before write",
			body:   "v = [10, 20, 30, 40]\n  v[1], (b, *) = v",
			result: "[v, b]",
			want: []Value{
				NewArray([]Value{NewInt(10), NewInt(10), NewInt(30), NewInt(40)}),
				NewInt(20),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := compileScript(t, "def run\n  "+tt.body+"\n  "+tt.result+"\nend")
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, tt.want)
		})
	}
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
