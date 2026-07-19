package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func typedGreetEngine(t testing.TB) *Engine {
	t.Helper()
	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("greet", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		name := args[0].String()
		return NewString("hi " + name), nil
	}, Signature{
		Params: []SignatureParam{
			{Name: "name", Type: "string"},
			{Name: "shout", Type: "bool", Optional: true},
		},
		Result: "string",
	})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	return engine
}

func TestHostSignatureCheckerContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name:    "argument type contradiction",
			source:  "def run()\n  greet(1)\nend",
			warning: "call to greet argument name expected string, got int",
		},
		{
			name:    "missing argument",
			source:  "def run()\n  greet()\nend",
			warning: "call to greet has too few arguments",
		},
		{
			name:    "extra argument",
			source:  "def run()\n  greet(\"a\", true, 1)\nend",
			warning: "call to greet has too many arguments",
		},
		{
			name:    "keyword rejected",
			source:  "def run()\n  greet(\"a\", shout: true)\nend",
			warning: "call to greet does not accept keyword arguments",
		},
		{
			name:    "block rejected",
			source:  "def run()\n  greet(\"a\") { 1 }\nend",
			warning: "call to greet does not accept a block",
		},
		{
			name: "result contradiction",
			source: `
def takes_int(value: int)
  value
end

def run()
  takes_int(greet("a"))
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithEngine(t, typedGreetEngine(t), tc.source)
			requireCheckWarningContains(t, script, tc.warning)
		})
	}
}

func TestHostSignatureStaysGradualWithoutSignature(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("greet", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString("hi"), nil
	})
	script := compileScriptWithEngine(t, engine, `
def takes_int(value: int)
  value
end

def run()
  takes_int(greet(1, 2, 3))
end
`)
	requireNoCheckWarnings(t, script)
}

func TestHostSignatureRuntimeValidationAligned(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, typedGreetEngine(t), "def run(v)\n  greet(v)\nend")
	if _, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{}); err == nil || !strings.Contains(err.Error(), "greet argument name expected string, got int") {
		t.Fatalf("Call(greet with int) error = %v, want boundary mismatch", err)
	}
	got, err := script.Call(context.Background(), "run", []Value{NewString("ada")}, CallOptions{})
	if err != nil || got.String() != "hi ada" {
		t.Fatalf("Call(greet) = %v, %v; want \"hi ada\"", got, err)
	}
}

func TestHostSignatureCoreOverride(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewInt(7), nil
	}, Signature{Result: "int"})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
def takes_string(value: string)
  value
end

def run()
  takes_string(format())
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	dynamic := MustNewEngine(Config{})
	dynamic.RegisterBuiltin("format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewInt(7), nil
	})
	script = compileScriptWithEngine(t, dynamic, `
def takes_string(value: string)
  value
end

def run()
  takes_string(format())
end
`)
	requireNoCheckWarnings(t, script)
}

func TestHostSignatureOptionGlobals(t *testing.T) {
	t.Parallel()

	typed, err := NewTypedBuiltin("jobs.enqueue", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewBool(true), nil
	}, Signature{
		Params: []SignatureParam{{Name: "queue", Type: "string"}, {Name: "payload", Type: "hash"}},
		Result: "bool",
	})
	if err != nil {
		t.Fatalf("NewTypedBuiltin: %v", err)
	}
	jobs := NewObject(map[string]Value{"enqueue": typed})
	opts := CallOptions{Globals: map[string]Value{"jobs": jobs}}

	script := compileScriptDefault(t, `
def run()
  jobs.enqueue(1, {})
end
`)
	warnings := script.CheckWarningsWithOptions(opts)
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Message, "call to jobs.enqueue argument queue expected string, got int") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsWithOptions() = %v, want jobs.enqueue argument warning", warnings)
	}

	plain := NewObject(map[string]Value{"enqueue": NewBuiltin("jobs.enqueue", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewBool(true), nil
	})})
	warnings = script.CheckWarningsWithOptions(CallOptions{Globals: map[string]Value{"jobs": plain}})
	if len(warnings) != 0 {
		t.Fatalf("CheckWarningsWithOptions(untyped) = %v, want none", warnings)
	}
}

func TestHostSignatureDefinitionErrors(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	noop := func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewNil(), nil
	}
	if err := engine.RegisterBuiltinWithSignature("bad", noop, Signature{
		Params: []SignatureParam{{Name: "x", Type: "not a type <"}},
	}); err == nil {
		t.Fatal("invalid type spelling registered without error")
	}
	if err := engine.RegisterBuiltinWithSignature("bad", noop, Signature{
		Params: []SignatureParam{{Name: "x", Optional: true}, {Name: "y"}},
	}); err == nil {
		t.Fatal("required-after-optional registered without error")
	}
}

func TestHostSignatureNamedTypesNormalizeLikeAnnotations(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("advance", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if args[0].Kind() != KindEnumValue {
			return NewNil(), fmt.Errorf("advance received %s, want normalized enum value", args[0].Kind())
		}
		return args[0], nil
	}, Signature{
		Params: []SignatureParam{{Name: "status", Type: "Status"}},
		Result: "Status",
	})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
enum Status
  Draft
  Published
end

def run()
  advance(:draft)
end
`)
	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(advance :draft) error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call(advance :draft) = %#v, want Status::Draft", got)
	}
}

