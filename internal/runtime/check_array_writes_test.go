package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Element writes to a local whose fact is a declared array<T> — shovel
// appends, indexed assignment, and the in-place builtin mutators — are
// checked against T: a provably disjoint value is reported at the write, a
// provably compatible write preserves the declared fact, and everything else
// conservatively weakens it.

func TestArrayMutatorSplatUsesEvaluationTimeValues(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def later()
  yield
  2
end

def run()
  args = []
  items = [1]
  items.push(*args, later() do
    args = ["bad"]
  end)
  [items, args]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindArray {
		t.Fatalf("run() kind = %v, want array", got.Kind())
	}
	result := got.Array()
	if len(result) != 2 {
		t.Fatalf("run() length = %d, want 2", len(result))
	}
	wantItems := NewArray([]Value{NewInt(1), NewInt(2)})
	if !result[0].Equal(wantItems) {
		t.Fatalf("run() items = %s, want %s", result[0].String(), wantItems.String())
	}
	wantArgs := NewArray([]Value{NewString("bad")})
	if !result[1].Equal(wantArgs) {
		t.Fatalf("run() args = %s, want %s", result[1].String(), wantArgs.String())
	}
}

func TestCheckArrayMutatorRetainedAliasesUseEvaluationTimeBindings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		wantLine int
	}{
		{
			name: "push splat",
			source: `def later() -> int
  yield
  3
end

def f(rows: array<array<int> | int>)
  args = [[1]]
  rows.push(*args, later() do
    args = [[2]]
  end)
  args[0] << "new"
  rows << "bad"
end
`,
			wantLine: 12,
		},
		{
			name: "fill value",
			source: `def selector() -> int
  yield
  0
end

def f(rows: array<array<int>>)
  value = [1]
  rows.fill(value, selector() do
    value = [2]
  end)
  value << "new"
  rows << "bad"
end
`,
			wantLine: 12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warnings := compileScriptDefault(t, tc.source).CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one incompatible write warning", warnings)
			}
			if warnings[0].Pos.Line != tc.wantLine ||
				!strings.Contains(warnings[0].Message, "write to rows expected element") ||
				!strings.Contains(warnings[0].Message, "got string") {
				t.Fatalf(
					"CheckWarnings() = %#v, want incompatible write warning on line %d",
					warnings,
					tc.wantLine,
				)
			}
		})
	}
}

func TestArrayMutatorRetainedAliasesUseEvaluationTimeBindings(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def push_later() -> int
  yield
  3
end

def fill_selector() -> int
  yield
  0
end

def run()
  push_rows = [[0]]
  push_args = [[1]]
  push_rows.push(*push_args, push_later() do
    push_args = [[2]]
  end)
  push_args[0] << "new"

  fill_rows = [[0]]
  fill_value = [1]
  fill_rows.fill(fill_value, fill_selector() do
    fill_value = [2]
  end)
  fill_value << "new"

  [push_rows, push_args, fill_rows, fill_value]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1)}),
			NewInt(3),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(2), NewString("new")}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
		}),
		NewArray([]Value{NewInt(2), NewString("new")}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayMutatorExactSplatFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		wantLine int
	}{
		{
			name: "diagnostic uses splat expression position",
			source: `def f(items: array<int>)
  args = ["bad"]
  items.push(*args)
end
`,
			wantLine: 3,
		},
		{
			name: "fill selectors keep their evaluation facts",
			source: `def zero
  0
end

def f(items: array<int>)
  args = ["bad", zero(), 1]
  items.fill(*args)
end
`,
			wantLine: 7,
		},
		{
			name: "direct fill selectors keep their evaluation facts",
			source: `def zero
  0
end

def f(items: array<int>)
  items.fill("bad", zero(), 1)
end
`,
			wantLine: 6,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warnings := compileScriptDefault(t, tc.source).CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one incompatible write warning", warnings)
			}
			if warnings[0].Pos.Line != tc.wantLine ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf(
					"CheckWarnings() = %#v, want incompatible write warning on line %d",
					warnings,
					tc.wantLine,
				)
			}
		})
	}
}

