package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScriptCallIsolatesBuiltinObjectsFromScriptMutation(t *testing.T) {
	script := compileScriptDefault(t, `def poison
  JSON.parse = "poison"
end

def parse_name
  JSON.parse("{\"name\":\"alex\"}")[:name]
end`)

	callScript(t, context.Background(), script, "poison", nil, CallOptions{})
	result := callScript(t, context.Background(), script, "parse_name", nil, CallOptions{})
	if !result.Equal(NewString("alex")) {
		t.Fatalf("parse_name after script-mutated JSON.parse = %#v, want alex", result)
	}
}

func TestBuiltinObjectCloneIsLazyAndCallLocal(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	root := newEnv(nil)
	engine.attachBuiltins(root, 0)

	if root.parent == nil || !root.parent.frozen {
		t.Fatalf("expected root to be attached to frozen builtin proto")
	}
	if _, ok := root.statics["JSON"]; ok {
		t.Fatalf("JSON was eagerly cloned into the call root")
	}
	protoJSON, ok := root.parent.statics["JSON"]
	if !ok || protoJSON.Kind() != KindObject {
		t.Fatalf("builtin proto JSON = (%#v, %t), want object", protoJSON, ok)
	}

	callJSON, ok := root.Get("JSON")
	if !ok || callJSON.Kind() != KindObject {
		t.Fatalf("root.Get(JSON) = (%#v, %t), want object", callJSON, ok)
	}
	if _, ok := root.statics["JSON"]; !ok {
		t.Fatalf("JSON was not cloned into the call root after first access")
	}

	callJSON.Hash()["parse"] = NewString("poison")
	if valueBuiltin(protoJSON.Hash()["parse"]) == nil {
		t.Fatalf("mutating call-local JSON changed the frozen builtin proto")
	}
}

func TestScriptCallReturnsIsolatedBuiltinObjects(t *testing.T) {
	script := compileScriptDefault(t, `def leak
  JSON
end

def parse_name
  JSON.parse("{\"name\":\"alex\"}")[:name]
end`)

	leaked := callScript(t, context.Background(), script, "leak", nil, CallOptions{})
	leaked.Hash()["parse"] = NewString("poison")

	result := callScript(t, context.Background(), script, "parse_name", nil, CallOptions{})
	if !result.Equal(NewString("alex")) {
		t.Fatalf("parse_name after host-mutated returned JSON = %#v, want alex", result)
	}
}

func TestScriptCallReturnsIsolatedBuiltinFunctions(t *testing.T) {
	script := compileScriptDefault(t, `def leak
  to_int
end

def convert
  to_int("7")
end`)

	leaked := callScript(t, context.Background(), script, "leak", nil, CallOptions{})
	valueBuiltin(leaked).Fn = func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewInt(99), nil
	}

	result := callScript(t, context.Background(), script, "convert", nil, CallOptions{})
	if !result.Equal(NewInt(7)) {
		t.Fatalf("convert after host-mutated returned to_int = %#v, want 7", result)
	}
}

func TestValueEqualityForContainersDoesNotPanic(t *testing.T) {
	script := compileScriptDefault(t, `def facts
  a = []
  h = {}
  {
    same_array: a == a,
    same_hash: h == h,
    equal_arrays: [1, { nested: 2 }] == [1, { nested: 2 }],
    equal_hashes: { items: [1, 2] } == { items: [1, 2] },
    different_arrays: [1] == [2],
    different_hashes: { value: 1 } == { value: 2 }
  }
end`)

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("container equality panicked: %v", recovered)
		}
	}()

	result := callScript(t, context.Background(), script, "facts", nil, CallOptions{})
	facts := result.Hash()
	for _, key := range []string{"same_array", "same_hash", "equal_arrays", "equal_hashes"} {
		if got := facts[key]; got.Kind() != KindBool || !got.Bool() {
			t.Fatalf("%s = %#v, want true", key, got)
		}
	}
	for _, key := range []string{"different_arrays", "different_hashes"} {
		if got := facts[key]; got.Kind() != KindBool || got.Bool() {
			t.Fatalf("%s = %#v, want false", key, got)
		}
	}
}

func TestValueStringHandlesCycles(t *testing.T) {
	if os.Getenv("VIBES_CONTAINMENT_SUBPROCESS") == "string-cycle" {
		script := compileScriptDefault(t, `def run
  h = {}
  h[:self] = h
  assert false, h
end`)

		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		if err == nil {
			t.Fatalf("expected cyclic assertion to fail")
		}
		if !strings.Contains(err.Error(), "<cycle>") {
			t.Fatalf("cyclic assertion error = %q, want cycle marker", err.Error())
		}
		return
	}

	runContainmentSubprocess(t, "string-cycle", "TestValueStringHandlesCycles")
}

