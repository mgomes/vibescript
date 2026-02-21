package vibes

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func compileTestProgram(t *testing.T, rel string) *Script {
	t.Helper()
	path := filepath.Join("..", "tests", rel)
	return compileScriptFromFileDefault(t, path)
}

func compileComplexExample(t *testing.T, rel string) *Script {
	return compileComplexExampleWithConfig(t, rel, Config{})
}

func compileComplexExampleWithConfig(t *testing.T, rel string, cfg Config) *Script {
	t.Helper()
	path := filepath.Join("..", "tests", "complex", rel)
	return compileScriptFromFileWithConfig(t, cfg, path)
}

func TestComplexExamplesCompile(t *testing.T) {
	engine := MustNewEngine(Config{})
	files := []string{
		"tests/complex/analytics.vibe",
		"tests/complex/durations.vibe",
		"tests/complex/finance.vibe",
		"tests/complex/strings.vibe",
		"tests/complex/loops.vibe",
		"tests/complex/typed.vibe",
		"tests/complex/pipeline.vibe",
		"tests/complex/massive.vibe",
		"tests/complex/chudnovsky.vibe",
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			full := filepath.Join("..", path)
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("read %s: %v", full, err)
			}
			if _, err := engine.Compile(string(data)); err != nil {
				t.Fatalf("compile %s: %v", path, err)
			}
		})
	}
}