func TestArrayMutatorRepeatedSplatsKeepDiagnosticOrigins(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(items: array<int>)
  args = ["bad"]
  items.push(
    *args,
    *args
  )
end
`)

	warnings := script.CheckWarningsForFunction("run")
	wantLines := []int{4, 5}
	if len(warnings) != len(wantLines) {
		t.Fatalf("CheckWarningsForFunction(run) = %#v, want two incompatible write warnings", warnings)
	}
	for i, warning := range warnings {
		if warning.Pos.Line != wantLines[i] ||
			!strings.Contains(warning.Message, "write to items expected element int, got string") {
			t.Errorf(
				"CheckWarningsForFunction(run)[%d] = %#v, want incompatible write warning on line %d",
				i,
				warning,
				wantLines[i],
			)
		}
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewArray([]Value{NewInt(1)})},
		CallOptions{},
	)
	want := NewArray([]Value{NewInt(1), NewString("bad"), NewString("bad")})
	if !got.Equal(want) {
		t.Fatalf("run([1]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayMutatorAlternativeDiagnosticsAreOrderIndependent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args string
	}{
		{name: "short alternative first", args: `flag ? ["bad"] : ["bad", "bad"]`},
		{name: "long alternative first", args: `flag ? ["bad", "bad"] : ["bad"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = `+tc.args+`
  items.push(*args)
end
`)

			// One warning, not one per bad element: both writes land at the
			// same position with the same text, so a second copy tells the
			// reader nothing. What this test protects is that the count does
			// not depend on which alternative the checker sees first, and
			// that holds either way.
			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 {
				t.Fatalf("CheckWarningsForFunction(run) = %#v, want one incompatible write warning", warnings)
			}
			for _, warning := range warnings {
				if warning.Pos.Line != 4 ||
					!strings.Contains(warning.Message, "write to items expected element int, got string") {
					t.Errorf(
						"CheckWarningsForFunction(run) = %#v, want incompatible write warnings on line 4",
						warnings,
					)
					break
				}
			}
		})
	}
}

func TestCheckArrayMutatorAlternativeDiagnosticsUnionTypes(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = flag ? ["bad"] : [true]
  items.push(*args)
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 4 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string | bool") {
		t.Fatalf(
			"CheckWarningsForFunction(run) = %#v, want one unioned incompatible write warning on line 4",
			warnings,
		)
	}
}

func TestCheckArrayMutatorExactSplatsKeepRetainedElementProvenance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call string
	}{
		{name: "push", call: "rows.push(*args)"},
		{name: "append", call: "rows.append(*args)"},
		{name: "prepend", call: "rows.prepend(*args)"},
		{name: "unshift", call: "rows.unshift(*args)"},
		{name: "fill", call: "rows.fill(*args)"},
		{name: "non-padding insert", call: "rows.insert(0, *args)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireNoCheckWarnings(t, compileScriptDefault(t, `
def takes_string(value: string)
  value
end

def f(rows: array<array<int>>, value)
  args = [[1]]
  `+tc.call+`
  args[0] << value
  for row in rows
    for item in row
      takes_string(item)
    end
  end
end
`))
		})
	}
}

// TestArrayMutatorExactSplatsGraftValuesNotAliases inverts what this used to
// pin. A splatted element grafted into a receiver is another value from the
// moment it lands there, so a later write through the argument array's own
// element cannot reach the row the mutator stored.
func TestArrayMutatorExactSplatsGraftValuesNotAliases(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  push_rows = [[0]]
  push_args = [[1]]
  push_rows.push(*push_args)
  push_args[0] << 2

  append_rows = [[0]]
  append_args = [[1]]
  append_rows.append(*append_args)
  append_args[0] << 2

  prepend_rows = [[0]]
  prepend_args = [[1]]
  prepend_rows.prepend(*prepend_args)
  prepend_args[0] << 2

  unshift_rows = [[0]]
  unshift_args = [[1]]
  unshift_rows.unshift(*unshift_args)
  unshift_args[0] << 2

  fill_rows = [[0]]
  fill_args = [[1]]
  fill_rows.fill(*fill_args)
  fill_args[0] << 2

  insert_rows = [[0]]
  insert_args = [[1]]
  insert_rows.insert(0, *insert_args)
  insert_args[0] << 2

  [push_rows, append_rows, prepend_rows, unshift_rows, fill_rows, insert_rows]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(0)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(0)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
			NewArray([]Value{NewInt(0)}),
		}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestArrayInsertPaddingBoundaryMatchesRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def later()
  yield
  1
end

def insert_at(flag: bool)
  items = []
  index = flag ? 0 : -1
  items.insert(index, 1)
  items
end

def run()
  zero = []
  zero.insert(0, 1)
  negative = []
  negative.insert(-1, 1)
  positive = []
  positive.insert(1, 1)
  index = 0
  captured = []
  captured.insert(index, later() do
    index = 5
  end)
  [zero, negative, positive, captured, index, insert_at(true), insert_at(false)]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewNil(), NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewInt(5),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayInsertStoredSplatSelectorAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = [flag ? 0 : -1]
  items.insert(*args, 1)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "zero",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name: "negative",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewInt(1), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(0)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([0], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayInsertDifferentWidthSplatAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = flag ? [0, 1] : [0]
  items.insert(*args)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "inserts",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name: "index only",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(0)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([0], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayInsertMutatingAndRaisingAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		args        string
		wantWarning bool
		wantTrue    Value
	}{
		{
			name:        "compatible write or invalid shape",
			args:        "flag ? [0, 1] : []",
			wantWarning: true,
			wantTrue:    NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name:     "padding write or invalid shape",
			args:     "flag ? [3, 1] : []",
			wantTrue: NewArray([]Value{NewInt(0), NewNil(), NewNil(), NewInt(1), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = `+tc.args+`
  begin
    items.insert(*args)
  rescue
    nil
  end
  items << "bad"
  items
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if tc.wantWarning {
				if len(warnings) != 1 ||
					warnings[0].Pos.Line != 9 ||
					!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
					t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 9", "run", warnings)
				}
			} else if len(warnings) != 0 {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want no warnings after a possible padding write", "run", warnings)
			}

			runtimeCases := []struct {
				name string
				flag bool
				want Value
			}{
				{name: "mutates", flag: true, want: tc.wantTrue},
				{
					name: "raises",
					flag: false,
					want: NewArray([]Value{NewInt(0), NewString("bad")}),
				},
			}
			for _, runtimeCase := range runtimeCases {
				t.Run(runtimeCase.name, func(t *testing.T) {
					t.Parallel()

					got := callScript(t, context.Background(), script, "run", []Value{
						NewArray([]Value{NewInt(0)}),
						NewBool(runtimeCase.flag),
					}, CallOptions{})
					if !got.Equal(runtimeCase.want) {
						t.Errorf(
							"run([0], %t) = %s, want %s",
							runtimeCase.flag,
							got.String(),
							runtimeCase.want.String(),
						)
					}
				})
			}
		})
	}
}

func TestCheckArrayMutatorKeywordSplatAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  options = flag ? {} : { extra: 2 }
  rescued = false
  begin
    items.push(1, **options)
  rescue
    rescued = true
  end
  items << "bad"
  [items, rescued]
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 10 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 10", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "empty keywords mutate",
			flag: true,
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1), NewInt(1), NewString("bad")}),
				NewBool(false),
			}),
		},
		{
			name: "keywords raise",
			flag: false,
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1), NewString("bad")}),
				NewBool(true),
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([1], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

// TestArrayFillMutationMatchesCheckerModel pins fill against the checker's
// model of it. The alias bound before the call keeps what it was given, and the
// returned value is another binding again, so the shovel that follows reaches
// only it.
func TestArrayFillMutationMatchesCheckerModel(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  items = [1, 2]
  alias_items = items
  returned = items.fill("bad")
  returned << "tail"
  empty = []
  empty.fill("bad")
  [items, alias_items, returned, empty]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{NewString("bad"), NewString("bad")}),
		NewArray([]Value{NewInt(1), NewInt(2)}),
		NewArray([]Value{NewString("bad"), NewString("bad"), NewString("tail")}),
		NewArray([]Value{}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayFillSelectorAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  start = flag ? 0 : -1
  items.fill(1, start, 1)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "zero",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name: "negative",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewInt(1), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(0), NewInt(0)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([0, 0], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayFillCorrelatedSplatAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = flag ? ["unused", 5, -1] : ["unused", 0, 0]
  items.fill(*args)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	for _, flag := range []bool{false, true} {
		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewBool(flag),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("bad")})
		if !got.Equal(want) {
			t.Errorf("run([1], %t) = %s, want %s", flag, got.String(), want.String())
		}
	}
}

func TestCheckArrayFillRepeatedSplatAlternativesStayCorrelated(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = flag ? ["unused"] : [0]
  begin
    items.fill(*args, *args)
  rescue
    nil
  end
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 9 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 9", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "invalid selector pair raises",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewString("bad")}),
		},
		{
			name: "compatible fill",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([1], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayFillProjectedSelectorsStayCorrelated(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		selectors string
		wantLine  int
	}{
		{
			name:      "direct projections",
			selectors: `pair[0], pair[1]`,
			wantLine:  9,
		},
		{
			name:      "projected locals through an alias",
			selectors: `start, count`,
			wantLine:  12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := ""
			if tc.name == "projected locals through an alias" {
				setup = `
  other = pair
  start = pair[0]
  count = other[1]`
			}
			script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  pair = flag ? ["invalid", 1] : [0, -1]`+setup+`
  begin
    items.fill("bad", `+tc.selectors+`)
  rescue
    nil
  end
  items << "later"
  items
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 ||
				warnings[0].Pos.Line != tc.wantLine ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want only the later write warning",
					"run",
					warnings,
				)
			}

			for _, flag := range []bool{false, true} {
				got := callScript(t, context.Background(), script, "run", []Value{
					NewArray([]Value{NewInt(1)}),
					NewBool(flag),
				}, CallOptions{})
				want := NewArray([]Value{NewInt(1), NewString("later")})
				if !got.Equal(want) {
					t.Errorf("run([1], %t) = %s, want %s", flag, got.String(), want.String())
				}
			}
		})
	}
}

func TestCheckArrayFillDestructuredSelectorsStayCorrelated(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  start, count = flag ? ["invalid", 1] : [0, -1]
  begin
    items.fill("bad", start, count)
  rescue
    nil
  end
  items << "later"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 9 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want only the later write warning",
			"run",
			warnings,
		)
	}

	for _, flag := range []bool{false, true} {
		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewBool(flag),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1], %t) = %s, want %s", flag, got.String(), want.String())
		}
	}
}

func TestCheckArrayFillProjectedLiteralDestructuringStaysCorrelated(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  pair = flag ? ["invalid", 1] : [0, -1]
  start, count = [pair[0], pair[1]]
  begin
    items.fill("bad", start, count)
  rescue
    nil
  end
  items << "later"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 10 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want only the later write warning",
			"run",
			warnings,
		)
	}

	for _, flag := range []bool{false, true} {
		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewBool(flag),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1], %t) = %s, want %s", flag, got.String(), want.String())
		}
	}
}

func TestCheckArrayFillProjectedSelectorsKeepRealWrite(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  pair = flag ? [0, 1] : ["invalid", -1]
  begin
    items.fill("bad", pair[0], pair[1])
  rescue
    nil
  end
  items << "later"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want only the fill write warning on line 5",
			"run",
			warnings,
		)
	}

	gotWrite := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
		NewBool(true),
	}, CallOptions{})
	wantWrite := NewArray([]Value{NewString("bad"), NewString("later")})
	if !gotWrite.Equal(wantWrite) {
		t.Errorf("run([1], true) = %s, want %s", gotWrite.String(), wantWrite.String())
	}
	gotRaise := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
		NewBool(false),
	}, CallOptions{})
	wantRaise := NewArray([]Value{NewInt(1), NewString("later")})
	if !gotRaise.Equal(wantRaise) {
		t.Errorf("run([1], false) = %s, want %s", gotRaise.String(), wantRaise.String())
	}
}

func TestCheckArrayFillProjectedSelectorsControlCompletion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		secondPair  string
		wantWarning bool
	}{
		{
			name:       "all correlated pairs raise",
			secondPair: `[0, "invalid"]`,
		},
		{
			name:        "one correlated pair completes",
			secondPair:  `[0, 1]`,
			wantWarning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  pair = flag ? ["invalid", 1] : `+tc.secondPair+`
  items.fill(1, pair[0], pair[1])
  takes_int("after fill")
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if !tc.wantWarning {
				if len(warnings) != 0 {
					t.Fatalf(
						"CheckWarningsForFunction(%q) = %#v, want no warning after an always-raising fill",
						"run",
						warnings,
					)
				}
				for _, flag := range []bool{false, true} {
					_, err := script.Call(
						context.Background(),
						"run",
						[]Value{NewArray([]Value{NewInt(1)}), NewBool(flag)},
						CallOptions{},
					)
					if err == nil {
						t.Fatalf("run([1], %t) succeeded, want invalid selector error", flag)
					}
				}
				return
			}
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want the reachable argument warning",
					"run",
					warnings,
				)
			}
		})
	}
}

func TestCheckArrayFillProjectedSelectorsControlBlockScheduling(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		secondPair  string
		wantWarning bool
	}{
		{
			name:       "all correlated pairs reject before the block",
			secondPair: `[0, "invalid"]`,
		},
		{
			name:        "one correlated pair reaches the block",
			secondPair:  `[0, 1]`,
			wantWarning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  pair = flag ? ["invalid", 1] : `+tc.secondPair+`
  items.fill(pair[0], pair[1]) do
    takes_int("inside block")
    1
  end
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if !tc.wantWarning {
				if len(warnings) != 0 {
					t.Fatalf(
						"CheckWarningsForFunction(%q) = %#v, want no warning from an unreachable block",
						"run",
						warnings,
					)
				}
				return
			}
			found := false
			for _, warning := range warnings {
				found = found ||
					strings.Contains(warning.Message, "argument value expected int, got string")
			}
			if !found {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want the block argument warning",
					"run",
					warnings,
				)
			}
		})
	}
}

func TestCheckArrayFillProjectedSelectorCorrelationClearsAfterMutation(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  pair = flag ? ["invalid", -1] : [0, -1]
  start = pair[0]
  pair[1] = 1
  count = pair[1]
  begin
    items.fill("bad", start, count)
  rescue
    nil
  end
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 8 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the possible fill write warning on line 8",
			"run",
			warnings,
		)
	}

	gotWrite := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
		NewBool(false),
	}, CallOptions{})
	wantWrite := NewArray([]Value{NewString("bad")})
	if !gotWrite.Equal(wantWrite) {
		t.Errorf("run([1], false) = %s, want %s", gotWrite.String(), wantWrite.String())
	}
}

func TestCheckArrayFillProjectedSelectorCorrelationSplitsAfterRebind(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, left: bool, right: bool)
  pair = left ? ["invalid", 0] : [0, 0]
  start = pair[0]
  pair = right ? [0, 1] : [0, -1]
  count = pair[1]
  begin
    items.fill("bad", start, count)
  rescue
    nil
  end
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 8 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the possible fill write warning on line 8",
			"run",
			warnings,
		)
	}

	got := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
		NewBool(false),
		NewBool(true),
	}, CallOptions{})
	want := NewArray([]Value{NewString("bad")})
	if !got.Equal(want) {
		t.Errorf("run([1], false, true) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayFillProjectedSelectorCorrelationClearsAtBranchMerge(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool, branch: bool)
  pair = flag ? ["invalid", 1] : [0, -1]
  start = pair[0]
  if branch
    count = pair[1]
  else
    count = pair[1]
  end
  begin
    items.fill("bad", start, count)
  rescue
    nil
  end
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 11 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the conservative fill write warning on line 11",
			"run",
			warnings,
		)
	}

	for _, flag := range []bool{false, true} {
		for _, branch := range []bool{false, true} {
			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
				NewBool(flag),
				NewBool(branch),
			}, CallOptions{})
			want := NewArray([]Value{NewInt(1)})
			if !got.Equal(want) {
				t.Errorf(
					"run([1], %t, %t) = %s, want %s",
					flag,
					branch,
					got.String(),
					want.String(),
				)
			}
		}
	}
}

func TestCheckArrayFillIndependentSelectorsRemainCartesian(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, left: bool, right: bool)
  starts = left ? ["invalid"] : [0]
  counts = right ? [1] : [-1]
  items.fill("bad", starts[0], counts[0])
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the possible fill write warning on line 5",
			"run",
			warnings,
		)
	}
}

func TestCheckArrayFillSingleExactReceiverControlsOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("negative range always raises", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run()
  items = [1]
  items.fill(2, -3..-1)
  takes_int("unreachable")
end
`)
		requireNoCheckWarnings(t, script)
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"array.fill range -3..-1 out of range",
		)
	})

	t.Run("rescued invalid span preserves receiver", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>)
  begin
    items.fill("bad", -3..-1)
  rescue
    nil
  end
  items << "later"
  items