func TestArrayFlattenRejectsCycles(t *testing.T) {
	if os.Getenv("VIBES_CONTAINMENT_SUBPROCESS") == "flatten-cycle" {
		script := compileScriptDefault(t, `def run(a)
  a.flatten
end`)

		items := make([]Value, 1)
		cyclic := NewArray(items)
		items[0] = cyclic

		_, err := script.Call(context.Background(), "run", []Value{cyclic}, CallOptions{})
		requireErrorContains(t, err, "array.flatten does not support cyclic structures")
		return
	}

	runContainmentSubprocess(t, "flatten-cycle", "TestArrayFlattenRejectsCycles")
}

func TestStringRegexMembersEnforceSizeGuards(t *testing.T) {
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 8 << 20}, `def match_text(text, pattern)
  text.match(pattern)
end

def scan_text(text, pattern)
  text.scan(pattern)
end

def sub_text(text, pattern, replacement)
  text.sub(pattern, replacement, regex: true)
end

def gsub_text(text, pattern, replacement)
  text.gsub(pattern, replacement, regex: true)
end`)

	largePattern := strings.Repeat("a", maxRegexPatternSize+1)
	requireCallErrorContains(t, script, "match_text", []Value{NewString("aaa"), NewString(largePattern)}, CallOptions{}, "string.match pattern exceeds limit")

	largeText := strings.Repeat("a", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "scan_text", []Value{NewString(largeText), NewString("a")}, CallOptions{}, "string.scan text exceeds limit")

	largeReplacement := strings.Repeat("x", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "sub_text", []Value{NewString("a"), NewString("a"), NewString(largeReplacement)}, CallOptions{}, "string.sub replacement exceeds limit")

	outputReplacement := strings.Repeat("x", maxRegexInputBytes)
	requireCallErrorContains(t, script, "gsub_text", []Value{NewString("aa"), NewString("a"), NewString(outputReplacement)}, CallOptions{}, "string.gsub output exceeds limit")
}

func TestScriptInspectionAPIsReturnIsolatedSnapshots(t *testing.T) {
	script := compileScriptDefault(t, `class Box
  def value
    return 3
  end
end

enum Status
  Draft
end

def answer
  return 7
end

def other
  return 11
end

def class_value
  Box.new.value
end

def enum_name
  Status::Draft.name
end`)

	fn, ok := script.Function("answer")
	if !ok {
		t.Fatalf("answer function missing")
	}
	fn.Params = []Param{{Name: "required"}}
	if result := callScript(t, context.Background(), script, "answer", nil, CallOptions{}); !result.Equal(NewInt(7)) {
		t.Fatalf("answer after mutating Function snapshot = %#v, want 7", result)
	}

	for _, candidate := range script.Functions() {
		if candidate.Name != "other" {
			continue
		}
		returnStmt, ok := candidate.Body[0].(*ReturnStmt)
		if !ok {
			t.Fatalf("other body[0] = %T, want *ReturnStmt", candidate.Body[0])
		}
		literal, ok := returnStmt.Value.(*IntegerLiteral)
		if !ok {
			t.Fatalf("other return value = %T, want *IntegerLiteral", returnStmt.Value)
		}
		literal.Value = 99
	}
	if result := callScript(t, context.Background(), script, "other", nil, CallOptions{}); !result.Equal(NewInt(11)) {
		t.Fatalf("other after mutating Functions snapshot = %#v, want 11", result)
	}

	for _, classDef := range script.Classes() {
		if classDef.Name != "Box" {
			continue
		}
		returnStmt, ok := classDef.Methods["value"].Body[0].(*ReturnStmt)
		if !ok {
			t.Fatalf("Box.value body[0] = %T, want *ReturnStmt", classDef.Methods["value"].Body[0])
		}
		literal, ok := returnStmt.Value.(*IntegerLiteral)
		if !ok {
			t.Fatalf("Box.value return value = %T, want *IntegerLiteral", returnStmt.Value)
		}
		literal.Value = 99
	}
	if result := callScript(t, context.Background(), script, "class_value", nil, CallOptions{}); !result.Equal(NewInt(3)) {
		t.Fatalf("class_value after mutating Classes snapshot = %#v, want 3", result)
	}

	for _, enumDef := range script.Enums() {
		if enumDef.Name != "Status" {
			continue
		}
		enumDef.Members["Draft"].Name = "Mutated"
	}
	if result := callScript(t, context.Background(), script, "enum_name", nil, CallOptions{}); !result.Equal(NewString("Draft")) {
		t.Fatalf("enum_name after mutating Enums snapshot = %#v, want Draft", result)
	}
}

