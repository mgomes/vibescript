package vibes

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

	exportedFunction := callScript(t, context.Background(), script, "export_function", nil, CallOptions{}).Function()
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

	exportedClass := callScript(t, context.Background(), script, "export_class", nil, CallOptions{}).Class()
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

	exportedEnum := callScript(t, context.Background(), script, "export_enum", nil, CallOptions{}).Enum()
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

func runContainmentSubprocess(t *testing.T, probe string, testName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	slowMethods := make(map[string]Value, 12_000)
	for i := range 12_000 {
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
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				<-start
				_ = engine.Builtins()
			})
		}
		close(start)
		time.Sleep(200 * time.Microsecond)
		for i := range 1_500 {
			engine.RegisterBuiltin(fmt.Sprintf("probe_%d_%d", attempt, i), noop)
		}
		wg.Wait()
	}
}