end

def entry()
  run([1])
end
`)
		warnings := script.CheckWarningsForFunction("entry")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 8 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the rescued tail write",
				"entry",
				warnings,
			)
		}
		got := callScript(t, context.Background(), script, "entry", nil, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Fatalf("entry() = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("bare start past end does not write", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>)
  items.fill("bad", 5)
  items << "later"
  items
end

def entry()
  run([1, 2, 3])
end
`)
		warnings := script.CheckWarningsForFunction("entry")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 4 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the later write",
				"entry",
				warnings,
			)
		}
		got := callScript(t, context.Background(), script, "entry", nil, CallOptions{})
		want := NewArray([]Value{
			NewInt(1),
			NewInt(2),
			NewInt(3),
			NewString("later"),
		})
		if !got.Equal(want) {
			t.Fatalf("entry() = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("zero count past end models nil padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>)
  items.fill(4, 5, 0)
  items << "later"
  items
end

def entry()
  run([1, 2, 3])
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "entry", nil, CallOptions{})
		want := NewArray([]Value{
			NewInt(1),
			NewInt(2),
			NewInt(3),
			NewNil(),
			NewNil(),
			NewString("later"),
		})
		if !got.Equal(want) {
			t.Fatalf("entry() = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("nullable bound survives nil padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int | nil>)
  items.fill(4, 5, 0)
  items << "later"
end

def entry()
  run([1])
end
`)
		warnings := script.CheckWarningsForFunction("entry")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 4 ||
			!strings.Contains(
				warnings[0].Message,
				"write to items expected element int | nil, got string",
			) {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the nullable-bound tail write",
				"entry",
				warnings,
			)
		}
	})
}

func TestCheckArrayFillSingleExactReceiverControlsBlockScheduling(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		receiver    string
		wantWarning bool
	}{
		{name: "empty receiver skips block", receiver: "[]"},
		{name: "nonempty receiver schedules block", receiver: "[1]", wantWarning: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run()
  items = `+tc.receiver+`
  items.fill do
    takes_int("inside block")
    1
  end
end
`)
			warnings := script.CheckWarningsForFunction("run")
			if !tc.wantWarning {
				if len(warnings) != 0 {
					t.Fatalf(
						"CheckWarningsForFunction(%q) = %#v, want no warning from a skipped block",
						"run",
						warnings,
					)
				}
				return
			}
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want the reachable block warning",
					"run",
					warnings,
				)
			}
		})
	}

	t.Run("literal receiver length is captured", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run()
  [].fill do
    raise "skipped"
  end
  takes_int("reachable")
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the reachable tail warning",
				"run",
				warnings,
			)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"argument value expected int, got string",
		)
	})
}