func TestScriptCallReturnsIsolatedCompiledValues(t *testing.T) {
	script := compileScriptDefault(t, `class Box
  def value
    return 3
  end
end

enum Status
  Draft
end

def exported_answer(value)
  return 7
end

def export_function
  exported_answer
end

def export_class
  Box
end

def export_enum
  Status
end

def class_value
  Box.new.value
end

def enum_name
  Status::Draft.name
end`)

	exportedFunction := valueFunction(callScript(t, context.Background(), script, "export_function", nil, CallOptions{}))
	returnStmt, ok := exportedFunction.Body[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("exported answer body[0] = %T, want *ReturnStmt", exportedFunction.Body[0])
	}
	literal, ok := returnStmt.Value.(*IntegerLiteral)
	if !ok {
		t.Fatalf("exported answer return value = %T, want *IntegerLiteral", returnStmt.Value)
	}
	literal.Value = 99
	if result := callScript(t, context.Background(), script, "exported_answer", []Value{NewString("arg")}, CallOptions{}); !result.Equal(NewInt(7)) {
		t.Fatalf("exported_answer after mutating returned function = %#v, want 7", result)
	}

	exportedClass := valueClass(callScript(t, context.Background(), script, "export_class", nil, CallOptions{}))
	returnStmt, ok = exportedClass.Methods["value"].Body[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("exported Box.value body[0] = %T, want *ReturnStmt", exportedClass.Methods["value"].Body[0])
	}
	literal, ok = returnStmt.Value.(*IntegerLiteral)
	if !ok {
		t.Fatalf("exported Box.value return value = %T, want *IntegerLiteral", returnStmt.Value)
	}
	literal.Value = 99
	if result := callScript(t, context.Background(), script, "class_value", nil, CallOptions{}); !result.Equal(NewInt(3)) {
		t.Fatalf("class_value after mutating returned class = %#v, want 3", result)
	}

	exportedEnum := valueEnum(callScript(t, context.Background(), script, "export_enum", nil, CallOptions{}))
	exportedEnum.Members["Draft"].Name = "Mutated"
	if result := callScript(t, context.Background(), script, "enum_name", nil, CallOptions{}); !result.Equal(NewString("Draft")) {
		t.Fatalf("enum_name after mutating returned enum = %#v, want Draft", result)
	}
}

func TestCompileRejectsOversizedSource(t *testing.T) {
	engine := MustNewEngine(Config{})
	source := "def run\n  " + strings.Repeat("a", 1<<20) + "\nend"

	_, err := engine.Compile(source)
	requireErrorContains(t, err, "source exceeds maximum")
}

func TestExecuteChecksCanceledContextBeforeCompile(t *testing.T) {
	engine := MustNewEngine(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := engine.Execute(ctx, "def run(")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute with canceled context = %v, want context.Canceled", err)
	}
}

func TestBuiltinRegistrationConcurrentWithSnapshots(t *testing.T) {
	if os.Getenv("VIBES_CONTAINMENT_SUBPROCESS") == "builtin-registration" {
		runBuiltinRegistrationConcurrencyProbe(t)
		return
	}

	runContainmentSubprocess(t, "builtin-registration", "TestBuiltinRegistrationConcurrentWithSnapshots")
}

func runContainmentSubprocess(t *testing.T, probe, testName string) {
	t.Helper()

	// Generous because the re-executed binary may carry whole-module
	// coverage instrumentation (atomic counters under goroutine
	// contention), which slows the concurrency probes well past their
	// uninstrumented runtime on shared CI runners. The deadline only
	// guards against genuine hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), "VIBES_CONTAINMENT_SUBPROCESS="+probe)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s subprocess timed out: %v\n%s", probe, ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("%s subprocess failed: %v\n%s", probe, err, output)
	}
}

func runBuiltinRegistrationConcurrencyProbe(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previousProcs)

	engine := MustNewEngine(Config{})
	slowMethods := make(map[string]Value, 256)
	for i := range 256 {
		slowMethods[fmt.Sprintf("method_%d", i)] = NewInt(int64(i))
	}
	for i := range 16 {
		engine.builtins[fmt.Sprintf("slow_%d", i)] = NewObject(slowMethods)
	}

	noop := func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewNil(), nil
	}

	for attempt := range 12 {
		start := make(chan struct{})
		stop := make(chan struct{})
		ready := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				<-start
				_ = engine.Builtins()
				ready <- struct{}{}
				for {
					select {
					case <-stop:
						return
					default:
						_ = engine.Builtins()
					}
				}
			})
		}
		close(start)
		for range 4 {
			<-ready
		}
		for i := range 500 {
			engine.RegisterBuiltin(fmt.Sprintf("probe_%d_%d", attempt, i), noop)
		}
		close(stop)
		wg.Wait()
	}
}
