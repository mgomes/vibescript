package runtime

import "testing"

// TestArrayFindIfnoneFallback covers the Ruby-aligned optional ifnone
// fallback of array.find: the callable runs only when no element matches,
// matches ignore it entirely, and the no-argument miss stays nil.
func TestArrayFindIfnoneFallback(t *testing.T) {
	t.Parallel()
	// A function with a default parameter is the natural zero-arity callable a
	// script can pass as a value: it is not auto-invoked at reference time
	// (it has parameters) and supplies its default when called with no args.
	script := compileScript(t, `
    def ifnone(reason = :none)
      reason
    end

    def miss_with_fallback(values)
      values.find(ifnone) do |v|
        v > 100
      end
    end

    def hit_ignores_fallback(values)
      values.find(ifnone) do |v|
        v == 2
      end
    end

    def miss_without_fallback(values)
      values.find do |v|
        v > 100
      end
    end
    `)

	values := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "miss calls fallback", fn: "miss_with_fallback", want: NewSymbol("none")},
		{name: "hit ignores fallback", fn: "hit_ignores_fallback", want: NewInt(2)},
		{name: "miss without fallback stays nil", fn: "miss_without_fallback", want: NewNil()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{values})
			if !got.Equal(tc.want) {
				t.Fatalf("%s: got %v, want %v", tc.fn, got, tc.want)
			}
		})
	}
}

// TestArrayFindIfnoneFallbackReturnsComputedValue verifies the fallback's
// return value flows through unchanged, including a composite value built
// inside the callable, mirroring Ruby returning whatever the ifnone Proc
// produces.
func TestArrayFindIfnoneFallbackReturnsComputedValue(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def ifnone(items = [])
      items.push(:missing)
    end

    def find_or_build(values)
      values.find(ifnone) do |v|
        v > 100
      end
    end
    `)

	values := NewArray([]Value{NewInt(1), NewInt(2)})
	got := callFunc(t, script, "find_or_build", []Value{values})
	want := NewArray([]Value{NewSymbol("missing")})
	if !got.Equal(want) {
		t.Fatalf("computed fallback: got %v, want %v", got, want)
	}
}

// TestArrayFindIfnoneFallbackErrors covers misuse: a non-callable fallback and
// passing more than one argument.
func TestArrayFindIfnoneFallbackErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		fn     string
		args   []Value
		want   string
	}{
		{
			name: "non-callable fallback",
			source: `
        def find_with_string(values)
          values.find("none") do |v|
            v > 100
          end
        end
      `,
			fn:   "find_with_string",
			args: []Value{NewArray([]Value{NewInt(1)})},
			want: "expected a callable value, got string",
		},
		{
			name: "too many arguments",
			source: `
        def ifnone
          :none
        end

        def find_with_extra(values)
          values.find(ifnone, ifnone) do |v|
            v > 100
          end
        end
      `,
			fn:   "find_with_extra",
			args: []Value{NewArray([]Value{NewInt(1)})},
			want: "array.find accepts at most one ifnone fallback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, tc.fn, tc.args, CallOptions{}, tc.want)
		})
	}
}

// TestArrayFindIfnoneFallbackRequiresBlock confirms the predicate block is
// still mandatory even when an ifnone fallback is supplied.
func TestArrayFindIfnoneFallbackRequiresBlock(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def ifnone
      :none
    end

    def find_no_block(values)
      values.find(ifnone)
    end
    `)

	requireCallErrorContains(t, script, "find_no_block",
		[]Value{NewArray([]Value{NewInt(1)})}, CallOptions{}, "array.find requires a block")
}