func TestCheckArrayFillReceiverLengthRejectsMultipleAlternatives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		alternates string
	}{
		{name: "different lengths", alternates: `flag ? [1] : [1, 2]`},
		{name: "same length", alternates: `flag ? [1] : [2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  items = `+tc.alternates+`
  items.fill do
    raise "stop"
  end
  takes_int("conservatively reachable")
end
`)
			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want the conservative tail warning",
					"run",
					warnings,
				)
			}
		})
	}
}

func TestCheckArrayMutatorAliasSplatsShareEvaluatedChoice(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  items = [1]
  args = flag ? [] : ["bad", 0]
  other = args
  items.fill(*args, *other)
  takes_int("unreachable")
end
`)

	requireNoCheckWarnings(t, script)
	for _, flag := range []bool{false, true} {
		_, err := script.Call(
			context.Background(),
			"run",
			[]Value{NewBool(flag)},
			CallOptions{},
		)
		if err == nil {
			t.Fatalf("run(%t) succeeded, want invalid fill shape", flag)
		}
	}
}

func TestCheckArrayMutatorAliasSplatCorrelationKeepsValidChoice(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  items = [1]
  args = flag ? ["bad"] : [0]
  other = args
  begin
    items.fill(*args, *other)
  rescue
    nil
  end
  takes_int("reachable")
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the reachable argument warning",
			"run",
			warnings,
		)
	}
}

func TestCheckArrayMutatorCrossKindSplatsShareEvaluatedChoice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup string
		call  string
	}{
		{
			name: "same local",
			call: "items.fill(*args, **args)",
		},
		{
			name:  "container alias",
			setup: "  other = args\n",
			call:  "items.fill(*args, **other)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  items = []
  args = flag ? [1] : { x: 1 }
`+tc.setup+`  `+tc.call+`
  takes_int("unreachable")
end
`)

			requireNoCheckWarnings(t, script)
			for _, flag := range []bool{false, true} {
				_, err := script.Call(
					context.Background(),
					"run",
					[]Value{NewBool(flag)},
					CallOptions{},
				)
				if err == nil {
					t.Fatalf("run(%t) succeeded, want one expansion to fail", flag)
				}
			}
		})
	}
}

func TestCheckArrayMutatorIndependentCrossKindSplatsCanComplete(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run()
  items = []
  args = [1]
  options = {}
  items.push(*args, **options)
  takes_int("reachable")
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the reachable argument warning",
			"run",
			warnings,
		)
	}
}

func TestCheckArrayMutatorSplatsSeparatedByMutationUseDistinctChoices(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>)
  args = []
  items.fill(*args, args << 0, *args)
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got array<int>") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the evaluated middle array write warning",
			"run",
			warnings,
		)
	}
}

func TestCheckArrayMutatorSplatsCaptureValuesAroundScriptMutation(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def mutate(args)
  args << 0
  "bad"
end

def run(items: array<int>)
  args = []
  items.fill(*args, mutate(args), *args)
  items << "later"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the evaluated mutation result warning",
			"run",
			warnings,
		)
	}

	got := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
	}, CallOptions{})
	want := NewArray([]Value{NewString("bad"), NewString("later")})
	if !got.Equal(want) {
		t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayMutatorDoesNotReplayTypedParameterNormalization(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
enum Status
  Draft
end

def mutate(args: array<Status>)
  args << :draft
  nil
end

def takes_int(value: int)
  value
end

def run(items: array<symbol>)
  args = [:draft]
  mutate(args)
  items.fill(*args)
  begin
    takes_int("reachable")
  rescue
    nil
  end
  [items, args]
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 20 ||
		!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the reachable argument warning",
			"run",
			warnings,
		)
	}

	got := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewSymbol("old")}),
	}, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{NewSymbol("draft")}),
		NewArray([]Value{NewSymbol("draft")}),
	})
	if !got.Equal(want) {
		t.Errorf("run([:old]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayFillExpansionCapRetainsArityImpossibility(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, a: bool, b: bool, c: bool, d: bool, e: bool, f: bool)
  x1 = a ? [] : [1, 2, 3, 4]
  x2 = b ? [] : [1, 2, 3, 4]
  x3 = c ? [] : [1, 2, 3, 4]
  x4 = d ? [] : [1, 2, 3, 4]
  x5 = e ? [] : [1, 2, 3, 4]
  x6 = f ? [] : [1, 2, 3, 4]
  begin
    items.fill(*x1, *x2, *x3, *x4, *x5, *x6)
  rescue
    nil
  end
  items << "later"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
			"run",
			warnings,
		)
	}

	cases := [][]Value{
		{
			NewArray([]Value{NewInt(1)}),
			NewBool(true),
			NewBool(true),
			NewBool(true),
			NewBool(true),
			NewBool(true),
			NewBool(true),
		},
		{
			NewArray([]Value{NewInt(1)}),
			NewBool(false),
			NewBool(true),
			NewBool(true),
			NewBool(true),
			NewBool(true),
			NewBool(true),
		},
	}
	for _, args := range cases {
		got := callScript(t, context.Background(), script, "run", args, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run(%v) = %s, want %s", args, got.String(), want.String())
		}
	}
}

func TestCheckArrayFillExpansionCapKeepsFeasibleArityReachable(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(a: bool, b: bool, c: bool, d: bool, e: bool, f: bool)
  items = []
  x1 = a ? [] : [1]
  x2 = b ? [] : [1, 2, 3, 4]
  x3 = c ? [] : [1, 2, 3, 4]
  x4 = d ? [] : [1, 2, 3, 4]
  x5 = e ? [] : [1, 2, 3, 4]
  x6 = f ? [] : [1, 2, 3, 4]
  items.fill(*x1, *x2, *x3, *x4, *x5, *x6)
  takes_int("possibly reachable")
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the reachable argument warning",
			"run",
			warnings,
		)
	}
}

