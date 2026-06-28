package runtime

import (
	"context"
	"testing"
)

func TestComparableClampForms(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def numeric
  [
    5.clamp(1..3),
    5.clamp(0, nil),
    5.clamp(nil, 3),
    5.clamp(5.5, 10),
    2.5.clamp(1, 2),
    2.5.clamp(1..3)
  ]
end

def strings
  [
    "z".clamp("a", "f"),
    "b".clamp("a", nil),
    "0".clamp("a", "f")
  ]
end

def exclusive_range
  5.clamp(1...3)
end

def inverted
  5.clamp(3, 1)
end`)

	got := callScript(t, context.Background(), script, "numeric", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewInt(3),
		NewInt(5),
		NewInt(3),
		NewFloat(5.5),
		NewInt(2),
		NewFloat(2.5),
	})

	got = callScript(t, context.Background(), script, "strings", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewString("f"),
		NewString("b"),
		NewString("a"),
	})

	requireCallErrorContains(t, script, "exclusive_range", nil, CallOptions{}, "cannot clamp with exclusive range")
	requireCallErrorContains(t, script, "inverted", nil, CallOptions{}, "min must be <= max")
}

func TestNumericClampHandlesInfinities(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  pos = 1.0 / 0.0
  neg = -1.0 / 0.0
  [
    pos.clamp(0, 10),
    neg.clamp(0, 10),
    5.clamp(neg, pos)
  ]
end

def nan
  (0.0 / 0.0).clamp(0, 10)
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewInt(10),
		NewInt(0),
		NewInt(5),
	})

	requireCallErrorContains(t, script, "nan", nil, CallOptions{}, "must not be NaN")
}
