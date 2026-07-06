package runtime

import "testing"

// TestBracketCommandArgumentEvaluation pins end-to-end evaluation of
// parenless commands taking array-literal arguments (`wrap [3, 1, 2]` ==
// `wrap([3, 1, 2])`), alongside the indexing readings that must survive: a
// known local indexes in every spacing, including through an assignment.
func TestBracketCommandArgumentEvaluation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def wrap(xs)
      xs
    end

    def concat(a, b)
      a + b
    end

    def command_array()
      wrap [3, 1, 2].sort.join("-")
    end

    def command_two_arrays()
      concat [1], [2]
    end

    def command_later_argument()
      concat [1], [2, 3]
    end

    def local_spaced_index()
      a = [10, 20, 30]
      a [1]
    end

    def local_spaced_index_assignment()
      a = [10, 20, 30]
      a [0] = 99
      a[0]
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "command array with postfix chain", fn: "command_array", want: NewString("1-2-3")},
		{name: "local spaced bracket indexes", fn: "local_spaced_index", want: NewInt(20)},
		{name: "local spaced bracket assigns", fn: "local_spaced_index_assignment", want: NewInt(99)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %v, want %v", tt.fn, got, tt.want)
			}
		})
	}

	t.Run("two array arguments", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "command_two_arrays", nil)
		compareArrays(t, got, []Value{NewInt(1), NewInt(2)})
	})

	t.Run("array as later argument", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "command_later_argument", nil)
		compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(3)})
	})
}

// TestBracketFlushAgainstBuiltinStaysIndexing pins the escape hatch: a
// bracket flush against a non-local callee keeps the indexing reading, so
// `puts[1]` still fails at runtime instead of becoming a call.
func TestBracketFlushAgainstBuiltinStaysIndexing(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def run()
      puts[1]
    end
    `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "cannot index builtin")
}