func TestComplexExamplesRun(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		function string
		args     []Value
		globals  map[string]Value
		want     Value
		check    func(*testing.T, Value)
	}{
		{
			name:     "analytics/run",
			file:     "analytics.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"total":   intVal(21),
				"top":     intVal(9),
				"names":   arrayVal(strVal("alex"), strVal("bea"), strVal("cam")),
				"average": floatVal(7),
				"active": arrayVal(
					hashVal(map[string]Value{"name": strVal("alex"), "score": intVal(5), "last_seen": intVal(100)}),
					hashVal(map[string]Value{"name": strVal("cam"), "score": intVal(7), "last_seen": intVal(120)}),
				),
				"leaders": arrayVal(),
			}),
		},
		{
			name:     "durations/run",
			file:     "durations.vibe",
			function: "run",
			check: func(t *testing.T, got Value) {
				t.Helper()
				if got.Kind() != KindHash {
					t.Fatalf("expected hash, got %v", got.Kind())
				}
				h := got.Hash()
				if h["span"].Int() != 5400 {
					t.Fatalf("span mismatch: %v", h["span"])
				}
				if h["iso"].String() != "PT1H30M" {
					t.Fatalf("iso mismatch: %v", h["iso"])
				}
				shifted := h["shifted"]
				if shifted.Kind() != KindArray || len(shifted.Array()) != 2 {
					t.Fatalf("shifted mismatch: %v", shifted)
				}
				readable := h["readable"]
				if readable.Kind() != KindString || !strings.Contains(readable.String(), " -> ") {
					t.Fatalf("readable mismatch: %v", readable)
				}
			},
		},
		{
			name:     "finance/run",
			file:     "finance.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"fee": mustMoney("11.00 USD"),
				"applied": arrayVal(
					mustMoney("3.00 USD"),
					mustMoney("4.50 USD"),
				),
				"average": hashVal(map[string]Value{
					"total": mustMoney("5.50 USD"),
					"count": intVal(2),
				}),
			}),
		},
		{
			name:     "strings/run",
			file:     "strings.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"slug":     strVal("hello-world"),
				"initials": strVal("AL"),
				"title":    strVal("vibes"),
				"wrapped":  arrayVal(strVal("one two"), strVal("three"), strVal("four")),
			}),
		},
		{
			name:     "loops/run",
			file:     "loops.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"sum":       intVal(10),
				"flat":      arrayVal(intVal(1), intVal(2), intVal(3), intVal(4)),
				"countdown": arrayVal(intVal(3), intVal(2), intVal(1), intVal(0)),
			}),
		},
		{
			name:     "typed/run",
			file:     "typed.vibe",
			function: "run",
			check: func(t *testing.T, got Value) {
				t.Helper()
				adjusted := time.Unix(90, 0).UTC()
				want := hashVal(map[string]Value{
					"announce":  strVal("score: alex => 9"),
					"adjusted":  NewTime(adjusted),
					"maybe_nil": nilVal(),
					"maybe_val": intVal(7),
					"user": hashVal(map[string]Value{
						"name":   strVal("alex"),
						"active": boolVal(true),
					}),
				})
				assertValueEqual(t, got, want)
			},
		},
		{
			name:     "pipeline/run",
			file:     "pipeline.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"normalized": arrayVal(
					hashVal(map[string]Value{"name": strVal("a"), "score": intVal(100)}),
					hashVal(map[string]Value{"name": strVal("b"), "score": intVal(80)}),
					hashVal(map[string]Value{"name": strVal("c"), "score": intVal(40)}),
				),
				"filtered": arrayVal(
					hashVal(map[string]Value{"name": strVal("a"), "score": intVal(120)}),
					hashVal(map[string]Value{"name": strVal("b"), "score": intVal(80)}),
				),
				"top": arrayVal(
					hashVal(map[string]Value{"name": strVal("a"), "score": intVal(100)}),
					hashVal(map[string]Value{"name": strVal("b"), "score": intVal(80)}),
				),
			}),
		},
		{
			name:     "chudnovsky/run",
			file:     "chudnovsky.vibe",
			function: "run",
			check: func(t *testing.T, got Value) {
				t.Helper()
				if got.Kind() != KindHash {
					t.Fatalf("expected hash, got %v", got.Kind())
				}
				coarse, ok := got.Hash()["coarse"]
				if !ok || coarse.Kind() != KindFloat {
					t.Fatalf("missing coarse")
				}
				precise, ok := got.Hash()["precise"]
				if !ok || precise.Kind() != KindFloat {
					t.Fatalf("missing precise")
				}
				if math.Abs(precise.Float()-math.Pi) > 1e-4 {
					t.Fatalf("precise pi off: %g", precise.Float())
				}
			},
		},
		{
			name:     "massive/run",
			file:     "massive.vibe",
			function: "run",
			want:     intVal(31_375),
		},
		{
			name:     "yield_basics/run",
			file:     "yield_basics.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"count":   intVal(2),
				"doubled": intVal(42),
				"sum":     intVal(70),
			}),
		},
		{
			name:     "with_blocks/run",
			file:     "with_blocks.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"sum":        intVal(15),
				"doubled":    arrayVal(intVal(2), intVal(4), intVal(6), intVal(8), intVal(10)),
				"first_even": intVal(2),
			}),
		},
		{
			name:     "advanced_blocks/run",
			file:     "advanced_blocks.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"captured":  arrayVal(intVal(1), intVal(2)),
				"nested":    intVal(36),
				"defaulted": intVal(7),
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := compileComplexExample(t, tc.file)
			opts := CallOptions{}
			if tc.globals != nil {
				opts.Globals = tc.globals
			}
			result, err := script.Call(context.Background(), tc.function, tc.args, opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, result)
				return
			}
			assertValueEqual(t, result, tc.want)
		})
	}
}

