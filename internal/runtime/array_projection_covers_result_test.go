package runtime

// This file guards the failure mode the other three guards cannot see.
//
// TestNewArrayCostHasOneSpelling checks that nobody writes the bad formula. The
// compile-time assertion on estimatedArrayWrapperExtraBytes checks that the
// derived value is not nonsense. The cross-compile step in CI makes that
// assertion run on the architectures the release matrix builds. None of the
// three can catch a charge that is simply absent: a projection that calls
// valueSliceBackingBytes for an array that is about to be boxed spells nothing
// wrong, computes a correct number, and compiles everywhere. It is the right
// function applied to the wrong thing, and it is how Array#zip came to allocate
// one arrayData wrapper per row against a quota that had admitted none of them.
//
// So this compares the projection against the result instead of against a rule:
// what a materializer promises before it allocates must cover what it actually
// produces. The two sides are deliberately not the same arithmetic -- the
// projection is the estimator's, the result is measured with unsafe.Sizeof --
// because a comparison against the estimator's own formula would agree with
// whatever that formula said. It fails by the size of the omission rather than
// by a threshold, and the row count multiplies it into something no noise could
// explain: 8 bytes a row, 512 across a 64-row zip.
//
// It covers the tuple family, which is the family that shares one projection
// helper. It is not a proof about the whole population, and the population is
// why: enumerating from the allocation side rather than the pricing side finds
// 181 NewArray call sites in the runtime's non-test code, 17 of them empty
// literals that allocate no backing and 43 of them inside a loop body -- the
// shape where a per-array omission stops being a constant and starts scaling
// with the input.
//
// Forty-three is too many to keep correct by inspection and it grows with every
// builtin that materializes rows, which is why the wrapper charge went into the
// pricing helpers rather than into each call site. The helpers now say which
// referent they price: arraySlotBackingBytes for an array that owns its Value,
// nestedArrayBackingBytes for one whose Value a surrounding backing already
// counts, valueSliceBackingBytes for a slice nothing ever boxes. Choosing
// between them is a judgement no guard in this package can make; this test only
// checks the answer for the family that shares a helper.

import (
	"context"
	"testing"
	"unsafe"

	"github.com/mgomes/vibescript/vibes/value"
)

// TestTupleProjectionCoversItsRows pins that the bytes a tuple materializer
// reserves for its inner rows are at least the bytes those rows turn out to
// occupy.
//
// Every one of these publishes each row as its own array value, so each row
// costs an arrayData wrapper on top of its slot backing. Pricing the rows with
// the bare-slice helper missed exactly that wrapper, once per row.
func TestTupleProjectionCoversItsRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		// rows and width describe the inner arrays the call produces, which is
		// what the projection helper is asked to price.
		rows, width int
	}{
		{
			name:   "zip",
			script: "def run(a, b)\n  a.zip(b)\nend",
			rows:   64, width: 2,
		},
		{
			name:   "product",
			script: "def run(a, b)\n  a.product(b)\nend",
			rows:   64 * 4, width: 2,
		},
		{
			name:   "combination",
			script: "def run(a, b)\n  a.combination(2)\nend",
			rows:   64 * 63 / 2, width: 2,
		},
		{
			name:   "permutation",
			script: "def run(a, b)\n  a.permutation(2)\nend",
			rows:   64 * 63, width: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			left := make([]Value, 64)
			for i := range left {
				left[i] = NewInt(int64(i))
			}
			right := make([]Value, 4)
			for i := range right {
				right[i] = NewInt(int64(i))
			}

			script := compileScriptDefault(t, tc.script)
			got, err := script.Call(context.Background(), "run",
				[]Value{NewArray(left), NewArray(right)}, CallOptions{})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			produced := got.Array()
			if len(produced) != tc.rows {
				t.Fatalf("%s produced %d rows, want %d -- the projection is being compared "+
					"against a different shape than the one it priced", tc.name, len(produced), tc.rows)
			}

			// What the materializer reserved for the rows before building them.
			projected := arrayTupleRowBackingBytes(len(produced), tc.width)

			// What the rows really occupy, taken from Go rather than from the
			// estimator: the wrapper each row allocates plus its slot backing.
			//
			// It has to come from the language and not from the estimator's own
			// arithmetic. Walking the rows with the estimator would compare a
			// projection against the formula it is built from and pass whatever
			// either says, and a first version of this test did exactly that --
			// it failed by 64 bytes a row, which was not the omission at all but
			// the elements' payloads, counted on one side and deliberately
			// excluded on the other because they are shared with the receiver.
			// unsafe.Sizeof has no opinion about any of that.
			actual := 0
			for _, row := range produced {
				actual += value.ArrayDataBytes + cap(row.Array())*int(unsafe.Sizeof(Value{}))
			}

			if projected < actual {
				t.Fatalf("%s reserved %d bytes for its %d rows and they occupy %d, short by %d "+
					"(%d per row): the preflight promised the allocation would fit and it does not, "+
					"so the rows were built against a quota that never admitted them",
					tc.name, projected, len(produced), actual, actual-projected,
					(actual-projected)/len(produced))
			}
		})
	}
}
