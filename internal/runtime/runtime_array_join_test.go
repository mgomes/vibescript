package runtime

import (
	"strings"
	"testing"
)

// TestArrayJoin exercises Ruby's Array#join, which recursively joins nested
// arrays with the active separator and renders nil as an empty segment.
func TestArrayJoin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def default_sep()
      ["a", "b"].join
    end

    def custom_sep()
      ["a", "b", "c"].join("-")
    end

    def scalar_nil()
      [1, nil, "x"].join(",")
    end

    def nested()
      [1, [2, 3], 4].join("-")
    end

    def deep()
      [1, [2, [3, 4]], 5].join("-")
    end

    def empty_nested()
      [1, [], 2].join("-")
    end

    def pairs()
      [[1, 2], [3, 4]].join("-")
    end

    def nested_nil()
      [1, [nil, 2], 3].join("-")
    end

    def empty_array()
      [].join(",")
    end

    def mixed_types()
      [1, 2.5, true, "x"].join("|")
    end
    `)

	cases := []struct {
		name string
		fn   string
		want string
	}{
		{name: "default separator", fn: "default_sep", want: "ab"},
		{name: "custom separator", fn: "custom_sep", want: "a-b-c"},
		{name: "scalar nil renders empty segment", fn: "scalar_nil", want: "1,,x"},
		{name: "nested array joined recursively", fn: "nested", want: "1-2-3-4"},
		{name: "deeply nested array joined recursively", fn: "deep", want: "1-2-3-4-5"},
		{name: "empty nested array contributes empty segment", fn: "empty_nested", want: "1--2"},
		{name: "array of pairs joined recursively", fn: "pairs", want: "1-2-3-4"},
		{name: "nil inside nested array renders empty segment", fn: "nested_nil", want: "1--2-3"},
		{name: "empty array joins to empty string", fn: "empty_array", want: ""},
		{name: "mixed scalar types use string form", fn: "mixed_types", want: "1|2.5|true|x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if got.Kind() != KindString {
				t.Fatalf("join %s kind = %v, want string", tc.fn, got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("join %s = %q, want %q", tc.fn, got.String(), tc.want)
			}
		})
	}
}

// TestArrayJoinSeparatorDiagnostics verifies the separator argument is validated
// the same way it was before recursion was added: only a single string is
// accepted.
func TestArrayJoinSeparatorDiagnostics(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def non_string_sep()
      [1, 2].join(3)
    end

    def too_many_args()
      [1, 2].join(",", ",")
    end
    `)

	requireCallErrorContains(t, script, "non_string_sep", nil, CallOptions{}, "array.join separator must be string")
	requireCallErrorContains(t, script, "too_many_args", nil, CallOptions{}, "array.join accepts at most one separator")
}

// TestArrayJoinGuards covers the cycle and depth protections that bound the
// recursion. Scripts cannot build cyclic or 1024-deep arrays, so the guards are
// exercised by calling arrayJoin directly with constructed structures, mirroring
// how flattenValues guards recursion.
func TestArrayJoinGuards(t *testing.T) {
	t.Parallel()

	t.Run("cyclic structure is rejected", func(t *testing.T) {
		t.Parallel()
		// Build a slice that contains a Value wrapping itself, so the cycle guard
		// observes the same slice identity on the recursive descent.
		cyclic := make([]Value, 1)
		cyclic[0] = NewArray(cyclic)

		var b strings.Builder
		err := arrayJoin(&b, cyclic, "-")
		if err == nil {
			t.Fatal("arrayJoin on cyclic structure: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "array.join does not support cyclic structures") {
			t.Fatalf("arrayJoin cyclic error = %q, want it to mention cyclic structures", err.Error())
		}
	})

	t.Run("excessively deep nesting is rejected", func(t *testing.T) {
		t.Parallel()
		// Nest deeper than maxFlattenDepth so the depth guard trips before the
		// goroutine stack can overflow.
		deep := []Value{NewInt(1)}
		for range maxFlattenDepth + 1 {
			deep = []Value{NewArray(deep)}
		}

		var b strings.Builder
		err := arrayJoin(&b, deep, "-")
		if err == nil {
			t.Fatal("arrayJoin on deep structure: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "array.join exceeded maximum depth") {
			t.Fatalf("arrayJoin deep error = %q, want it to mention maximum depth", err.Error())
		}
	})
}