func TestProgramFixtures(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		function string
		want     Value
	}{
		{
			name:     "runtime_stress",
			file:     "runtime_stress.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"total":      intVal(500_500),
				"square_sum": intVal(55),
				"iso":        strVal("PT3M30S"),
				"shifted":    strVal("1970-01-01T00:03:30Z"),
				"minutes":    intVal(3),
				"seconds":    intVal(30),
			}),
		},
		{
			name:     "typing_fixture",
			file:     "typing_fixture.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"add_nil": nilVal(),
				"add_val": intVal(7),
				"kw":      strVal("alex-7"),
			}),
		},
		{
			name:     "collections_fixture",
			file:     "collections_fixture.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"initial": strVal("A"),
				"sizes": hashVal(map[string]Value{
					"arr": intVal(2),
					"str": intVal(5),
				}),
			}),
		},
		{
			name:     "blocks/block_arity",
			file:     "blocks/block_arity.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"extra":   arrayVal(intVal(1)),
				"missing": arrayVal(intVal(9), nilVal(), nilVal()),
			}),
		},
		{
			name:     "blocks/block_closure",
			file:     "blocks/block_closure.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"total":  intVal(6),
				"shadow": intVal(5),
				"mapped": intVal(16),
			}),
		},
		{
			name:     "blocks/reduce_single",
			file:     "blocks/reduce_single.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"single": intVal(7),
				"hash": hashVal(map[string]Value{
					"one": intVal(2),
					"two": intVal(4),
				}),
			}),
		},
		{
			name:     "blocks/instance_block_context",
			file:     "blocks/instance_block_context.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"total": intVal(10),
				"count": intVal(10),
			}),
		},
		{
			name:     "classes/people",
			file:     "classes/people.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"name":   strVal("John"),
				"age":    intVal(1),
				"reveal": strVal("shh"),
			}),
		},
		{
			name:     "classes/counter",
			file:     "classes/counter.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"before": intVal(0),
				"after":  intVal(3),
			}),
		},
		{
			name:     "classes/point",
			file:     "classes/point.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"before": hashVal(map[string]Value{"x": intVal(2), "y": intVal(3)}),
				"after":  hashVal(map[string]Value{"x": intVal(9), "y": intVal(3)}),
			}),
		},
		{
			name:     "classes/privacy",
			file:     "classes/privacy.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"internal": intVal(42),
			}),
		},
		{
			name:     "classes/self_calls",
			file:     "classes/self_calls.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"class_call":    intVal(10),
				"instance_call": intVal(14),
			}),
		},
		{
			name:     "classes/setter",
			file:     "classes/setter.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"before": intVal(0),
				"after":  intVal(5),
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := compileTestProgram(t, tc.file)
			result, err := script.Call(context.Background(), tc.function, nil, CallOptions{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertValueEqual(t, result, tc.want)
		})
	}
}

func TestBlockErrorCases(t *testing.T) {
	script := compileTestProgram(t, "blocks/error_cases.vibe")

	checkErr := func(fn, contains string) {
		t.Helper()
		_, err := script.Call(context.Background(), fn, nil, CallOptions{})
		if err == nil {
			t.Fatalf("%s: expected error", fn)
		}
		if !strings.Contains(err.Error(), contains) {
			t.Fatalf("%s: unexpected error %v", fn, err)
		}
	}

	checkErr("each_without_block", "requires a block")
	checkErr("map_without_block", "requires a block")
	checkErr("reduce_empty_without_init", "requires an initial value")

	val, err := script.Call(context.Background(), "reduce_empty_with_init", nil, CallOptions{})
	if err != nil {
		t.Fatalf("reduce_empty_with_init: unexpected error %v", err)
	}
	assertValueEqual(t, val, intVal(10))
}

func TestBlockErrorPropagation(t *testing.T) {
	script := compileTestProgram(t, "blocks/block_error_propagation.vibe")
	_, err := script.Call(context.Background(), "explode", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected error from block")
	}
	if !strings.Contains(err.Error(), "unknown_method") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComplexExamplesStress(t *testing.T) {
	highQuota := Config{StepQuota: 5_000_000}
	massive := compileComplexExampleWithConfig(t, "massive.vibe", highQuota)
	val, err := massive.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("massive run failed: %v", err)
	}
	if val.Kind() != KindInt || val.Int() != 31375 {
		t.Fatalf("unexpected massive sum: %v", val)
	}

	piScript := compileComplexExampleWithConfig(t, "chudnovsky.vibe", highQuota)
	for i := range 50 {
		val, err := piScript.Call(context.Background(), "pi_approx_precise", []Value{intVal(5_000)}, CallOptions{})
		if err != nil {
			t.Fatalf("pi_approx_precise run %d failed: %v", i, err)
		}
		if val.Kind() != KindFloat {
			t.Fatalf("pi_approx_precise returned non-float: %v", val.Kind())
		}
		if math.Abs(val.Float()-math.Pi) > 1e-6 {
			t.Fatalf("pi approximation off: %g", val.Float())
		}
	}
}

