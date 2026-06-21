package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestArrayExtremaHappyPaths(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def extrema()
      ints = [3, 1, 2]
      floats = [2.5, -1.0, 0.0]
      words = ["aaa", "b", "cc"]

      {
        min_int: ints.min,
        max_int: ints.max,
        minmax_int: ints.minmax,
        min_float: floats.min,
        max_float: floats.max,
        min_string: words.min,
        max_string: words.max,
        min_by: words.min_by { |s| s.length },
        max_by: words.max_by { |s| s.length },
        minmax_string: words.minmax
      }
    end
    `)

	got := callFunc(t, script, "extrema", nil).Hash()

	if !got["min_int"].Equal(NewInt(1)) || !got["max_int"].Equal(NewInt(3)) {
		t.Fatalf("int extrema mismatch: min=%v max=%v", got["min_int"], got["max_int"])
	}
	compareArrays(t, got["minmax_int"], []Value{NewInt(1), NewInt(3)})
	if !got["min_float"].Equal(NewFloat(-1.0)) || !got["max_float"].Equal(NewFloat(2.5)) {
		t.Fatalf("float extrema mismatch: min=%v max=%v", got["min_float"], got["max_float"])
	}
	if !got["min_string"].Equal(NewString("aaa")) || !got["max_string"].Equal(NewString("cc")) {
		t.Fatalf("string extrema mismatch: min=%v max=%v", got["min_string"], got["max_string"])
	}
	if !got["min_by"].Equal(NewString("b")) || !got["max_by"].Equal(NewString("aaa")) {
		t.Fatalf("min_by/max_by mismatch: min=%v max=%v", got["min_by"], got["max_by"])
	}
	compareArrays(t, got["minmax_string"], []Value{NewString("aaa"), NewString("cc")})
}

func TestArrayExtremaEmpty(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def empties()
      {
        min: [].min,
        max: [].max,
        minmax: [].minmax,
        min_by: [].min_by { |v| v },
        max_by: [].max_by { |v| v }
      }
    end
    `)

	got := callFunc(t, script, "empties", nil).Hash()

	for _, name := range []string{"min", "max", "min_by", "max_by"} {
		if got[name].Kind() != KindNil {
			t.Fatalf("%s on empty array = %v, want nil", name, got[name])
		}
	}
	compareArrays(t, got["minmax"], []Value{NewNil(), NewNil()})
}

func TestArrayExtremaTiesKeepFirstElement(t *testing.T) {
	t.Parallel()
	// Block keys collide so the first element with the extreme key must win,
	// matching Ruby's Enumerable#min_by/#max_by tie behavior. The hashes are
	// distinguishable by their :id so we can observe which element was kept.
	script := compileScript(t, `
    def ties()
      rows = [
        { id: 1, len: 2 },
        { id: 2, len: 1 },
        { id: 3, len: 2 },
        { id: 4, len: 1 }
      ]

      {
        min_by: rows.min_by { |r| r[:len] },
        max_by: rows.max_by { |r| r[:len] }
      }
    end
    `)

	got := callFunc(t, script, "ties", nil).Hash()

	minRow := got["min_by"].Hash()
	if !minRow["id"].Equal(NewInt(2)) {
		t.Fatalf("min_by tie kept id=%v, want first matching id=2", minRow["id"])
	}
	maxRow := got["max_by"].Hash()
	if !maxRow["id"].Equal(NewInt(1)) {
		t.Fatalf("max_by tie kept id=%v, want first matching id=1", maxRow["id"])
	}
}