func TestArrayMutatorSplatCorrelationUsesBindingGeneration(t *testing.T) {
	t.Parallel()

	one := &ArrayLiteral{Elements: []Expression{&IntegerLiteral{Value: 1}}}
	empty := &ArrayLiteral{}
	firstValue := &Identifier{Name: "args"}
	secondValue := &Identifier{Name: "args"}
	firstSplat := &SplatArg{Value: firstValue}
	secondSplat := &SplatArg{Value: secondValue}
	firstAlternatives := []Expression{one, empty}
	cases := []struct {
		name             string
		secondGeneration uint64
		secondValues     []Expression
		wantLengths      map[int]int
	}{
		{
			name:             "same evaluated source stays correlated",
			secondGeneration: 7,
			secondValues:     []Expression{one, empty},
			wantLengths:      map[int]int{0: 1, 2: 1},
		},
		{
			name:             "rebound source stays independent",
			secondGeneration: 8,
			secondValues:     []Expression{one, empty},
			wantLengths:      map[int]int{0: 1, 1: 2, 2: 1},
		},
		{
			name:             "reordered alternatives stay independent",
			secondGeneration: 7,
			secondValues:     []Expression{empty, one},
			wantLengths:      map[int]int{0: 1, 1: 2, 2: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checker := scriptChecker{}
			variants, exact := checker.staticallyExpandedArrayMutatorCalls(
				&CallExpr{Args: []Expression{firstSplat, secondSplat}},
				map[Expression][]Expression{
					firstValue:  firstAlternatives,
					secondValue: tc.secondValues,
				},
				map[Expression]checkCallSplatSource{
					firstValue: {
						identity: []capturedContainerRoot{{
							name:       "args",
							generation: 7,
						}},
						alternatives: firstAlternatives,
					},
					secondValue: {
						identity: []capturedContainerRoot{{
							name:       "args",
							generation: tc.secondGeneration,
						}},
						alternatives: tc.secondValues,
					},
				},
			)
			if !exact {
				t.Fatal("staticallyExpandedArrayMutatorCalls() exact = false, want true")
			}
			gotLengths := make(map[int]int)
			for _, variant := range variants {
				gotLengths[len(variant.call.Args)]++
			}
			if !reflect.DeepEqual(gotLengths, tc.wantLengths) {
				t.Errorf("variant length counts = %v, want %v", gotLengths, tc.wantLengths)
			}
		})
	}
}

func TestCheckArrayFillInvalidSelectorsPreserveReceiverThroughRescue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call string
	}{
		{name: "non numeric start", call: `ignored = items.fill("unused", "bad")`},
		{name: "bignum start", call: `ignored = items.fill("unused", 9223372036854775808)`},
		{name: "bignum length", call: `ignored = items.fill("unused", 0, 9223372036854775808)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>)
  begin
    `+tc.call+`
  rescue
    nil
  end
  items << "bad"
  items
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 ||
				warnings[0].Pos.Line != 8 ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 8", "run", warnings)
			}

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
			}, CallOptions{})
			want := NewArray([]Value{NewInt(1), NewString("bad")})
			if !got.Equal(want) {
				t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
			}
		})
	}
}

func TestCheckInvalidArrayMutatorCallShapesPreserveReceiverThroughRescue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call string
	}{
		{name: "consumed fill missing value", call: "ignored = items.fill()"},
		{name: "receiver assignment fill missing value", call: "items = items.fill()"},
		{name: "fill too many arguments", call: `items.fill("never written", 0, 1, 2)`},
		{name: "insert nonnumeric index", call: `ignored = items.insert("bad index", "never written")`},
		{name: "push keyword", call: `items.push("never written", extra: 2)`},
		{name: "insert keyword", call: `items.insert(0, "never written", extra: 2)`},
		{name: "fill keyword", call: `items.fill("never written", extra: 2)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>)
  rescued = false
  begin
    `+tc.call+`
  rescue
    rescued = true
  end
  items << "bad"
  [items, rescued]
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 ||
				warnings[0].Pos.Line != 9 ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 9", "run", warnings)
			}

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
			}, CallOptions{})
			want := NewArray([]Value{
				NewArray([]Value{NewInt(1), NewString("bad")}),
				NewBool(true),
			})
			if !got.Equal(want) {
				t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
			}
		})
	}
}

func TestCheckInvalidArrayFillBlockShapeDoesNotRunBlock(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>)
  rescued = false
  begin
    items.fill(0, 1, 2) do
      items << "block poison"
      0
    end
  rescue
    rescued = true
  end
  items << "bad"
  [items, rescued]
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 12 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want only the incompatible append on line 12", "run", warnings)
	}

	got := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
	}, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{NewInt(1), NewString("bad")}),
		NewBool(true),
	})
	if !got.Equal(want) {
		t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckUnrescuedInvalidArrayFillStopsReachability(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>)
  items.fill()
  items << "unreachable"
end
`)

	requireNoCheckWarnings(t, script)
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewArray([]Value{NewInt(1)})},
		CallOptions{},
		"array.fill requires a value or a block",
	)
}

func TestArrayFillSelectorSafetyBoundaryMatchesRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  bare_start = []
  bare_start.fill(1, 2)
  safe_window = []
  safe_window.fill(1, 0, 1)
  negative_length = [1]
  negative_length.fill("bad", 0, -1)
  padding_only = []
  padding_only.fill(1, 2, 0)
  range_window = []
  range_window.fill(1, 0..1)
  range_padding = []
  range_padding.fill(1, 2..2)
  range_empty = []
  range_empty.fill(1, 0...0)
  beginless = [1, 2, 3]
  beginless.fill(4, ..1)
  float_range = [1, 2, 3]
  float_range.fill(5, 0.0..1.9)
  negative_end = [1, 2, 3]
  negative_end.fill(6, 0..-1)
  padding = [1]
  padding.fill(7, 3, 1)
  [
    bare_start,
    safe_window,
    negative_length,
    padding_only,
    range_window,
    range_padding,
    range_empty,
    beginless,
    float_range,
    negative_end,
    padding,
  ]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewNil(), NewNil()}),
		NewArray([]Value{NewInt(1), NewInt(1)}),
		NewArray([]Value{NewNil(), NewNil(), NewInt(1)}),
		NewArray([]Value{}),
		NewArray([]Value{NewInt(4), NewInt(4), NewInt(3)}),
		NewArray([]Value{NewInt(5), NewInt(5), NewInt(3)}),
		NewArray([]Value{NewInt(6), NewInt(6), NewInt(6)}),
		NewArray([]Value{NewInt(1), NewNil(), NewNil(), NewInt(7)}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestArrayFillNextResultRespectsEnsureCompletion(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def always_raise()
  items = [1, 2]
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        raise "stop"
      end
    end
  rescue
    nil
  end
  items
end

def conditionally_raise(stop: bool)
  items = [1, 2]
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        if stop
          raise "stop"
        end
      end
    end
  rescue
    nil
  end
  items
end
`)

	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{
			name: "raising ensure",
			fn:   "always_raise",
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "conditional ensure raises",
			fn:   "conditionally_raise",
			args: []Value{NewBool(true)},
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "conditional ensure falls through",
			fn:   "conditionally_raise",
			args: []Value{NewBool(false)},
			want: NewArray([]Value{NewString("bad"), NewString("bad")}),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callScript(t, context.Background(), script, tc.fn, tc.args, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("%s(%v) = %s, want %s", tc.fn, tc.args, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayFillNoOpSelectorsPreserveBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fillCall string
	}{
		{name: "negative length", fillCall: `items.fill("bad", 0, -1)`},
		{name: "zero length", fillCall: `items.fill("bad", 0, 0)`},
		{name: "empty range", fillCall: `items.fill("bad", 0...0)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def f(items: array<int>)
  `+tc.fillCall+`
  items << "later"
end
`)

			warnings := script.CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one later write warning", warnings)
			}
			if warnings[0].Pos.Line != 4 ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf("CheckWarnings() = %#v, want the warning on the later write", warnings)
			}
		})
	}
}

func TestCheckArrayFillNegativeLengthPrecedesUnknownStart(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def f(items: array<int>, start)
  items.fill("unused", start, -1)
  items << "later"
end
`)

	warnings := script.CheckWarnings()
	if len(warnings) != 1 {
		t.Fatalf("CheckWarnings() = %#v, want one later write warning", warnings)
	}
	if warnings[0].Pos.Line != 4 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarnings() = %#v, want the warning on the later write", warnings)
	}
}

func TestCheckArrayFillSkippedBlockPaddingWeakensBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fill string
	}{
		{name: "zero count", fill: `items.fill(5, 0)`},
		{name: "empty range", fill: `items.fill(5...5)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>)
  `+tc.fill+` do
    raise "must not run"
  end
  items << "later"
  items
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
			}, CallOptions{})
			want := NewArray([]Value{
				NewInt(1),
				NewNil(),
				NewNil(),
				NewNil(),
				NewNil(),
				NewString("later"),
			})
			if !got.Equal(want) {
				t.Fatalf("run([1]) = %s, want %s", got.String(), want.String())
			}
		})
	}

	t.Run("nil-compatible bound survives padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int | nil>)
  items.fill(5, 0) do
    raise "must not run"
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int | nil, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the later incompatible write",
				"run",
				warnings,
			)
		}
	})
}

