package runtime

import (
	"testing"
)

func TestArrayTakeAndDrop(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def take_n(values, n)
      values.take(n)
    end

    def drop_n(values, n)
      values.drop(n)
    end
    `)

	base := []Value{NewInt(1), NewInt(2), NewInt(3)}

	cases := []struct {
		name string
		fn   string
		n    int64
		want []Value
	}{
		{name: "take prefix", fn: "take_n", n: 2, want: []Value{NewInt(1), NewInt(2)}},
		{name: "take zero", fn: "take_n", n: 0, want: []Value{}},
		{name: "take beyond length", fn: "take_n", n: 5, want: []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{name: "drop suffix", fn: "drop_n", n: 1, want: []Value{NewInt(2), NewInt(3)}},
		{name: "drop zero", fn: "drop_n", n: 0, want: []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{name: "drop beyond length", fn: "drop_n", n: 5, want: []Value{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{NewArray(base), NewInt(tc.n)})
			compareArrays(t, got, tc.want)
		})
	}
}

func TestArrayTakeAndDropTruncateFloatCounts(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def take_n(values, n)
      values.take(n)
    end

    def drop_n(values, n)
      values.drop(n)
    end
    `)

	base := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	// Ruby converts the count via to_int, truncating fractional values.
	compareArrays(t, callFunc(t, script, "take_n", []Value{base, NewFloat(2.9)}), []Value{NewInt(1), NewInt(2)})
	compareArrays(t, callFunc(t, script, "drop_n", []Value{base, NewFloat(1.9)}), []Value{NewInt(2), NewInt(3)})
}

func TestArrayTakeAndDropErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def take_neg(values)
      values.take(-1)
    end

    def drop_neg(values)
      values.drop(-1)
    end

    def take_none(values)
      values.take
    end

    def drop_extra(values)
      values.drop(1, 2)
    end

    def take_bad_type(values)
      values.take("nope")
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
	requireCallErrorContains(t, script, "take_neg", base, CallOptions{}, "array.take attempted with negative size")
	requireCallErrorContains(t, script, "drop_neg", base, CallOptions{}, "array.drop attempted with negative size")
	requireCallErrorContains(t, script, "take_none", base, CallOptions{}, "array.take expects exactly one count")
	requireCallErrorContains(t, script, "drop_extra", base, CallOptions{}, "array.drop expects exactly one count")
	requireCallErrorContains(t, script, "take_bad_type", base, CallOptions{}, "array.take count must be integer")
}

func TestArrayZip(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def zip_two(a, b, c)
      a.zip(b, c)
    end

    def zip_none(a)
      a.zip
    end

    def zip_longer(a, b)
      a.zip(b)
    end
    `)

	t.Run("pads uneven lengths with nil", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "zip_two", []Value{
			NewArray([]Value{NewInt(1), NewInt(2)}),
			NewArray([]Value{NewInt(3), NewInt(4)}),
			NewArray([]Value{NewInt(5)}),
		})
		want := []Value{
			NewArray([]Value{NewInt(1), NewInt(3), NewInt(5)}),
			NewArray([]Value{NewInt(2), NewInt(4), NewNil()}),
		}
		compareArrays(t, got, want)
	})

	t.Run("no arguments wraps each element", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "zip_none", []Value{NewArray([]Value{NewInt(1), NewInt(2)})})
		want := []Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(2)}),
		}
		compareArrays(t, got, want)
	})

	t.Run("result length follows receiver", func(t *testing.T) {
		t.Parallel()
		// Extra elements in the argument array are discarded; Ruby keys the
		// row count to the receiver's length.
		got := callFunc(t, script, "zip_longer", []Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(2), NewInt(3)}),
		})
		want := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
		compareArrays(t, got, want)
	})
}

func TestArrayZipRejectsNonArrayArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def zip_scalar(a)
      a.zip(3)
    end
    `)

	requireCallErrorContains(t, script, "zip_scalar", []Value{NewArray([]Value{NewInt(1)})}, CallOptions{}, "array.zip arguments must be arrays")
}

func TestHashValuesAt(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def lookup(record)
      record.values_at(:b, :c, :a)
    end

    def lookup_empty(record)
      record.values_at
    end

    def lookup_string(record)
      record.values_at("a")
    end
    `)

	record := NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(2),
	})

	t.Run("returns values in key order with nil for misses", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup", []Value{record})
		want := []Value{NewInt(2), NewNil(), NewInt(1)}
		compareArrays(t, got, want)
	})

	t.Run("no keys returns empty array", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_empty", []Value{record})
		compareArrays(t, got, []Value{})
	})

	t.Run("string and symbol keys are interchangeable", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_string", []Value{record})
		compareArrays(t, got, []Value{NewInt(1)})
	})
}

func TestHashValuesAtRejectsNonKeyArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def lookup(record)
      record.values_at(1)
    end
    `)

	record := NewHash(map[string]Value{"a": NewInt(1)})
	requireCallErrorContains(t, script, "lookup", []Value{record}, CallOptions{}, "hash.values_at keys must be symbol or string")
}

func TestCollectionHelpersPreserveReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def take_keeps_source(values)
      taken = values.take(1)
      { taken: taken, source: values }
    end

    def zip_keeps_source(values, other)
      zipped = values.zip(other)
      { zipped: zipped, source: values }
    end
    `)

	t.Run("take does not mutate receiver", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "take_keeps_source", []Value{NewArray([]Value{NewInt(1), NewInt(2)})}).Hash()
		compareArrays(t, result["taken"], []Value{NewInt(1)})
		compareArrays(t, result["source"], []Value{NewInt(1), NewInt(2)})
	})

	t.Run("zip does not mutate receiver", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "zip_keeps_source", []Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(2)}),
		}).Hash()
		compareArrays(t, result["zipped"], []Value{NewArray([]Value{NewInt(1), NewInt(2)})})
		compareArrays(t, result["source"], []Value{NewInt(1)})
	})
}