func TestArrayExtremaSingleElement(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def singletons()
      {
        min: [7].min,
        max: [7].max,
        minmax: [7].minmax,
        min_by: [7].min_by { |v| v },
        max_by: [7].max_by { |v| v }
      }
    end
    `)

	got := callFunc(t, script, "singletons", nil).Hash()

	for _, name := range []string{"min", "max", "min_by", "max_by"} {
		if !got[name].Equal(NewInt(7)) {
			t.Fatalf("%s on single-element array = %v, want 7", name, got[name])
		}
	}
	compareArrays(t, got["minmax"], []Value{NewInt(7), NewInt(7)})
}

func TestArrayExtremaRejectsIncomparableValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def bad_min()
      [1, "a"].min
    end

    def bad_max()
      [1, "a"].max
    end

    def bad_minmax()
      [1, "a"].minmax
    end

    def bad_min_by()
      [1, "a"].min_by { |v| v }
    end

    def bad_max_by()
      [1, "a"].max_by { |v| v }
    end
    `)

	requireCallErrorContains(t, script, "bad_min", nil, CallOptions{}, "array.min values are not comparable")
	requireCallErrorContains(t, script, "bad_max", nil, CallOptions{}, "array.max values are not comparable")
	requireCallErrorContains(t, script, "bad_minmax", nil, CallOptions{}, "array.minmax values are not comparable")
	requireCallErrorContains(t, script, "bad_min_by", nil, CallOptions{}, "array.min_by block values are not comparable")
	requireCallErrorContains(t, script, "bad_max_by", nil, CallOptions{}, "array.max_by block values are not comparable")
}

func TestArrayExtremaRejectArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def min_args()
      [1, 2].min(1)
    end

    def max_args()
      [1, 2].max(1)
    end

    def minmax_args()
      [1, 2].minmax(1)
    end

    def min_by_args()
      [1, 2].min_by(1) { |v| v }
    end

    def max_by_args()
      [1, 2].max_by(1) { |v| v }
    end
    `)

	requireCallErrorContains(t, script, "min_args", nil, CallOptions{}, "array.min does not take arguments")
	requireCallErrorContains(t, script, "max_args", nil, CallOptions{}, "array.max does not take arguments")
	requireCallErrorContains(t, script, "minmax_args", nil, CallOptions{}, "array.minmax does not take arguments")
	requireCallErrorContains(t, script, "min_by_args", nil, CallOptions{}, "array.min_by does not take arguments")
	requireCallErrorContains(t, script, "max_by_args", nil, CallOptions{}, "array.max_by does not take arguments")
}

func TestArrayExtremaByRequiresBlock(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def min_by_no_block()
      [1, 2].min_by
    end

    def max_by_no_block()
      [1, 2].max_by
    end
    `)

	requireCallErrorContains(t, script, "min_by_no_block", nil, CallOptions{}, "array.min_by requires a block")
	requireCallErrorContains(t, script, "max_by_no_block", nil, CallOptions{}, "array.max_by requires a block")
}

func TestArrayExtremaByPropagatesBlockErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def min_by_raises()
      [1, 2, 3].min_by { |v| raise "boom" }
    end

    def max_by_raises()
      [1, 2, 3].max_by { |v| raise "boom" }
    end
    `)

	requireCallErrorContains(t, script, "min_by_raises", nil, CallOptions{}, "boom")
	requireCallErrorContains(t, script, "max_by_raises", nil, CallOptions{}, "boom")
}

func largeIntArray(n int) Value {
	values := make([]Value, n)
	for i := range n {
		values[i] = NewInt(int64(i))
	}
	return NewArray(values)
}

func TestArrayExtremaByParticipateInStepQuota(t *testing.T) {
	t.Parallel()
	// A tight step quota must trip while the block forms walk a large array,
	// proving each block invocation accounts for steps like sort_by/map.
	script := compileScriptWithConfig(t, Config{StepQuota: 40}, `
    def run(values)
      values.min_by { |v| v }
    end
    `)

	requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
}

func TestArrayExtremaByHonorCancellation(t *testing.T) {
	t.Parallel()
	// A canceled context must abort the block walk: step() polls cancellation on
	// its first invocation, so even a tiny array is enough to observe it.
	script := compileScript(t, `
    def run()
      [3, 1, 2].max_by { |v| v }
    end
    `)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("max_by under canceled context = %v, want context.Canceled", err)
	}
}
