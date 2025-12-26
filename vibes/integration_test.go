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
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	engine := NewEngine(Config{})
	script, err := engine.Compile(string(source))
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	return script
}

func compileComplexExample(t *testing.T, rel string) *Script {
	return compileComplexExampleWithConfig(t, rel, Config{})
}

func compileComplexExampleWithConfig(t *testing.T, rel string, cfg Config) *Script {
	t.Helper()
	path := filepath.Join("..", "tests", "complex", rel)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	engine := NewEngine(cfg)
	script, err := engine.Compile(string(source))
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	return script
}

func TestComplexExamplesCompile(t *testing.T) {
	engine := NewEngine(Config{})
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
		path := path
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
	}

	for _, tc := range cases {
		tc := tc
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
	}

	for _, tc := range cases {
		tc := tc
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
	for i := 0; i < 50; i++ {
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

	engine := NewEngine(Config{StepQuota: 5_000_000})
	for _, path := range files {
		path := path
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