func TestHostSignatureResultEnforcedAtRuntime(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("lie", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString("not an int"), nil
	}, Signature{Result: "int"})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, "def run()\n  lie()\nend")
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "return value for lie expected int, got string") {
		t.Fatalf("Call(lie) error = %v, want return contract mismatch", err)
	}
}

func TestHostSignatureRejectsForwardedBlockStatically(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, typedGreetEngine(t), `
def run(s: string, blk: function)
  greet(s, &blk)
end
`)
	requireCheckWarningContains(t, script, "call to greet does not accept a block")
}

func TestHostSignatureUnresolvedNamedTypeWarns(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("advance", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return args[0], nil
	}, Signature{Params: []SignatureParam{{Name: "status", Type: "Status"}}})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, "def run()\n  advance(:draft)\nend")
	requireCheckWarningContains(t, script, "call to advance argument status uses unknown type Status")
}

func TestHostSignatureFunctionParamPreservesCallable(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("accept", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if !isCallableValue(args[0]) {
			return NewNil(), fmt.Errorf("accept received %s, want a callable", args[0].Kind())
		}
		return NewBool(true), nil
	}, Signature{Params: []SignatureParam{{Name: "fn", Type: "function"}}})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
def cb()
  "ran"
end

def run()
  accept(cb)
end
`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(accept cb) error: %v", err)
	}
	if got.Kind() != KindBool || !got.Bool() {
		t.Fatalf("Call(accept cb) = %#v, want true", got)
	}
}

func TestHostSignatureModuleContextResolvesNamedTypes(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "levels.vibe", content: `enum Level
  Low
  High
end

def pick(level: Level) -> Level
  tag(level)
end
`})
	engine := mustNewEngineWithModuleRoot(t, dir)
	err := engine.RegisterBuiltinWithSignature("tag", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return args[0], nil
	}, Signature{
		Params: []SignatureParam{{Name: "level", Type: "Level"}},
		Result: "Level",
	})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
def run()
  require("levels").pick(:low)
end
`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(module pick) error: %v", err)
	}
	if got.Kind() != KindEnumValue {
		t.Fatalf("Call(module pick) = %#v, want Level enum value", got)
	}
}

func TestHostSignatureUnresolvedResultTypeWarns(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	err := engine.RegisterBuiltinWithSignature("mystery", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewInt(1), nil
	}, Signature{Result: "Missing"})
	if err != nil {
		t.Fatalf("RegisterBuiltinWithSignature: %v", err)
	}
	script := compileScriptWithEngine(t, engine, "def run()\n  mystery()\nend")
	requireCheckWarningContains(t, script, "call to mystery result uses unknown type Missing")
}

func TestHostSignatureModuleFunctionsWinOverGlobals(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "helpers.vibe", content: "def fetch(a, b, c)\n  a\nend\n\ndef use() -> int\n  fetch(1, 2, 3)\nend\n"})
	engine := mustNewEngineWithModuleRoot(t, dir)
	typed, err := NewTypedBuiltin("fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString(args[0].String()), nil
	}, Signature{Params: []SignatureParam{{Name: "key", Type: "string"}}, Result: "string"})
	if err != nil {
		t.Fatalf("NewTypedBuiltin: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
def run()
  require("helpers").use()
end
`)
	warnings := script.CheckWarningsWithOptions(CallOptions{Globals: map[string]Value{"fetch": typed}})
	if len(warnings) != 0 {
		t.Fatalf("CheckWarningsWithOptions() = %v, want none: the module's own fetch wins over the host global", warnings)
	}
}

func TestHostSignatureTypedGlobalsApplyInsideModules(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "worker.vibe", content: "def enqueue_bad()\n  jobs.enqueue(1, {})\nend\n"})
	engine := mustNewEngineWithModuleRoot(t, dir)
	typed, err := NewTypedBuiltin("jobs.enqueue", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewBool(true), nil
	}, Signature{
		Params: []SignatureParam{{Name: "queue", Type: "string"}, {Name: "payload", Type: "hash"}},
		Result: "bool",
	})
	if err != nil {
		t.Fatalf("NewTypedBuiltin: %v", err)
	}
	script := compileScriptWithEngine(t, engine, `
def run()
  require("worker").enqueue_bad()
end
`)
	warnings := script.CheckWarningsWithOptions(CallOptions{Globals: map[string]Value{"jobs": NewObject(map[string]Value{"enqueue": typed})}})
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Message, "call to jobs.enqueue argument queue expected string, got int") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsWithOptions() = %v, want typed contract applied inside the module", warnings)
	}
}