func TestCheckArrayFillNoncompletingBlockSplatOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("all exact alternatives skip and preserve", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  selectors = flag ? [0, 0] : [5, -1]
  items.fill(*selectors) do
    raise "must not run"
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the later incompatible write",
				"run",
				warnings,
			)
		}
	})

	t.Run("one exact alternative skips", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  selectors = flag ? [0, 1] : [0, 0]
  items.fill(*selectors) do
    raise "stop"
  end
  items << "reachable on zero count"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the reachable incompatible write",
				"run",
				warnings,
			)
		}
		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewBool(false),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("reachable on zero count")})
		if !got.Equal(want) {
			t.Fatalf("run([1], false) = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("all exact alternatives invoke", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  selectors = flag ? [0, 1] : [5, 2]
  items.fill(*selectors) do
    raise "stop"
  end
  takes_int("unreachable")
end
`)
		requireNoCheckWarnings(t, script)
	})
}

func TestCheckArrayFillOverflowStopsBeforeBlock(t *testing.T) {
	t.Parallel()

	t.Run("literal block and tail are unreachable", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>)
  items.fill(9223372036854775807, 1) do
    takes_int("block must not run")
    1
  end
  takes_int("tail unreachable")
end
`)
		requireNoCheckWarnings(t, script)
		requireCallErrorContains(
			t,
			script,
			"run",
			[]Value{NewArray([]Value{NewInt(1)})},
			CallOptions{},
			"array.fill window is too large",
		)
	})

	t.Run("value form and tail are unreachable", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>)
  items.fill("unused", 9223372036854775807, 1)
  takes_int("tail unreachable")
end
`)
		requireNoCheckWarnings(t, script)
		requireCallErrorContains(
			t,
			script,
			"run",
			[]Value{NewArray([]Value{NewInt(1)})},
			CallOptions{},
			"array.fill window is too large",
		)
	})

	t.Run("largest nonoverflowing span still invokes", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>)
  items.fill(9223372036854775806, 1) do
    takes_int("reachable block")
    raise "stop"
  end
  takes_int("unreachable tail")
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable block warning",
				"run",
				warnings,
			)
		}
	})
}

func TestCheckArrayFillExactSelectorAlternativesControlBlockOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("all alternatives skip the block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  count = flag ? 0 : -1
  items.fill(0, count) do
    takes_int("block unreachable")
    raise "stop"
  end
  items << "later"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 12 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
				"run",
				warnings,
			)
		}

		for _, flag := range []bool{false, true} {
			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
				NewBool(flag),
			}, CallOptions{})
			want := NewArray([]Value{NewInt(1), NewString("later")})
			if !got.Equal(want) {
				t.Errorf("run([1], %t) = %s, want %s", flag, got.String(), want.String())
			}
		}
	})

	t.Run("all alternatives invoke the block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  count = flag ? 1 : 2
  items.fill(0, count) do
    takes_int("block reachable")
    raise "stop"
  end
  takes_int("tail unreachable")
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 9 ||
			!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable block warning",
				"run",
				warnings,
			)
		}
	})

	t.Run("mixed alternatives keep block and tail paths", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  count = flag ? 0 : 1
  items.fill(0, count) do
    takes_int("block reachable")
    raise "stop"
  end
  takes_int("tail reachable")
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 2 ||
			warnings[0].Pos.Line != 9 ||
			warnings[1].Pos.Line != 12 {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want block and tail warnings",
				"run",
				warnings,
			)
		}
		for _, warning := range warnings {
			if !strings.Contains(warning.Message, "argument value expected int, got string") {
				t.Fatalf(
					"CheckWarningsForFunction(%q) = %#v, want only argument warnings",
					"run",
					warnings,
				)
			}
		}
	})

	t.Run("empty range alternatives skip the block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, flag: bool)
  selector = flag ? (0...0) : (1...1)
  items.fill(selector) do
    takes_int("block unreachable")
    1
  end
end
`)
		requireNoCheckWarnings(t, script)
	})

	t.Run("invalid shape rejects before the block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>)
  begin
    items.fill(0, 0, 0) do
      takes_int("block unreachable")
      items << "block mutation"
    end
  rescue
    nil
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			warnings[0].Pos.Line != 15 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
				"run",
				warnings,
			)
		}

		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
		}
	})
}

func TestCheckArrayFillDynamicSelectorFactsControlBlockOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("negative count skips for a dynamic numeric start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  items.fill(start, -1) do
    takes_int("block unreachable")
    raise "stop"
  end
  items << "later"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
				"run",
				warnings,
			)
		}
	})

	t.Run("zero count can pad for a dynamic numeric start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  items.fill(start, 0) do
    takes_int("block unreachable")
    raise "stop"
  end
  items << "later"
end
`)
		requireNoCheckWarnings(t, script)
	})

	t.Run("zero count cannot pad for a range-or-nil start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: range | nil)
  items.fill(start, 0) do
    takes_int("block unreachable")
    raise "stop"
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
				"run",
				warnings,
			)
		}

		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewNil(),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1], nil) = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("nullable numeric start preserves skipped and rescued paths", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int | nil)
  begin
    items.fill(start) do
      takes_int("block reachable")
      raise "stop"
    end
  rescue
    nil
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 2 ||
			warnings[0].Pos.Line != 9 ||
			warnings[1].Pos.Line != 15 {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the block and tail writes",
				"run",
				warnings,
			)
		}

		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewNil(),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1], nil) = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("nullable numeric count preserves skipped and rescued paths", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, count: int | nil)
  begin
    items.fill(0, count) do
      takes_int("block reachable")
      raise "stop"
    end
  rescue
    nil
  end
  items << "later"
  items
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 2 ||
			warnings[0].Pos.Line != 9 ||
			warnings[1].Pos.Line != 15 {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the block and tail writes",
				"run",
				warnings,
			)
		}

		got := callScript(t, context.Background(), script, "run", []Value{
			NewArray([]Value{NewInt(1)}),
			NewInt(0),
		}, CallOptions{})
		want := NewArray([]Value{NewInt(1), NewString("later")})
		if !got.Equal(want) {
			t.Errorf("run([1], 0) = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("nil count may invoke or skip without padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  items.fill(start, nil) do
    takes_int("block reachable")
    raise "stop"
  end
  items << "tail reachable"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 2 {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want block and tail warnings",
				"run",
				warnings,
			)
		}
	})

	t.Run("positive count always invokes on a completing start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  items.fill(start, 1) do
    takes_int("block reachable")
    raise "stop"
  end
  takes_int("tail unreachable")
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "argument value expected int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the block warning",
				"run",
				warnings,
			)
		}
	})

	t.Run("invalid count rejects before the block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  begin
    items.fill(start, "invalid") do
      takes_int("block unreachable")
      items << "block mutation"
    end
  rescue
    nil
  end
  items << "later"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want only the reachable tail write",
				"run",
				warnings,
			)
		}
	})

	t.Run("dynamic count preserves a nonpositive start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>, count: int)
  items.fill(0, count) do
    raise "stop"
  end
  items << "later"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the reachable tail write",
				"run",
				warnings,
			)
		}
	})

	t.Run("dynamic count may pad a positive start", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int>, count: int)
  items.fill(5, count) do
    raise "stop"
  end
  items << "later"
end
`)
		requireNoCheckWarnings(t, script)
	})

	t.Run("nullable bound survives possible dynamic-count padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def run(items: array<int | nil>, count: int)
  items.fill(5, count) do
    raise "stop"
  end
  items << "later"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to items expected element int | nil, got string") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want the reachable tail write",
				"run",
				warnings,
			)
		}
	})

	t.Run("bare dynamic numeric start may invoke or skip without padding", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def run(items: array<int>, start: int)
  items.fill(start) do
    takes_int("block reachable")
    raise "stop"
  end
  items << "tail reachable"
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if len(warnings) != 2 {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %#v, want block and tail warnings",
				"run",
				warnings,
			)
		}
	})
}