// TestAllVibeFilesCompileAndRun discovers all .vibe files in tests/ and ensures
// they compile and execute their run() function without error.
func TestAllVibeFilesCompileAndRun(t *testing.T) {
	testsDir := filepath.Join("..", "tests")
	var files []string
	err := filepath.Walk(testsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".vibe") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tests dir: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no .vibe files found in tests/")
	}

	engine := MustNewEngine(Config{StepQuota: 5_000_000})
	for _, path := range files {
		rel, _ := filepath.Rel(testsDir, path)
		t.Run(rel, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			script, err := engine.Compile(string(source))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if fn, ok := script.Function("run"); ok {
				_, err := script.Call(context.Background(), fn.Name, nil, CallOptions{})
				if err != nil {
					t.Fatalf("run: %v", err)
				}
			}
		})
	}
}

func TestRuntimeErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/runtime.vibe")

	requireCallErrorContains(t, script, "div_by_zero", nil, CallOptions{}, "division by zero")
	requireCallErrorContains(t, script, "mod_by_zero", nil, CallOptions{}, "modulo by zero")
	requireCallErrorContains(t, script, "array_index_out_of_bounds", nil, CallOptions{}, "index out of bounds")
	requireCallErrorContains(t, script, "string_index_out_of_bounds", nil, CallOptions{}, "index out of bounds")
	requireCallErrorContains(t, script, "method_missing", nil, CallOptions{}, "unknown")
	requireCallErrorContains(t, script, "nil_method", nil, CallOptions{}, "nil")
}

func TestTypeErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/types.vibe")

	requireCallErrorContains(t, script, "sub_mismatch", nil, CallOptions{}, "unsupported")
	requireCallErrorContains(t, script, "mul_mismatch", nil, CallOptions{}, "unsupported")
	requireCallErrorContains(t, script, "div_mismatch", nil, CallOptions{}, "unsupported")
	requireCallErrorContains(t, script, "unary_mismatch", nil, CallOptions{}, "unsupported")
	requireCallErrorContains(t, script, "arg_type_mismatch", []Value{strVal("wrong")}, CallOptions{}, "argument n expected int, got string")
	requireCallErrorContains(t, script, "return_type_mismatch", nil, CallOptions{}, "return value for return_type_mismatch expected int, got string")
}

func TestAttributeErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/attributes.vibe")

	requireCallErrorContains(t, script, "set_readonly", nil, CallOptions{}, "read-only property")
}

func TestYieldErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/yield.vibe")

	requireCallErrorContains(t, script, "yield_without_block", nil, CallOptions{}, "no block given")

	// run function should work since it uses blocks correctly
	val, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}
	assertValueEqual(t, val, hashVal(map[string]Value{
		"count": intVal(3),
	}))
}

func TestArgumentErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/arguments.vibe")

	requireCallErrorContains(t, script, "too_few_args", nil, CallOptions{}, "argument")
	requireCallErrorContains(t, script, "too_many_args", nil, CallOptions{}, "argument")

	// run function should work
	val, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}
	if val.Kind() != KindHash {
		t.Fatalf("run: expected hash, got %v", val.Kind())
	}
	h := val.Hash()
	if h["a"].Int() != 15 {
		t.Fatalf("run: a mismatch: %v", h["a"])
	}
	if h["b"].Int() != 25 {
		t.Fatalf("run: b mismatch: %v", h["b"])
	}
}

func TestBlockEnvironmentIsolation(t *testing.T) {
	source := `
def run
  results = []
  nums = [1, 2, 3]
  nums.each do |x|
    local = x * 10
    results = results.push(local)
  end
  results
end
`
	script := compileScriptDefault(t, source)

	for i := range 100 {
		result, err := script.Call(context.Background(), "run", nil, CallOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		want := arrayVal(intVal(10), intVal(20), intVal(30))
		assertValueEqual(t, result, want)
	}
}

func TestBlockEnvironmentNoLeakBetweenCalls(t *testing.T) {
	source := `
def transform(items)
  items.map do |x|
    temp = x + 100
    temp * 2
  end
end

def run
  a = transform([1, 2, 3])
  b = transform([10, 20, 30])
  { a: a, b: b }
end
`
	script := compileScriptDefault(t, source)

	for i := range 50 {
		result, err := script.Call(context.Background(), "run", nil, CallOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		h := result.Hash()
		wantA := arrayVal(intVal(202), intVal(204), intVal(206))
		wantB := arrayVal(intVal(220), intVal(240), intVal(260))
		assertValueEqual(t, h["a"], wantA)
		assertValueEqual(t, h["b"], wantB)
	}
}