func TestCheckArrayFillNoncompletingBlockPreservesReceiverThroughRescue(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>)
  begin
    items.fill() do
      raise "stop"
    end
  rescue
    nil
  end
  items << "later"
  items
end
`)
	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf(
			"CheckWarningsForFunction(%q) = %#v, want the later incompatible write",
			"run",
			warnings,
		)
	}
	got := callScript(t, context.Background(), script, "run", []Value{
		NewArray([]Value{NewInt(1)}),
	}, CallOptions{})
	want := NewArray([]Value{NewInt(1), NewString("later")})
	if !got.Equal(want) {
		t.Fatalf("run([1]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayWriteContradictionKeepsWitness(t *testing.T) {
	t.Parallel()

	// The reported append really lands, so the written value is a witnessed
	// element afterwards: the corrupted array still satisfies a boundary the
	// witness does not contradict, and the write site stays the only report.
	script := compileScriptDefault(t, `
def strings(values: array<string>)
  values
end

def f(items: array<int>)
  items << "bad"
  strings(items)
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarnings() = %#v, want only the write contradiction", warnings)
	}
}

func TestCheckArrayWritesKeepForwardedValuesAndDeclaredBoundsConsistent(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

class UpdatedReceiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

class DynamicReceiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Strict
  def check(value)
    takes_int(value)
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def exact_index(names: array<symbol>)
  names[0] = :third
  UpdatedReceiver.new.send(*names, "bad")
end

def exact_alias(names: array<symbol>)
  copy = names
  names[0] = :third
  UpdatedReceiver.new.send(*copy, "bad")
end

def retain_index_bound(names: array<symbol>)
  names[0] = :third
  names << 1
end

def retain_alias_bound(names: array<symbol>)
  copy = names
  names[0] = :third
  copy << 1
end

def rebound_alias(names: array<symbol>)
  copy = names
  copy = Strict
  names << :third
  copy.new.check("bad")
end

def dynamic_index(names: array<symbol>, index: int)
  names[index] = :third
  DynamicReceiver.new.send(*names, "bad")
end

def short_circuit_index(names: array<symbol>)
  names[0] ||= :third
  UpdatedReceiver.new.send(*names, "bad")
end

def retain_dynamic_index_bound(names: array<symbol>, index: int)
  names[index] = :third
  names << 1
end

def prepend_name(names: array<symbol>)
  names.prepend(:third)
  DynamicReceiver.new.send(*names, "bad")
end

def escaped_shovel(names: array<symbol>)
  Producer.new.consume(names << :extra)
  DynamicReceiver.new.send(*names, "bad")
end

def mutate_name_in_loop(names: array<symbol>, flag: bool)
  while flag
    names.prepend(:third)
    break
  end
  DynamicReceiver.new.send(*names, "bad")
end

def shovel_name_in_loop(names: array<symbol>, flag: bool)
  while flag
    names << :third
    break
  end
  DynamicReceiver.new.send(*names, "bad")
end

def retain_shovel_loop_bound(names: array<symbol>, flag: bool)
  while flag
    names << :third
    break
  end
  names << 1
end

def run(index: int, flag: bool)
  exact_index([:first])
  exact_alias([:first])
  retain_index_bound([:first])
  retain_alias_bound([:first])
  dynamic_index([:first], index)
  short_circuit_index([:first])
  retain_dynamic_index_bound([:first], index)
  prepend_name([:first])
  escaped_shovel([:first])
  mutate_name_in_loop([:first], flag)
  shovel_name_in_loop([:first], flag)
  retain_shovel_loop_bound([:first], flag)
  rebound_alias([:first])
end`)

	gotWarnings := script.CheckWarningsForFunction("run")
	warnings := strings.Join(checkWarningMessages(gotWarnings), "\n")
	// Two distinct targets: the third diagnostic repeated one of them
	// exactly -- same function, position, and text -- and identical copies
	// are collapsed before reporting.
	if got := strings.Count(warnings, "call to takes_int argument value expected int, got string"); got != 2 {
		t.Fatalf("forwarded diagnostics = %d in %#v, want 2 exact targets", got, gotWarnings)
	}
	if got := strings.Count(warnings, "write to names expected element symbol, got int"); got != 3 {
		t.Fatalf("receiver write diagnostics = %d in %q, want 3 retained bounds", got, warnings)
	}
	if got := strings.Count(warnings, "write to copy expected element symbol, got int"); got != 1 {
		t.Fatalf("alias write diagnostics = %d in %q, want 1 retained alias bound", got, warnings)
	}
}

func TestCheckArrayWritesInvalidateOnlyDependentForwardedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		want         string
		wantFunction string
	}{
		{
			name: "retained child stays exact when its parent changes",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def third(value)
    takes_int(value)
  end
end

def retained_child(items: array<array<symbol>>, child: array<symbol>)
  items[0] = child
  Receiver.new.send(*child, "bad")
end

def run()
  retained_child([[:first]], [:third])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "nested parent write updates a projected child alias",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def nested_write(items: array<array<symbol>>)
  child = items[0]
  items[0][0] = :third
  Receiver.new.send(*child, "bad")
end

def run()
  nested_write([[:first]])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "replacing a parent element preserves the detached child",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def replace_parent(items: array<array<symbol>>)
  child = items[0]
  items[0] = [:third]
  Receiver.new.send(*child, "bad")
end

def run()
  replace_parent([[:first]])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "destructured aliases share exact mutations",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def destructured(names: array<symbol>)
  copy, ignored = [names, 0]
  names[0] = :third
  Receiver.new.send(*copy, "bad")
end

def run()
  destructured([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "destructuring captures every value before rebinding targets",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def simultaneous(names: array<symbol>)
  names, copy = [[:third], names]
  Receiver.new.send(*copy, "bad")
end

def run()
  simultaneous([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "duplicate destructure targets keep the last value",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def duplicate_target()
  names, names = [[:first], [:third]]
  Receiver.new.send(*names, "bad")
end

def run()
  duplicate_target()
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "shared call arguments keep alias identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def mutate_first(a: array<symbol>, b: array<symbol>)
  a[0] = :third
  Receiver.new.send(*b, "bad")
end

def run()
  names = [:first]
  mutate_first(names, names)
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "logical assignment keeps selected alias identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def logical_alias(names: array<symbol>)
  copy = nil
  copy ||= names
  names[0] = :third
  Receiver.new.send(*copy, "bad")
end

def run()
  logical_alias([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "no-op insert keeps the exact forwarded name",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end
end

def no_op_mutator(names)
  names.push()
  names.insert(0)
  Receiver.new.send(*names, "bad")
end

def run()
  no_op_mutator([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "rebound alias keeps its unrelated class identity",
			source: `def takes_int(value: int)
  value
end

class Strict
  def check(value)
    takes_int(value)
  end
end

def rebound_alias(names: array<symbol>)
  copy = names
  copy = Strict
  names << :third
  copy.new.check("bad")
end

def run()
  rebound_alias([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Strict#check",
		},
		{
			name: "escaped shovel does not retain a stale forwarded name",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def escaped_shovel(names: array<symbol>)
  Producer.new.consume(names << :extra)
  Receiver.new.send(*names, "bad")
end

def run()
  escaped_shovel([:first])
end`,
		},
		{
			name: "parenless member mutation clears a shovel receiver value",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def parenless_mutation()
  names = [:first]
  (names << :third).shift
  Receiver.new.send(*names, "bad")
end

def run()
  parenless_mutation()
end`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := compileScript(t, tc.source).CheckWarningsForFunction("run")
			got := strings.Join(checkWarningMessages(warnings), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, want none", "run", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, tc.want)
			}
			if len(warnings) != 1 || warnings[0].Function != tc.wantFunction {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want one warning in %q", "run", warnings, tc.wantFunction)
			}
		})
	}
}

func TestCheckArrayWriteDirectAliasTransfersRelations(t *testing.T) {
	t.Parallel()

	checker := &scriptChecker{
		scopes: []map[string]struct{}{{
			"base":   {},
			"child":  {},
			"copy":   {},
			"parent": {},
			"source": {},
		}},
		localTypes: []checkTypeFrame{{
			"base":   checkTypeArray,
			"child":  checkTypeArray,
			"copy":   nil,
			"parent": checkTypeArray,
			"source": checkTypeArray,
		}},
		localClassValues: []checkClassValueFrame{nil},
	}
	checker.linkContainerIdentityAlias("source", "base")
	checker.linkStaticValueAlias("source", "base")
	checker.linkContainerAlias("base", "child")
	checker.linkStaticValueDependency("child", "base")
	checker.linkContainerAlias("base", "parent")
	checker.linkStaticValueDependency("base", "parent")

	transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "source"})
	checker.advanceLocalBindingGeneration("copy")
	checker.bindLocalType("copy", checkTypeArray)
	checker.applyContainerAliasTransfer("copy", transfer)
	checker.advanceLocalBindingGeneration("source")

	tests := []struct {
		name      string
		relations map[string]map[string]checkBindingEdge
		from      string
		to        string
	}{
		{
			name:      "definite identity",
			relations: checker.containerIdentityAliases,
			from:      "copy",
			to:        "base",
		},
		{
			name:      "may alias reachability",
			relations: checker.typeAliases,
			from:      "copy",
			to:        "child",
		},
		{
			name:      "incoming static dependency",
			relations: checker.staticValueDependents,
			from:      "child",
			to:        "copy",
		},
		{
			name:      "outgoing static dependency",
			relations: checker.staticValueDependents,
			from:      "copy",
			to:        "parent",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			edge, exists := tc.relations[tc.from][tc.to]
			if !exists || !checker.bindingEdgeCurrent(tc.from, tc.to, edge) {
				t.Errorf("transferred relation %q -> %q = %#v, %t, want current", tc.from, tc.to, edge, exists)
			}
		})
	}
}

func TestCheckArrayWriteLogicalAssignmentBindingGeneration(t *testing.T) {
	t.Parallel()

	nullableIntArray := &TypeExpr{
		Kind:     TypeArray,
		Nullable: true,
		TypeArgs: []*TypeExpr{checkTypeInt},
	}
	tests := []struct {
		name         string
		operator     TokenType
		current      *TypeExpr
		fact         *logicalAssignmentTargetFact
		wantAdvance  bool
		wantAlias    bool
		wantIdentity bool
		wantStatic   bool
	}{
		{
			name:     "known truthy or assignment preserves identity",
			operator: tokenOrAssign,
			current:  checkTypeArray,
			fact: &logicalAssignmentTargetFact{
				current: checkTypeArray,
				known:   true,
			},
			wantAlias:    true,
			wantIdentity: true,
			wantStatic:   true,
		},
		{
			name:     "known truthy and assignment replaces identity",
			operator: tokenAndAssign,
			current:  checkTypeArray,
			fact: &logicalAssignmentTargetFact{
				current:      checkTypeArray,
				rhsReachable: true,
				known:        true,
			},
			wantAdvance: true,
		},
		{
			name:     "unknown or assignment retains a possible alias",
			operator: tokenOrAssign,
			current:  nullableIntArray,
			fact: &logicalAssignmentTargetFact{
				current:      nullableIntArray,
				rhsReachable: true,
			},
			wantAdvance: true,
			wantAlias:   true,
			wantStatic:  true,
		},
		{
			name:     "unknown and assignment replaces a possible container",
			operator: tokenAndAssign,
			current:  nullableIntArray,
			fact: &logicalAssignmentTargetFact{
				current:      nullableIntArray,
				rhsReachable: true,
			},
			wantAdvance: true,
		},
		{
			name:         "collection fallback preserves known truthy identity",
			operator:     tokenOrAssign,
			current:      checkTypeArray,
			wantAlias:    true,
			wantIdentity: true,
			wantStatic:   true,
		},
		{
			name:        "collection fallback replaces known truthy identity",
			operator:    tokenAndAssign,
			current:     checkTypeArray,
			wantAdvance: true,
		},
		{
			name:        "collection fallback retains an unknown possible alias",
			operator:    tokenOrAssign,
			current:     nullableIntArray,
			wantAdvance: true,
			wantAlias:   true,
			wantStatic:  true,
		},
		{
			name:        "collection fallback and assignment replaces an unknown container",
			operator:    tokenAndAssign,
			current:     nullableIntArray,
			wantAdvance: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checker := &scriptChecker{
				scopes: []map[string]struct{}{{
					"copy":  {},
					"items": {},
				}},
				localTypes: []checkTypeFrame{{
					"copy":  checkTypeArray,
					"items": tc.current,
				}},
				localClassValues: []checkClassValueFrame{nil},
			}
			checker.linkContainerIdentityAlias("items", "copy")
			checker.linkStaticValueAlias("items", "copy")
			stmt := &AssignStmt{
				Target:   &Identifier{Name: "items"},
				Operator: tc.operator,
				Value: &ArrayLiteral{
					Elements: []Expression{&IntegerLiteral{Value: 1}},
				},
			}

			fact := tc.fact
			if fact != nil {
				captured := *fact
				if tc.operator == tokenOrAssign && !captured.known {
					captured.priorAliasTransfer = checker.captureContainerAliasTransfer(stmt.Target)
				}
				fact = &captured
			}
			checker.inferAssignStatementTypes("", stmt, nil, fact)

			if got := checker.localBindingGenerations["items"]; (got != 0) != tc.wantAdvance {
				t.Errorf("binding generation = %d, want advance %t", got, tc.wantAdvance)
			}
			aliasEdge, aliasExists := checker.typeAliases["items"]["copy"]
			aliasCurrent := aliasExists && checker.bindingEdgeCurrent("items", "copy", aliasEdge)
			if aliasCurrent != tc.wantAlias {
				t.Errorf("possible alias current = %t, want %t", aliasCurrent, tc.wantAlias)
			}
			identityEdge, identityExists := checker.containerIdentityAliases["items"]["copy"]
			identityCurrent := identityExists && checker.bindingEdgeCurrent("items", "copy", identityEdge)
			if identityCurrent != tc.wantIdentity {
				t.Errorf("identity alias current = %t, want %t", identityCurrent, tc.wantIdentity)
			}
			forwardEdge, forwardExists := checker.staticValueDependents["items"]["copy"]
			forwardCurrent := forwardExists && checker.bindingEdgeCurrent("items", "copy", forwardEdge)
			reverseEdge, reverseExists := checker.staticValueDependents["copy"]["items"]
			reverseCurrent := reverseExists && checker.bindingEdgeCurrent("copy", "items", reverseEdge)
			if forwardCurrent != tc.wantStatic || reverseCurrent != tc.wantStatic {
				t.Errorf("static dependencies current = (%t, %t), want (%t, %t)",
					forwardCurrent, reverseCurrent, tc.wantStatic, tc.wantStatic)
			}
		})
	}
}
