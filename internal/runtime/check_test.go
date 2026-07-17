package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type checkOptionGlobalsCapability map[string]Value

func (c checkOptionGlobalsCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	globals := make(map[string]Value, len(c))
	for name, val := range c {
		globals[name] = val
	}
	return globals, nil
}

func TestCheckWarningsValidateTypeAnnotations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def accept(v: int | Missing) -> int | Missing
  v
end
`)

	requireCheckWarningContains(t, script, "unknown type Missing")
}

func TestCheckWarningsValidateDestructuredBlockElementTypeAnnotations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [1].each do |(x: Missing)|
    x
  end
end
`)

	requireCheckWarningContains(t, script, "unknown type Missing")
}

func TestCheckWarningsWithOptionsResolveHostEnumGlobals(t *testing.T) {
	t.Parallel()

	hostScript := compileScript(t, `
enum Status
  Draft
end
`)
	hostStatus := NewEnum(hostScript.enums["Status"])

	script := compileScript(t, `
def run(status: Status = :draft) -> Status
  status
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireNoCheckWarningsWithOptions(t, script, CallOptions{
		Globals: map[string]Value{"Status": hostStatus},
	})

	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"Status": hostStatus},
	})
	if err != nil {
		t.Fatalf("Call() with host enum global returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() with host enum global = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsWithOptionsRespectHostGlobalsBeforeBuiltins(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  rand(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to rand has too many arguments")

	hostRand := NewBuiltin("rand", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewInt(int64(len(args))), nil
	})
	opts := CallOptions{Globals: map[string]Value{"rand": hostRand}}
	requireNoCheckWarningsWithOptions(t, script, opts)

	got, err := script.Call(context.Background(), "run", nil, opts)
	if err != nil {
		t.Fatalf("Call() with host rand global returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with host rand global = %s, want 2", got)
	}
}

func TestCheckWarningsVisitRaiseMessageExpression(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def message()
  "bad"
end

def run()
  raise RuntimeError, message(1)
end
`)

	requireCheckWarningContains(t, script, "call to message has unexpected positional arguments")
}

func TestCheckWarningsWithOptionsRespectHostGlobalsBeforeStaticContracts(t *testing.T) {
	t.Parallel()

	functionScript := compileScript(t, `
def target(a)
  a
end

def run()
  target(1, 2)
end
`)
	requireCheckWarningContains(t, functionScript, "call to target has unexpected positional arguments")

	hostTarget := NewBuiltin("target", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewInt(int64(len(args))), nil
	})
	functionOpts := CallOptions{Globals: map[string]Value{"target": hostTarget}}
	requireNoCheckWarningsWithOptions(t, functionScript, functionOpts)

	got, err := functionScript.Call(context.Background(), "run", nil, functionOpts)
	if err != nil {
		t.Fatalf("Call() with host target global returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with host target global = %s, want 2", got)
	}

	classScript := compileScript(t, `
class Box
  def self.take(v)
    v
  end
end

def run()
  Box.take(1, 2)
end
`)
	requireCheckWarningContains(t, classScript, "call to Box.take has unexpected positional arguments")

	hostBox := NewObject(map[string]Value{
		"take": NewBuiltin("Box.take", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
			return NewInt(int64(len(args))), nil
		}),
	})
	classOpts := CallOptions{Globals: map[string]Value{"Box": hostBox}}
	requireNoCheckWarningsWithOptions(t, classScript, classOpts)

	got, err = classScript.Call(context.Background(), "run", nil, classOpts)
	if err != nil {
		t.Fatalf("Call() with host Box global returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with host Box global = %s, want 2", got)
	}

	chainedScript := compileScript(t, `
class ChainBox
  def initialize()
  end

  def take(v)
    v
  end
end

def run()
  ChainBox.new.take(1, 2)
end
`)
	requireCheckWarningContains(t, chainedScript, "call to ChainBox#take has unexpected positional arguments")

	hostChainBox := NewObject(map[string]Value{
		"new": NewAutoBuiltin("ChainBox.new", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			return NewObject(map[string]Value{
				"take": NewBuiltin("ChainBox#take", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
					return NewInt(int64(len(args))), nil
				}),
			}), nil
		}),
	})
	chainedOpts := CallOptions{Globals: map[string]Value{"ChainBox": hostChainBox}}
	requireNoCheckWarningsWithOptions(t, chainedScript, chainedOpts)

	got, err = chainedScript.Call(context.Background(), "run", nil, chainedOpts)
	if err != nil {
		t.Fatalf("Call() with host ChainBox global returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with host ChainBox global = %s, want 2", got)
	}
}

func TestCheckWarningsRespectRegisteredBuiltinsBeforeSpecs(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("rand", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewInt(int64(len(args))), nil
	})
	script := compileScriptWithEngine(t, engine, `
def run()
  rand(1, 2)
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() with registered rand builtin returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with registered rand builtin = %s, want 2", got)
	}
}

func TestBuiltinCallSpecsFollowRegistry(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	tests := []struct {
		name    string
		minArgs int
		maxArgs int
	}{
		{name: "assert", minArgs: 1, maxArgs: -1},
		{name: "JSON.parse", minArgs: 1, maxArgs: 1},
		{name: "Time.parse", minArgs: 1, maxArgs: 2},
		{name: "Math.sqrt", minArgs: 1, maxArgs: 1},
	}
	for _, tt := range tests {
		spec, ok := engine.builtinCallSpec(tt.name)
		if !ok {
			t.Errorf("builtinCallSpec(%q) was not found", tt.name)
			continue
		}
		if spec.minArgs != tt.minArgs || spec.maxArgs != tt.maxArgs {
			t.Errorf("builtinCallSpec(%q) arity = %d..%d, want %d..%d", tt.name, spec.minArgs, spec.maxArgs, tt.minArgs, tt.maxArgs)
		}
	}
	if _, ok := engine.builtinCallSpec("Regexp.union"); ok {
		t.Error("Regexp.union unexpectedly has a static call contract")
	}

	snapshot := engine.Builtins()
	assertBuiltin := valueBuiltin(snapshot["assert"])
	if assertBuiltin == nil || assertBuiltin.checkSpec == nil {
		t.Error("Engine.Builtins() did not preserve the assert call contract")
	}
	jsonParse := valueBuiltin(snapshot["JSON"].Hash()["parse"])
	if jsonParse == nil || jsonParse.checkSpec == nil {
		t.Error("Engine.Builtins() did not preserve the JSON.parse call contract")
	}

	engine.RegisterBuiltin("assert", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	if _, ok := engine.builtinCallSpec("assert"); ok {
		t.Error("host override retained the default assert call contract")
	}
}

func TestCheckWarningsWalkShortCircuitExpressions(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def one(value)
  value
end

def run()
  true && one()
end
`)

	requireCheckWarningContains(t, script, "call to one is missing argument value")
}

func TestCheckWarningsResolveRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("enum_status")
  :published
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Published" {
		t.Fatalf("Call() after required module enum export = %#v, want Status::Published", got)
	}
}

func TestCheckWarningsDoNotResolveInvalidArityRequireModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("enum_status", "extra")
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "require expects a single module name argument")
}

func TestCheckWarningsDoNotResolveInvalidKeywordRequireModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("enum_status", name: "status")
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "require: unknown keyword argument name")
}

func TestCheckWarningsResolveRequiredModuleFunctionExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("helper")
  double(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to double has unexpected positional arguments")
}

func TestCheckWarningsResolveInlineRequiredModuleFunctionMembers(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("helper").double(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to helper.double has unexpected positional arguments")
}

func TestCheckWarningsCheckReachablePrivateModuleHelpers(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "contracts.vibe", content: `private def bad(value: int = "bad")
  value
end

def run()
  bad()
end
`})
	engine := mustNewEngineWithModuleRoot(t, dir)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("contracts").run()
end
`)

	requireCheckWarningContains(t, script, "default value for value expected int, got string")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "argument value expected int, got string")
}

func TestCheckWarningsDoNotAutoInvokeParameterizedRequiredModuleFunctionMembers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "inline require member",
			source: `def run()
  require("helper").double
end`,
		},
		{
			name: "aliased require member",
			source: `def run()
  require("helper", as: "helpers")
  helpers.double
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := moduleTestEngine(t)
			script := compileScriptWithEngine(t, engine, tc.source)
			requireNoCheckWarnings(t, script)

			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("Call(%q) returned error: %v", "run", err)
			}
			if got.Kind() != KindFunction {
				t.Fatalf("Call(%q) = %s, want function value", "run", got.Kind())
			}
		})
	}
}

func TestCheckWarningsSeedInlineRequiredModuleExportsBeforeMemberArguments(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("enum_status").normalize(:draft)
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after inline required module member returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after inline required module member = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsResolveAliasedRequiredModuleFunctionExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("helper", as: "helpers")
  helpers.double(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to helpers.double has unexpected positional arguments")
}

func TestCheckWarningsApplyLeftRequireEffectsBeforeBinaryRight(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("enum_status") && normalize("bad")
end
`)

	requireCheckWarningContains(t, script, "call to normalize argument status expected Status, got string")
}

func TestCheckWarningsDoNotLeakAliasConflictModuleExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def helpers(value)
  value
end

def run()
  begin
    require("helper", as: "helpers")
  rescue
    nil
  end
  double(1, 2)
end
`)

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsDoNotMaterializeBuiltinAliasConflicts(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  begin
    require("helper", as: "JSON")
  rescue
    nil
  end
  JSON.parse()
end
`)

	requireCheckWarningContains(t, script, "call to JSON.parse has too few arguments")
}

func TestCheckWarningsResolveRequiredModuleFunctionExportsInShortCircuitExpressions(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("helper") && double(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to double has unexpected positional arguments")
}

func TestCheckWarningsPreserveBuiltinShadowingForRequiredModuleExports(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "shadow_rand.vibe"), []byte(`def rand(a, b)
  a
end
`), 0o644); err != nil {
		t.Fatalf("write shadow_rand module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `
def run()
  require("shadow_rand")
  rand(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to rand has too many arguments")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "rand expects at most one argument")
}

func TestCheckWarningsResolveRequiredModuleExportsFromModuleClassBodies(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "status.vibe"), []byte(`enum Status
  Draft
end
`), 0o644); err != nil {
		t.Fatalf("write status module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "loader.vibe"), []byte(`class Loader
  require("./status")
end
`), 0o644); err != nil {
		t.Fatalf("write loader module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("loader")
  :draft
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after class-body required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after class-body required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsResolveSnippetDeferredClassBodyRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script, err := engine.CompileSnippet(`
class Loader
  require("enum_status")
end

def normalize(status: Status) -> Status
  status
end

normalize(:draft)
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after deferred class-body required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after deferred class-body required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsCheckSnippetDeferredClassBodyInEntrypointOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "uses prior require before deferred class body",
			source: `require("enum_status")

class Loader
  normalize(:draft)
end

def normalize(status: Status) -> Status
  status
end`,
		},
		{
			name: "reports deferred class body contract errors",
			source: `require("enum_status")

class Loader
  normalize("draft")
end

def normalize(status: Status) -> Status
  status
end`,
			want: "call to normalize argument status expected Status, got string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := moduleTestEngine(t)
			script, err := engine.CompileSnippet(tc.source, "run")
			if err != nil {
				t.Fatalf("CompileSnippet(%q) failed: %v", tc.name, err)
			}
			if tc.want == "" {
				if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
					t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", "run", warnings)
				}
				return
			}
			warnings := script.CheckWarningsForFunction("run")
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsForFunctionChecksReachableLocalContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "direct function return",
			source: `def bad() -> int
  "x"
end

def unused() -> int
  "unused"
end

def run
  bad()
end`,
			want: "return value expected int, got string",
		},
		{
			name: "transitive function return",
			source: `def leaf() -> int
  "x"
end

def middle
  leaf()
end

def unused() -> int
  "unused"
end

def run
  middle()
end`,
			want: "return value expected int, got string",
		},
		{
			name: "function default",
			source: `def helper(v: int = "x")
  v
end

def unused() -> int
  "unused"
end

def run
  helper()
end`,
			want: "default value for v expected int, got string",
		},
		{
			name: "class method return",
			source: `class Box
  def self.make() -> int
    "x"
  end
end

def unused() -> int
  "unused"
end

def run
  Box.make()
end`,
			want: "return value expected int, got string",
		},
		{
			name: "instance method return",
			source: `class Box
  def take() -> int
    "x"
  end
end

def unused() -> int
  "unused"
end

def run
  Box.new.take()
end`,
			want: "return value expected int, got string",
		},
		{
			name: "uses call-site namespace state",
			source: `def helper
  JSON.parse("{}", "extra")
end

def unused() -> int
  "unused"
end

def run
  helper()
  JSON.parse = nil
end`,
			want: "call to JSON.parse has too many arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			warnings := script.CheckWarningsForFunction("run")
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				if warning.Function == "unused" {
					t.Fatalf("CheckWarningsForFunction(%q) checked unused function: %#v", "run", warnings)
				}
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsForFunctionRechecksReachableFunctionsPerRuntimeState(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def helper() -> Status
  :draft
end

def run(flag)
  if flag
    require("enum_status")
    helper()
  else
    helper()
  end
end
`)

	warnings := script.CheckWarningsForFunction("run")
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	got := strings.Join(messages, "\n")
	if !strings.Contains(got, "unknown type Status") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, "unknown type Status")
	}
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsForCallValidatesArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		args    []Value
		options CallOptions
		want    string
	}{
		{
			name: "missing positional",
			source: `def run(name)
  name
end`,
			want: "call to run is missing argument name",
		},
		{
			name: "unexpected positional",
			source: `def run()
  nil
end`,
			args: []Value{NewString("extra")},
			want: "call to run has unexpected positional arguments",
		},
		{
			name: "typed positional",
			source: `def run(count: int)
  count
end`,
			args: []Value{NewString("one")},
			want: "call to run argument count expected int, got string",
		},
		{
			name: "missing keyword",
			source: `def run(name:)
  name
end`,
			want: "call to run is missing keyword argument name",
		},
		{
			name: "typed keyword",
			source: `def run(count: int)
  count
end`,
			options: CallOptions{Keywords: map[string]Value{"count": NewString("one")}},
			want:    "call to run argument count expected int, got string",
		},
		{
			name: "typed block without host block",
			source: `def run(&block: function)
  block
end`,
			want: "call to run argument block expected function, got nil",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			warnings := script.CheckWarningsForCall("run", tc.args, tc.options)
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForCall(%q) = %q, want substring %q", "run", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsResolveSymbolRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require(:enum_status)
  :published
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after symbol-required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Published" {
		t.Fatalf("Call() after symbol-required module enum export = %#v, want Status::Published", got)
	}
}

func TestCheckWarningsResolveRequiredModuleEnumExportsInBlockParameters(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("enum_status")
  [:published].map do |status: Status|
    status
  end
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() with required enum block parameter returned error: %v", err)
	}
	if got.Kind() != KindArray || len(got.Array()) != 1 {
		t.Fatalf("Call() with required enum block parameter = %#v, want one-element array", got)
	}
	status := got.Array()[0]
	if status.Kind() != KindEnumValue || valueEnumValue(status).Name != "Published" {
		t.Fatalf("Call() with required enum block parameter yielded %#v, want Status::Published", status)
	}
}

func TestCheckWarningsWithOptionsResolveCapabilityGlobals(t *testing.T) {
	t.Parallel()

	hostScript := compileScript(t, `
enum Status
  Draft
end
`)
	hostStatus := NewEnum(hostScript.enums["Status"])
	opts := callOptionsWithCapabilities(checkOptionGlobalsCapability{"Status": hostStatus})

	script := compileScript(t, `
def run(status: Status = :draft) -> Status
  status
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireNoCheckWarningsWithOptions(t, script, opts)

	got, err := script.Call(context.Background(), "run", nil, opts)
	if err != nil {
		t.Fatalf("Call() with capability enum global returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() with capability enum global = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsWithOptionsRespectCapabilityGlobalsBeforeBuiltins(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  rand(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to rand has too many arguments")

	capRand := NewBuiltin("rand", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewInt(int64(len(args))), nil
	})
	opts := callOptionsWithCapabilities(checkOptionGlobalsCapability{"rand": capRand})
	requireNoCheckWarningsWithOptions(t, script, opts)

	got, err := script.Call(context.Background(), "run", nil, opts)
	if err != nil {
		t.Fatalf("Call() with capability rand global returned error: %v", err)
	}
	if !got.Equal(NewInt(2)) {
		t.Fatalf("Call() with capability rand global = %s, want 2", got)
	}
}

func TestCheckWarningsSeedCallableContractsWithClassBodyRequires(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
class Normalizer
  require("enum_status")

  def self.normalize(status: Status = :published) -> Status
    status
  end
end

def run(status: Status = :draft) -> Status
  Normalizer.normalize(status)
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after class-body required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after class-body required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsAccumulateClassBodyRuntimeEffectsInSourceOrder(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def parse(raw, extra)
  raw
end

def normalize(status: Status = :draft) -> Status
  status
end

class ZLoadStatus
  require("enum_status")
  JSON.parse = parse
end

class AUseStatus
  normalize(:draft)
  JSON.parse("{}", "extra")
end

def run(status: Status = :published) -> Status
  normalize(status)
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after ordered class-body effects returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Published" {
		t.Fatalf("Call() after ordered class-body effects = %#v, want Status::Published", got)
	}
}

func TestCheckWarningsResolveTransitiveRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.Mkdir(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir module package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "status.vibe"), []byte(`
enum Status
  Draft
  Published
end
`), 0o644); err != nil {
		t.Fatalf("write status module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "wrapper.vibe"), []byte(`
require("./status")
`), 0o644); err != nil {
		t.Fatalf("write wrapper module: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{root}})
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("pkg/wrapper")
  :published
end
`)

	requireNoCheckWarnings(t, script)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after transitive module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Published" {
		t.Fatalf("Call() after transitive module enum export = %#v, want Status::Published", got)
	}
}

func TestCheckWarningsDoNotHoistRequiredModuleEnumExportsFromUnrelatedFunctions(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def load()
  require("enum_status")
end

def normalize(status: Status) -> Status
  status
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "normalize", []Value{NewSymbol("draft")}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotHoistRequiredModuleEnumExportsBeforeRequire(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def normalize(status: Status) -> Status
  status
end

def run()
  normalize(:draft)
  require("enum_status")
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotHoistUnreachableRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  return :draft
  require("enum_status")
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsKeepConditionalRequiredModuleEnumExportsBranchScoped(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  if flag
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsMergeRequiredModuleEnumExportsFromAllBranches(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  if flag
    require("enum_status")
  else
    require("enum_status")
  end
  :draft
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", []Value{NewBool(false)}, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after branch-required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after branch-required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsPreserveCommonRequireAliasesAfterBranchMerge(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag)
  if flag
    require("helper", as: "helpers")
  else
    require("helper", as: "helpers")
  end
  helpers.double(1, 2)
end
`)

	requireCheckWarningContains(t, script, "call to helpers.double has unexpected positional arguments")
	requireCallErrorContains(t, script, "run", []Value{NewBool(true)}, CallOptions{}, "unexpected positional arguments")
}

func TestCheckWarningsResolveRequiredModuleEnumExportsFromIfConditions(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  if require("enum_status")
    return :draft
  end

  if flag
    raise "stop"
  elsif require("enum_status")
    return :draft
  end

  :published
end
`)

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsPropagateConditionRequireEffectsToTrueBranch(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag)
  if flag && require("enum_status")
    normalize("bad")
  end
end
`)

	requireCheckWarningContains(t, script, "call to normalize argument status expected Status, got string")
}

func TestCheckWarningsDoNotHoistShortCircuitRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  false && require("enum_status")
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsResolveReachableShortCircuitRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "symbolic and",
			source: `
def run() -> Status
  true && require("enum_status")
  :draft
end
`,
			want: "Draft",
		},
		{
			name: "symbolic or",
			source: `
def run() -> Status
  false || require("enum_status")
  :published
end
`,
			want: "Published",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			engine := moduleTestEngine(t)
			script := compileScriptWithEngine(t, engine, tc.source)
			requireNoCheckWarnings(t, script)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("Call() after short-circuit require returned error: %v", err)
			}
			if got.Kind() != KindEnumValue || valueEnumValue(got).Name != tc.want {
				t.Fatalf("Call() after short-circuit require = %#v, want Status::%s", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsDoNotHoistConditionalExpressionRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  flag ? require("enum_status") : nil
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")

	staticScript := compileScriptWithEngine(t, engine, `
def run() -> Status
  false ? require("enum_status") : nil
  :draft
end
`)

	requireCheckWarningContains(t, staticScript, "unknown type Status")
	requireCallErrorContains(t, staticScript, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotLeakConditionalExpressionCallArgumentRequires(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def id(value)
  value
end

def run(flag) -> Status
  flag ? id(require("enum_status")) : nil
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotHoistIfExpressionConditionRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  value = if flag
    nil
  elsif require("enum_status")
    nil
  else
    nil
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(true)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsApplyIfExpressionConditionEffectsBeforeBranch(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  if require("enum_status")
    normalize(:draft)
  else
    :published
  end
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after if-expression condition require returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after if-expression condition require = %#v, want Status::Draft", got)
	}
}

func TestCheckCallTargetResolvesBeforeArgumentRequires(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helpers.vibe"), []byte(`
def shout(value: string)
  value
end
`), 0o644); err != nil {
		t.Fatalf("write helpers module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})
	script, _, _, err := CompileSnippetWithProgram(engine, `shout(1, require("helpers"))
shout(1)`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// The first call's target resolves before its own argument's require
	// runs, so no contract is visible there; the next statement sees the
	// loaded exports and reports.
	warnings := script.CheckWarnings()
	if len(warnings) != 1 {
		t.Fatalf("CheckWarnings() = %#v, want exactly the second call's warning", warnings)
	}
	if warnings[0].Pos.Line != 2 || !strings.Contains(warnings[0].Message, "call to shout argument value expected string, got int") {
		t.Fatalf("CheckWarnings() = %#v, want line-2 argument warning", warnings)
	}
}

func TestCheckCallBlockArgumentRequireEffectsApply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "status_helpers.vibe"), []byte(`
enum Status
  Draft
end

def cb
  nil
end
`), 0o644); err != nil {
		t.Fatalf("write status_helpers module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})

	// The block argument's require binds its exports with the other
	// arguments, before dispatch, so the call site's annotation resolves.
	// use's own definition walk stays unseeded (the require is not
	// top-level), so only the call-site diagnostics in run matter here.
	script := compileScriptWithEngine(t, engine, `
def use(value: Status, &blk)
  value
end

def run
  use(:draft, &require("status_helpers").cb)
end
`)
	for _, warning := range script.CheckWarnings() {
		if warning.Function == "run" {
			t.Fatalf("CheckWarnings() = %#v, want none in run", warning)
		}
	}

	// And the resolved annotation validates the argument at the call.
	invalid := compileScriptWithEngine(t, engine, `
def use(value: Status, &blk)
  value
end

def run
  use(:bogus, &require("status_helpers").cb)
end
`)
	requireCheckWarningContains(t, invalid, "call to use argument value expected Status")
}

func TestCheckCallArgumentRequireEffectsApplyInOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helpers.vibe"), []byte(`
def shout(value: string)
  value
end
`), 0o644); err != nil {
		t.Fatalf("write helpers module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})

	// An earlier argument evaluates before a later argument's require, so
	// its nested call must not resolve the later exports.
	before, _, _, err := CompileSnippetWithProgram(engine, `def pair(a, b)
  a
end

pair(shout(1), require("helpers"))`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if warnings := before.CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("CheckWarnings() = %#v, want none for a call before the later require", warnings)
	}

	// A require in an earlier argument is live for the arguments after it.
	after, _, _, err := CompileSnippetWithProgram(engine, `def pair(a, b)
  a
end

pair(require("helpers"), shout(1))`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	requireCheckWarningContains(t, after, "call to shout argument value expected string, got int")
}

func TestCheckWarningsSkipReachedMethodsInSeededPass(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helpers.vibe"), []byte(`
def shout(value: string)
  value
end
`), 0o644); err != nil {
		t.Fatalf("write helpers module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})

	// A method invoked from top level before the require checks under the
	// pre-require root only: no second pass with the later exports.
	before, _, _, err := CompileSnippetWithProgram(engine, `class Api
  def self.load
    shout(1)
  end
end

Api.load
require "helpers"`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if warnings := before.CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("CheckWarnings() = %#v, want none for pre-require method call", warnings)
	}

	// The same call after the require checks under the loaded exports.
	after, _, _, err := CompileSnippetWithProgram(engine, `require "helpers"

class Api
  def self.load
    shout(1)
  end
end

Api.load`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	requireCheckWarningContains(t, after, "call to shout argument value expected string, got int")
}

func TestCheckWarningsDoNotHoistConditionalModuleEntrypointRequires(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "status.vibe"), []byte(`
enum Status
  Draft
end
`), 0o644); err != nil {
		t.Fatalf("write status module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "wrapper.vibe"), []byte(`
if false
  require("./status")
end
`), 0o644); err != nil {
		t.Fatalf("write wrapper module: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{root}})
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("wrapper")
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotHoistCaseArmRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  case flag
  when true
    require("enum_status") && :draft
  else
    :published
  end
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotLeakCaseArmCallArgumentRequires(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def id(value)
  value
end

def run(flag) -> Status
  case flag
  when true
    id(require("enum_status"))
  else
    nil
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsMergeCommonCaseArmRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag) -> Status
  case flag
  when true
    require("enum_status") && :draft
  else
    require("enum_status") && :published
  end
end
`)

	requireNoCheckWarnings(t, script)
	for _, tc := range []struct {
		name string
		flag bool
		want string
	}{
		{name: "then", flag: true, want: "Draft"},
		{name: "else", flag: false, want: "Published"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := script.Call(context.Background(), "run", []Value{NewBool(tc.flag)}, CallOptions{})
			if err != nil {
				t.Fatalf("Call() returned error: %v", err)
			}
			if got.Kind() != KindEnumValue || valueEnumValue(got).Name != tc.want {
				t.Fatalf("Call() = %#v, want Status::%s", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsDoNotHoistLoopBodyRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(items) -> Status
  for item in items
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewArray(nil)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsDoNotHoistBlockRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def noop
  nil
end

def run() -> Status
  noop do
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsSkipIgnoredFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def noop
  nil
end

def run
  noop do
    rand(1, 2)
  end
  1
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewInt(1)) {
		t.Fatalf("Call() = %s, want 1", got)
	}
}

func TestCheckWarningsSkipIgnoredBuiltinBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  money("1.00 USD") do
    rand(1, 2)
  end
  1
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewInt(1)) {
		t.Fatalf("Call() = %s, want 1", got)
	}
}

func TestCheckWarningsCheckYieldedFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def invoke
  yield
end

def run
  invoke do
    rand(1, 2)
  end
end
`)

	requireCheckWarningContains(t, script, "call to rand has too many arguments")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "rand expects at most one argument")
}

func TestCheckWarningsSkipShortCircuitedYieldedFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def false_and_invoke
  false && yield
end

def true_or_invoke
  true || yield
end

def run
  false_and_invoke do
    rand(1, 2)
  end
  true_or_invoke do
    rand(1, 2)
  end
  1
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewInt(1)) {
		t.Fatalf("Call() = %s, want 1", got)
	}
}

func TestCheckWarningsSkipCapturedFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def invoke(&block)
  block != nil
end

def run
  invoke do
    rand(1, 2)
  end
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewBool(true)) {
		t.Fatalf("Call() = %s, want true", got)
	}
}

func TestCheckWarningsSkipStaticallyUnreachableFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def false_if_invoke
  if false
    yield
  end
end

def run
  false_if_invoke do
    rand(1, 2)
  end

  [1].fetch(0) do
    rand(1, 2)
  end

  [1].fetch(-1) do
    rand(1, 2)
  end

  [1].fetch(0.0) do
    rand(1, 2)
  end

  1
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewInt(1)) {
		t.Fatalf("Call() = %s, want 1", got)
	}
}

func TestCheckWarningsSkipNestedIgnoredFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def noop
  nil
end

def wrapper
  noop do
    yield
  end
end

def run
  wrapper do
    rand(1, 2)
  end
  1
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}
	if !got.Equal(NewInt(1)) {
		t.Fatalf("Call() = %s, want 1", got)
	}
}

func TestCheckWarningsCheckNestedYieldedFunctionBlockBodies(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def invoke
  yield
end

def wrapper
  invoke do
    yield
  end
end

def run
  wrapper do
    rand(1, 2)
  end
end
`)

	requireCheckWarningContains(t, script, "call to rand has too many arguments")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "rand expects at most one argument")
}

func TestCheckWarningsDoNotHoistRescueOnlyRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  begin
    1
  rescue RuntimeError
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsRescueExpressionPreservesCalleeAutoCallMode(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  def inc(n)
    n + 1
  end
end

def run
  (Box.new.inc rescue Box.new.inc)(1)
end
`)

	requireNoCheckWarnings(t, script)
	if got := callScript(t, context.Background(), script, "run", nil, CallOptions{}); !got.Equal(NewInt(2)) {
		t.Fatalf("run() = %s, want 2", got)
	}
}

func TestCheckWarningsDoNotHoistSafeNavigationRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	staticScript := compileScriptWithEngine(t, engine, `
def run() -> Status
  nil&.load(require("enum_status")) do
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, staticScript, "unknown type Status")
	requireCallErrorContains(t, staticScript, "run", nil, CallOptions{}, "unknown type Status")

	dynamicScript := compileScriptWithEngine(t, engine, `
def run(maybe) -> Status
  maybe&.load(require("enum_status"))
  :draft
end
`)

	requireCheckWarningContains(t, dynamicScript, "unknown type Status")
	requireCallErrorContains(t, dynamicScript, "run", []Value{NewNil()}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsResolveEnsureRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  begin
    1
  ensure
    require("enum_status")
  end
  :draft
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after ensure-required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after ensure-required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsDefersReturnTypeUntilAfterEnsureRequires(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  begin
    return :draft
  ensure
    require("enum_status")
  end
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after ensure-required explicit return enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after ensure-required explicit return enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsSkipUnreachableShortCircuitOperands(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  false && rand(1, 2)
  true || rand(1, 2)
end
`)

	requireNoCheckWarnings(t, script)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("Call() with unreachable short-circuit operands returned error: %v", err)
	}
}

func TestCheckWarningsSkipStaticallyUnreachableIfBranches(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(flag)
  if false
    JSON.parse()
  elsif true
    1
  else
    JSON.parse()
  end

  if true
    1
  else
    JSON.parse()
  end

  value = if nil
    JSON.parse()
  elsif true
    1
  else
    JSON.parse()
  end

  other = if flag
    1
  elsif true
    2
  else
    JSON.parse()
  end

  ternary = true ? 1 : JSON.parse()
  ternary_false = false ? JSON.parse() : 2

  [value, other, ternary, ternary_false]
end

def typed() -> int
  if false
    "bad"
  elsif true
    1
  else
    "bad"
  end
end

def typed_ternary() -> int
  true ? 1 : "bad"
end

def exiting()
  if true
    return 1
  end
  JSON.parse()
end
`)

	requireNoCheckWarnings(t, script)
	if _, err := script.Call(context.Background(), "run", []Value{NewBool(false)}, CallOptions{}); err != nil {
		t.Fatalf("Call(%q, false) with unreachable if branches returned error: %v", "run", err)
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewBool(true)}, CallOptions{}); err != nil {
		t.Fatalf("Call(%q, true) with unreachable if branches returned error: %v", "run", err)
	}
	if _, err := script.Call(context.Background(), "typed", nil, CallOptions{}); err != nil {
		t.Fatalf("Call(%q) with unreachable typed branches returned error: %v", "typed", err)
	}
	if _, err := script.Call(context.Background(), "typed_ternary", nil, CallOptions{}); err != nil {
		t.Fatalf("Call(%q) with unreachable typed ternary branch returned error: %v", "typed_ternary", err)
	}
	if _, err := script.Call(context.Background(), "exiting", nil, CallOptions{}); err != nil {
		t.Fatalf("Call(%q) with statically exiting branch returned error: %v", "exiting", err)
	}
}

func TestCheckRegexLiteralConditionIsStaticallyTruthy(t *testing.T) {
	t.Parallel()

	// A regex literal is always truthy, so a typed function whose only return
	// sits under `if /re/` never falls through to nil and its else, if any, is
	// unreachable. The checker must recognize this or it flags a spurious
	// return-type warning.
	script := compileScript(t, `
def typed_regex() -> int
  if /ok/
    1
  end
end

def typed_regex_else() -> int
  if /ok/
    1
  else
    "bad"
  end
end
`)

	requireNoCheckWarnings(t, script)
	if _, err := script.Call(context.Background(), "typed_regex", nil, CallOptions{}); err != nil {
		t.Fatalf("Call(typed_regex) returned error: %v", err)
	}
	if _, err := script.Call(context.Background(), "typed_regex_else", nil, CallOptions{}); err != nil {
		t.Fatalf("Call(typed_regex_else) returned error: %v", err)
	}
}

func TestCheckWarningsResolveDefaultRequiredModuleEnumExportsInParameterOrder(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(_loader = require("enum_status"), status: Status = :draft) -> Status
  status
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after default-required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after default-required module enum export = %#v, want Status::Draft", got)
	}

	callWarnings := script.CheckWarningsForCall("run", nil, CallOptions{
		Keywords: map[string]Value{"status": NewSymbol("draft")},
	})
	if len(callWarnings) > 0 {
		t.Fatalf("CheckWarningsForCall() after default-required module enum export = %#v, want none", callWarnings)
	}
	got, err = script.Call(context.Background(), "run", nil, CallOptions{
		Keywords: map[string]Value{"status": NewSymbol("draft")},
	})
	if err != nil {
		t.Fatalf("Call() with keyword after default-required module enum export returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() with keyword after default-required module enum export = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsDoNotLetFutureBindingsShadowDefaultRequiredModuleExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(_loader = require("enum_status"), require = nil, status: Status = :draft) -> Status
  body_require = require
  status
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after default-required module enum export with future bindings returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after default-required module enum export with future bindings = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsDoNotTreatShadowedRequireAsModuleImport(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def normalize(status: Status) -> Status
  status
end

def run(require)
  require("enum_status")
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
}

func TestCheckWarningsWithOptionsDoNotTreatHostRequireAsModuleImport(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def normalize(status: Status) -> Status
  status
end

def run()
  require("enum_status")
  normalize(:published)
end
`)
	hostRequire := NewBuiltin("host.require", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	opts := CallOptions{Globals: map[string]Value{"require": hostRequire}}

	requireCheckWarningContainsWithOptions(t, script, opts, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, opts, "unknown type Status")
}

func TestCheckWarningsValidateRequiredModuleFunctionContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		module  string
		want    string
		wantErr string
	}{
		{
			name: "typed default",
			module: `def bad(v: int = "x")
  v
end`,
			want:    "default value for v expected int, got string",
			wantErr: "argument v expected int, got string",
		},
		{
			name: "typed return",
			module: `def bad() -> int
  "x"
end`,
			want:    "return value expected int, got string",
			wantErr: "return value for bad expected int, got string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := tempModuleTree(t, moduleFile{path: "bad.vibe", content: tc.module})
			engine := mustNewEngineWithModuleRoot(t, root)
			script := compileScriptWithEngine(t, engine, `
def run()
  require("bad").bad()
end
`)

			requireCheckWarningContains(t, script, tc.want)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.wantErr)
		})
	}
}

func TestCheckWarningsDoNotMarkModuleCheckedDuringSuppressedClassBodySeeding(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "bad.vibe", content: `def bad() -> int
  "x"
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
class Loader
  require("bad")
end

def run()
  nil
end
`)

	requireCheckWarningContains(t, script, "return value expected int, got string")
}

func TestCheckWarningsRecheckModuleExportsPerCallerContext(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t,
		moduleFile{path: "status.vibe", content: `enum Status
  Draft
end
`},
		moduleFile{path: "consumer.vibe", content: `def make() -> Status
  :draft
end
`},
	)
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
def run(flag)
  if flag
    require("status")
    require("consumer").make()
  else
    require("consumer").make()
  end
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", []Value{NewBool(false)}, CallOptions{}, "unknown type Status")
}

func TestCheckWarningsValidateRequiredModuleInitializers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		module string
	}{
		{
			name: "module entrypoint",
			module: `JSON.parse("{}", "extra")
`,
		},
		{
			name: "class body",
			module: `class Boot
  JSON.parse("{}", "extra")
end
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := tempModuleTree(t, moduleFile{path: "bad.vibe", content: tc.module})
			engine := mustNewEngineWithModuleRoot(t, root)
			script := compileScriptWithEngine(t, engine, `
def run()
  require("bad")
end
`)

			requireCheckWarningContains(t, script, "call to JSON.parse has too many arguments")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "JSON.parse expects a single JSON string argument")
		})
	}
}

func TestCheckWarningsValidateRequiredModuleFunctionsWithCallerRequiredExports(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t,
		moduleFile{path: "status.vibe", content: `enum Status
  Draft
end
`},
		moduleFile{path: "consumer.vibe", content: `def normalize(status: Status) -> Status
  status
end
`},
	)
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  require("status")
  require("consumer").normalize(:draft)
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() with caller-required enum available to required module returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() with caller-required enum available to required module = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsValidateRequiredModuleFunctionsWithHostGlobals(t *testing.T) {
	t.Parallel()

	hostScript := compileScript(t, `
enum Status
  Draft
end
`)
	hostStatus := NewEnum(hostScript.enums["Status"])
	root := tempModuleTree(t, moduleFile{path: "consumer.vibe", content: `def normalize(status: Status) -> Status
  status
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
def run
  require("consumer").normalize(:draft)
end
`)
	opts := CallOptions{Globals: map[string]Value{"Status": hostStatus}}

	requireNoCheckWarningsWithOptions(t, script, opts)
	got, err := script.Call(context.Background(), "run", nil, opts)
	if err != nil {
		t.Fatalf("Call() with host enum available to required module returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() with host enum available to required module = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsValidateTypedDefaultsAndReturns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "typed default parameter",
			source: `def run(v: int = "bad")
  v
end`,
			want: "default value for v expected int, got string",
		},
		{
			name: "explicit return value",
			source: `def run() -> int
  return "bad"
end`,
			want: "return value expected int, got string",
		},
		{
			name: "implicit literal return value",
			source: `def run() -> int
  "bad"
end`,
			want: "return value expected int, got string",
		},
		{
			name: "empty typed function body",
			source: `def run() -> int
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "if statement without else",
			source: `def run(flag) -> int
  if flag
    1
  end
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "ternary nil branch",
			source: `def run(flag) -> int
  flag ? nil : 1
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "typed method return",
			source: `class Box
  def take() -> int
    "bad"
  end
end`,
			want: "return value expected int, got string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

func TestCheckWarningsPreserveTryReturnRequireStateForDeferredReturnChecks(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  begin
    require("enum_status")
    return :draft
  ensure
    nil
  end
end
`)

	requireNoCheckWarnings(t, script)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call() after begin-return require with ensure returned error: %v", err)
	}
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("Call() after begin-return require with ensure = %#v, want Status::Draft", got)
	}
}

func TestCheckWarningsValidateStaticCallContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "function argument type",
			source: `def accept(v: int)
  v
end

def run()
  accept("bad")
end`,
			want: "call to accept argument v expected int, got string",
		},
		{
			name: "function arity",
			source: `def one(v)
  v
end

def run()
  one(1, 2)
end`,
			want: "call to one has unexpected positional arguments",
		},
		{
			name: "function call member argument type",
			source: `def accept(v: int)
  v
end

def run()
  accept.call("bad")
end`,
			want: "call to accept.call argument v expected int, got string",
		},
		{
			name: "auto-invoked function call member arity",
			source: `def accept(v)
  v
end

def run()
  accept.call
end`,
			want: "call to accept.call is missing argument v",
		},
		{
			name: "typed rest argument",
			source: `def collect(*items: array<int>)
  items
end

def run()
  collect(1, "bad")
end`,
			want: "call to collect argument items expected array<int>, got array<int | string>",
		},
		{
			name: "typed keyword rest argument",
			source: `def accept(**opts: hash<string, int>)
  opts
end

def run()
  accept(limit: "slow")
end`,
			want: "call to accept argument opts expected hash<string, int>, got { limit: string }",
		},
		{
			name: "typed block argument without block",
			source: `def accept(&block: function)
  block
end

def run()
  accept()
end`,
			want: "call to accept argument block expected function, got nil",
		},
		{
			name: "method argument type",
			source: `class Box
  def take(v: int)
    v
  end
end

def run()
  Box.new.take("bad")
end`,
			want: "call to Box#take argument v expected int, got string",
		},
		{
			name: "method arity",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take(1, 2)
end`,
			want: "call to Box#take has unexpected positional arguments",
		},
		{
			name: "constructor arity",
			source: `class Box
  def initialize(v)
  end
end

def run()
  Box.new()
end`,
			want: "call to Box.new is missing argument v",
		},
		{
			name: "auto-invoked constructor arity",
			source: `class Box
  def initialize(v)
  end
end

def run()
  Box.new
end`,
			want: "call to Box.new is missing argument v",
		},
		{
			name: "auto-invoked method arity",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take
end`,
			want: "call to Box#take is missing argument v",
		},
		{
			name: "builtin arity",
			source: `def run()
  JSON.parse("{}", "extra")
end`,
			want: "call to JSON.parse has too many arguments",
		},
		{
			name: "array fetch miss checks block",
			source: `def run()
  [1].fetch(2) do
    JSON.parse()
  end
end`,
			want: "call to JSON.parse has too few arguments",
		},
		{
			name: "uuid block",
			source: `def run()
  uuid do
    "x"
  end
end`,
			want: "call to uuid does not accept a block",
		},
		{
			name: "random id block",
			source: `def run()
  random_id do
    "x"
  end
end`,
			want: "call to random_id does not accept a block",
		},
		{
			name: "regex replace block",
			source: `def run()
  Regex.replace("a", "a", "b") do
    "x"
  end
end`,
			want: "call to Regex.replace does not accept a block",
		},
		{
			name: "regex replace all block",
			source: `def run()
  Regex.replace_all("a", "a", "b") do
    "x"
  end
end`,
			want: "call to Regex.replace_all does not accept a block",
		},
		{
			name: "array builtin arity",
			source: `def run()
  [1, 2].fetch()
end`,
			want: "call to array.fetch has too few arguments",
		},
		{
			name: "empty if consequent",
			source: `def run(flag) -> int
  if flag
  else
    1
  end
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "try rescue return value",
			source: `def run() -> int
  begin
    1
  rescue RuntimeError
    "bad"
  end
end`,
			want: "return value expected int, got string",
		},
		{
			name: "begin ensure cleanup does not mask body result",
			source: `def run() -> int
  begin
    nil
  ensure
    1
  end
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "break value",
			source: `def run()
  while true
    break JSON.parse()
  end
end`,
			want: "call to JSON.parse has too few arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

func TestCheckWarningsHonorRuntimeContractSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "rescue types are error classes",
			source: `def run()
  begin
    1 / 0
  rescue RuntimeError
    nil
  end
end`,
		},
		{
			name: "unreachable tail after explicit return",
			source: `def run() -> int
  return 1
  nil
end`,
		},
		{
			name: "unreachable tail after explicit return skips call checks",
			source: `def run()
  return 1
  JSON.parse()
end`,
		},
		{
			name: "unreachable tail after raise",
			source: `def run() -> int
  raise "boom"
  nil
end`,
		},
		{
			name: "unreachable tail after raise skips call checks",
			source: `def run()
  raise "boom"
  JSON.parse()
end`,
		},
		{
			name: "unreachable tail after break skips call checks",
			source: `def run()
  while true
    break
    JSON.parse()
  end
end`,
		},
		{
			name: "unreachable tail after next skips call checks",
			source: `def run()
  for item in [1]
    next
    JSON.parse()
  end
end`,
		},
		{
			name: "begin body return skips unreachable else",
			source: `def run() -> int
  begin
    return 1
  rescue RuntimeError
    2
  else
    "bad"
  end
end`,
		},
		{
			name: "begin body return skips unreachable else call checks",
			source: `def run()
  begin
    return 1
  rescue RuntimeError
    2
  else
    JSON.parse()
  end
end`,
		},
		{
			name: "begin body conditional return skips unreachable else",
			source: `def run(flag) -> int
  begin
    if flag
      return 1
    else
      return 2
    end
  rescue RuntimeError
    3
  else
    "bad"
  end
end`,
		},
		{
			name: "begin body return makes following statements unreachable",
			source: `def run()
  begin
    return 1
  rescue RuntimeError
    return 2
  end
  JSON.parse()
end`,
		},
		{
			name: "ensure return overrides begin body result",
			source: `def run() -> int
  begin
    nil
  ensure
    return 1
  end
end`,
		},
		{
			name: "ensure raise overrides begin body result",
			source: `def run() -> int
  begin
    nil
  ensure
    raise "boom"
  end
end`,
		},
		{
			name: "ensure return masks body return type",
			source: `def run() -> int
  begin
    return "bad"
  ensure
    return 1
  end
end`,
		},
		{
			name: "ensure return masks rescue return type",
			source: `def run() -> int
  begin
    raise "boom"
  rescue RuntimeError
    return "bad"
  ensure
    return 1
  end
end`,
		},
		{
			name: "ensure return masks else return type",
			source: `def run() -> int
  begin
    1
  rescue RuntimeError
    2
  else
    return "bad"
  ensure
    return 1
  end
end`,
		},
		{
			name: "ensure return makes following statements unreachable",
			source: `def run()
  begin
    1
  ensure
    return 1
  end
  JSON.parse()
end`,
		},
		{
			name: "parenless options hash",
			source: `def configure(opts: { retries: int })
  opts[:retries]
end

def run()
  configure retries: 3
end`,
		},
		{
			name: "parenthesized function options hash",
			source: `def configure(opts: { retries: int })
  opts[:retries]
end

def run()
  configure(retries: 3)
end`,
		},
		{
			name: "keyword rest excludes consumed keywords",
			source: `def configure(mode: string, **opts: hash<string, int>)
  opts
end

def run()
  configure(mode: "fast", retries: 3)
end`,
		},
		{
			name: "enum return symbol coercion",
			source: `enum Status
  Draft
end

def run() -> Status
  :draft
end`,
		},
		{
			name: "enum argument symbol coercion",
			source: `enum Status
  Draft
end

def identity(status: Status) -> Status
  status
end

def run()
  identity(:draft)
end`,
		},
		{
			name: "local function binding shadows top-level function",
			source: `def target(a)
  a
end

def optional(a = 1)
  a
end

def run()
  target = optional
  target()
end`,
		},
		{
			name: "local receiver shadows builtin namespace",
			source: `def run(JSON)
  JSON.parse()
end`,
		},
		{
			name: "shadowed class receiver skips chained constructor check",
			source: `class Box
  def take()
    1
  end
end

def run(Box)
  Box.new.take(1, 2, 3)
end`,
		},
		{
			name: "explicit member call with arguments is not auto-checked as empty",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take(1)
end`,
		},
		{
			name: "bare non-auto namespace member stays callable value",
			source: `def run()
  JSON.parse
end`,
		},
		{
			name: "reassigned builtin namespace member skips builtin contract",
			source: `def parse(raw, extra)
  raw
end

def run()
  JSON.parse = parse
  JSON.parse("{}", "extra")
end`,
		},
		{
			name: "safe navigation nil receiver skips arguments and block",
			source: `def run()
  nil&.profile(JSON.parse()) do
    JSON.parse()
  end
end`,
		},
		{
			name: "assert accepts ignored extra arguments",
			source: `def run()
  assert true, "ok", "ignored"
end`,
		},
		{
			name: "money ignores keywords and block",
			source: `def run()
  money("1.00 USD", currency: "USD") do
    "ignored"
  end
end`,
		},
		{
			name: "typed block argument with function contract",
			source: `def accept(&block: function)
  block
end

def run()
  accept() do
    1
  end
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckWarningsCollectCallArgumentRequiresBeforeBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "positional argument require",
			source: `def normalize(_loader = require("enum_status"), status: Status) -> Status
  status
end

def run()
  normalize(require("enum_status"), :draft)
end`,
		},
		{
			name: "keyword argument require",
			source: `def normalize(_loader = require("enum_status"), status: Status = :draft) -> Status
  status
end

def run()
  normalize(_loader: require("enum_status"), status: :draft)
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := moduleTestEngine(t)
			script := compileScriptWithEngine(t, engine, tc.source)
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckWarningsTrackShadowingInStatementOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "later local function binding does not shadow earlier top-level call",
			source: `def target(a)
  a
end

def optional(a = 1)
  a
end

def run()
  target(1, 2)
  target = optional
end`,
			want: "call to target has unexpected positional arguments",
		},
		{
			name: "branch local function binding does not shadow following top-level call",
			source: `def target(a)
  a
end

def optional(a = 1)
  a
end

def run(flag)
  if flag
    target = optional
  end
  target(1, 2)
end`,
			want: "call to target has unexpected positional arguments",
		},
		{
			name: "later builtin namespace member reassignment does not shadow earlier call",
			source: `def parse(raw, extra)
  raw
end

def run()
  JSON.parse("{}", "extra")
  JSON.parse = parse
end`,
			want: "call to JSON.parse has too many arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

func TestRuntimeResolvesAllUnionMembersBeforeMatching(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def accept(v: int | Missing) -> int | Missing
  v
end

def run()
  accept(1)
end
`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Missing")
}

func TestCheckWarningsFlagUndefinedNameInFunctionBody(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  missing_name
end
`)

	requireCheckWarningContains(t, script, "undefined variable missing_name")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "undefined variable missing_name")
}

func TestCheckWarningsFlagUndefinedFunctionCallInFunctionBody(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  helper(1)
end
`)

	requireCheckWarningContains(t, script, "undefined variable helper")
}

func TestCheckWarningsFlagTypoedLocalReference(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  count = 1
  countr + 1
end
`)

	requireCheckWarningContains(t, script, "undefined variable countr")
}

func TestCheckWarningsFlagUndefinedNameInTopLevelSnippet(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet("missing_name\n", "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	requireCheckWarningContains(t, script, "undefined variable missing_name")
}

func TestCheckWarningsForCallFlagUndefinedNamesOnExecutionPath(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  missing_name
end
`)

	warnings := script.CheckWarningsForCall("run", nil, CallOptions{})
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "undefined variable missing_name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %#v, want undefined variable missing_name", warnings)
	}
}

func TestCheckOrderIndependentWarningsCoverUncalledFunctions(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`
def uncalled()
  missing_name
end
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	if warnings := script.CheckWarningsForCall("run", nil, CallOptions{}); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForCall() = %#v, want none for the empty entrypoint", warnings)
	}
	warnings := script.CheckOrderIndependentWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "undefined variable missing_name") {
		t.Fatalf("CheckOrderIndependentWarnings() = %#v, want undefined variable missing_name", warnings)
	}
}

func TestCheckOrderIndependentWarningsDropStateSensitiveWarnings(t *testing.T) {
	t.Parallel()

	// The Missing annotation is a state-sensitive warning (a host global or a
	// require executed elsewhere could bind it), so the order-independent
	// subset must not include it.
	script := compileScript(t, `
def accept(v: Missing) -> Missing
  v
end
`)

	requireCheckWarningContains(t, script, "unknown type Missing")
	if warnings := script.CheckOrderIndependentWarnings(); len(warnings) > 0 {
		t.Fatalf("CheckOrderIndependentWarnings() = %#v, want none", warnings)
	}
}

func TestCheckWarningsFlagBareErrorClassNameOutsideRaise(t *testing.T) {
	t.Parallel()

	// Canonical error class names resolve without an env binding only in the
	// raise class position; everywhere else the runtime raises undefined
	// variable, so the checker mirrors that.
	script := compileScript(t, `
def classify(err)
  case err
  when TypeError
    "type"
  else
    "other"
  end
end
`)

	requireCheckWarningContains(t, script, "undefined variable TypeError")
}

func TestCheckWarningsResolveRaiseErrorClassAndMessageNames(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  raise RuntimeError, "boom"
end
`))
}

func TestCheckWarningsResolveRequiredModuleExportNames(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "mathx.vibe"), []byte(`export def double(x)
  x * 2
end

export def triple(x)
  x * 3
end
`), 0o644); err != nil {
		t.Fatalf("write mathx module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})

	// Requires bind exports into the call root at runtime, so exports are
	// legal references from any function, not only the one that requires.
	requireNoCheckWarnings(t, compileScriptWithEngine(t, engine, `
def run()
  require("mathx")
  double(2)
end

def helper()
  triple(3)
end
`))
}

func TestCheckWarningsResolveRequiredModuleAliasNames(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "mathx.vibe"), []byte(`export def double(x)
  x * 2
end
`), 0o644); err != nil {
		t.Fatalf("write mathx module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})

	requireNoCheckWarnings(t, compileScriptWithEngine(t, engine, `
def run()
  require("mathx", as: "MathX")
  MathX.double(2)
end

def alias_value()
  MathX
end
`))
}

func TestCheckWarningsFlagUndefinedNameInBlockArg(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def id(&b)
  b
end

def run()
  id(&missing_name)
end
`)

	requireCheckWarningContains(t, script, "undefined variable missing_name")
}

func TestCheckWarningsFlagUndefinedNameInSplatArg(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def take(*args)
  args
end

def run()
  take(*missing)
end
`)

	requireCheckWarningContains(t, script, "undefined variable missing")
}

func TestCheckWarningsResolveRequireInsideSplatArg(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "mathx.vibe"), []byte(`export def double(x)
  x * 2
end
`), 0o644); err != nil {
		t.Fatalf("write mathx module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})

	requireNoCheckWarnings(t, compileScriptWithEngine(t, engine, `
def take(*args)
  args
end

def run()
  take(*[require("mathx")])
end

def helper()
  double(3)
end
`))
}

func TestCheckWarningsResolveRequireInsideBlockArg(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "mathx.vibe"), []byte(`export def double(x)
  x * 2
end
`), 0o644); err != nil {
		t.Fatalf("write mathx module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})

	requireNoCheckWarnings(t, compileScriptWithEngine(t, engine, `
def id(&b)
  b
end

def run()
  id(&require("mathx").double)
end

def helper()
  double(3)
end
`))
}

func TestCheckWarningsSuppressUndefinedNamesAfterNonStaticRequireAlias(t *testing.T) {
	t.Parallel()

	// An as: alias that is not a string or symbol literal cannot be folded
	// statically, so the script opts out of undefined-name checking exactly
	// like a dynamic require target.
	requireNoCheckWarnings(t, compileScript(t, `
def run(alias_name)
  require("mathx", as: alias_name)
  something_from_module
end
`))
}

func TestCheckWarningsSeeRequiresInsideNextValues(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "mathx.vibe"), []byte(`export def double(x)
  x * 2
end
`), 0o644); err != nil {
		t.Fatalf("write mathx module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})

	// A require reached only through a next value still binds its exports
	// for the whole script.
	requireNoCheckWarnings(t, compileScriptWithEngine(t, engine, `
def run()
  i = 0
  while i < 1
    i += 1
    next require("mathx")
  end
  double(2)
end
`))
}

func TestCheckWarningsRespectNextValueBindings(t *testing.T) {
	t.Parallel()

	// A statement-expression in a next value binds locals in the enclosing
	// scope; the union model must include them.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  i = 0
  while i < 3
    i += 1
    if i > 1
      puts captured
    end
    next begin
      captured = i
    end
  end
end
`))
}

func TestCheckWarningsSuppressUndefinedNamesAfterDynamicRequire(t *testing.T) {
	t.Parallel()

	// A require whose module name is not statically resolvable can bind
	// arbitrary export names, so the whole script opts out of the check.
	requireNoCheckWarnings(t, compileScript(t, `
def load_it(name)
  require(name)
  something_from_module
end
`))
}

func TestCheckWarningsResolveBuiltinAndKernelNames(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  puts "hi"
  print "x"
  values = [rand, now, uuid, JSON.stringify({a: 1})]
  if block_given?
    yield values
  end
  values
end
`))
}

func TestCheckWarningsResolveEnumAndClassNamesForwardReferences(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  point = Point.new(1, 2)
  [Status::Draft, Status.values, point.sum, later(1)]
end

def later(x)
  x + 1
end

enum Status
  Draft
  Live
end

class Point
  property x
  property y

  def initialize(x, y)
    @x = x
    @y = y
  end

  def sum
    x + y
  end
end
`))
}

func TestCheckWarningsResolveImplicitSelfMembers(t *testing.T) {
	t.Parallel()

	// Methods and class bodies run with self bound, so bare identifiers can
	// resolve through implicit self member lookup the checker cannot model.
	requireNoCheckWarnings(t, compileScript(t, `
class Greeter
  getter name

  register_defaults

  def self.register_defaults
    "registered"
  end

  def initialize(name)
    @name = name
  end

  def greet
    prefix + name
  end

  def prefix
    "hello "
  end

  def keep_self
    self
  end

  def self.build
    default_label
  end

  def self.default_label
    "greeter"
  end
end
`))
}

func TestCheckWarningsResolveRescueBindingsAndSkippedClauseLocals(t *testing.T) {
	t.Parallel()

	// Locals from the begin body and from every rescue clause are predeclared
	// once the statement completes, and a later clause sees the (skipped)
	// earlier clauses' locals as nils, mirroring the runtime.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  begin
    risky = 1
  rescue TypeError => te
    first_fallback = te
  rescue RuntimeError => e
    second_fallback = [e, first_fallback]
  end
  [risky, first_fallback, second_fallback]
end
`))
}

func TestCheckWarningsResolveBlockParameterNames(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, 2].each do |v|
    puts v
  end
  pairs = [[1, 2]].map do |(a, b)|
    a + b
  end
  doubled = [3].map { _1 * 2 }
  bumped = [4].map { it + 1 }
  [pairs, doubled, bumped]
end
`))
}

func TestCheckWarningsResolveLocalBindingForms(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  lazy ||= 3
  a, b = [1, 2]
  for i in [1, 2]
    puts i
  end
  [lazy, a, b, i]
end
`))
}

func TestCheckWarningsRespectLocalsBoundLaterInBody(t *testing.T) {
	t.Parallel()

	// The union model predeclares every name a body can bind, so a read
	// before the assignment stays silent even though the runtime fails on
	// that path. The over-approximation only ever suppresses warnings.
	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`
puts value
value = 1
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsForCallResolveCallTimeHostGlobals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  host_value
end
`)

	// Without the host's options the reference cannot resolve.
	requireCheckWarningContains(t, script, "undefined variable host_value")

	// Hosts check with the same options they later pass to Call; a name
	// supplied through CallOptions.Globals must never be reported.
	opts := CallOptions{Globals: map[string]Value{"host_value": NewInt(7)}}
	if warnings := script.CheckWarningsForCall("run", nil, opts); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForCall() = %#v, want none with host global", warnings)
	}
	requireNoCheckWarningsWithOptions(t, script, opts)

	got, err := script.Call(context.Background(), "run", nil, opts)
	if err != nil {
		t.Fatalf("Call() with host global returned error: %v", err)
	}
	if !got.Equal(NewInt(7)) {
		t.Fatalf("Call() with host global = %s, want 7", got)
	}
}

func TestCheckWarningsResolveCapabilityGlobalsFromOptions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  ctx
end
`)

	requireCheckWarningContains(t, script, "undefined variable ctx")
	requireNoCheckWarningsWithOptions(t, script, CallOptions{
		Capabilities: []CapabilityAdapter{checkOptionGlobalsCapability{"ctx": NewString("host")}},
	})
}

func TestCheckWarningsFlagLiteralArrayBlockParamTypeMismatch(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  ["x"].map do |value: int|
    value
  end
end
`)

	requireCheckWarningContains(t, script, "argument value expected int, got string")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "argument value expected int, got string")
}

func TestCheckWarningsFlagLiteralArrayBlockParamMismatchOnAnyElement(t *testing.T) {
	t.Parallel()

	// The runtime fails on the first yielded element that misses the
	// annotation, so one contradicting element is enough.
	script := compileScript(t, `
def run()
  [1, "x"].each do |v: int|
    v
  end
end
`)

	requireCheckWarningContains(t, script, "argument v expected int, got string")
}

func TestCheckWarningsFlagLiteralArrayBlockParamMismatchAcrossIterators(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"each", "map", "select", "reject", "find"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
def run()
  ["x"].`+method+` do |v: int|
    v
  end
end
`)
			requireCheckWarningContains(t, script, "argument v expected int, got string")
		})
	}
}

func TestCheckWarningsSkipLaterFindElements(t *testing.T) {
	t.Parallel()

	// find stops yielding on the first truthy block result, so only the
	// first element is guaranteed to reach the annotation check.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, "x"].find do |v: int|
    v > 0
  end
end
`))

	script := compileScript(t, `
def run()
  ["x", 1].find do |v: int|
    v > 0
  end
end
`)
	requireCheckWarningContains(t, script, "argument v expected int, got string")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "argument v expected int, got string")
}

func TestCheckWarningsSkipLaterElementsWhenBlockCanEscape(t *testing.T) {
	t.Parallel()

	// A break, return, or raise in the body can end the iteration before a
	// later mismatched element is yielded, so those bodies only pin the
	// first element.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, "x"].each do |v: int|
    break
  end
end
`))

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, "x"].each do |v: int|
    return v
  end
end
`))

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  begin
    [1, "x"].each do |v: int|
      raise RuntimeError, "stop"
    end
  rescue => e
    e
  end
end
`))

	// A retry unwinds out of the block (to an enclosing rescue, or as a
	// local-jump error at the yield boundary), so it can also end the
	// iteration before a later mismatched element is yielded.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, "x"].each do |v: int|
    retry
  end
end
`))

	// The first element is always yielded, so a mismatch there still warns
	// even when the body can escape.
	script := compileScript(t, `
def run()
  ["x", 1].each do |v: int|
    break
  end
end
`)
	requireCheckWarningContains(t, script, "argument v expected int, got string")
}

func TestCheckWarningsKeepLaterElementsWhenBlockOnlySkips(t *testing.T) {
	t.Parallel()

	// next moves to the following iteration without ending it, so every
	// element is still yielded and later mismatches stay flagged.
	script := compileScript(t, `
def run()
  [1, "x"].each do |v: int|
    next
  end
end
`)
	requireCheckWarningContains(t, script, "argument v expected int, got string")
}

func TestCheckWarningsFlagLiteralArrayBlockParamFloatIntMismatch(t *testing.T) {
	t.Parallel()

	// Block parameter contracts do not coerce int to float, so the checker
	// mirrors the runtime rejection.
	script := compileScript(t, `
def run()
  [1].map do |v: float|
    v
  end
end
`)

	requireCheckWarningContains(t, script, "argument v expected float, got int")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "argument v expected float, got int")
}

func TestCheckWarningsFlagEachWithIndexBlockParamMismatches(t *testing.T) {
	t.Parallel()

	element := compileScript(t, `
def run()
  ["x"].each_with_index do |v: int, i: int|
    [v, i]
  end
end
`)
	requireCheckWarningContains(t, element, "argument v expected int, got string")

	index := compileScript(t, `
def run()
  [1].each_with_index do |v: int, i: string|
    [v, i]
  end
end
`)
	requireCheckWarningContains(t, index, "argument i expected string, got int")
	requireCallErrorContains(t, index, "run", nil, CallOptions{}, "argument i expected string, got int")
}

func TestCheckWarningsSkipBlockParamTypesForNonLiteralReceivers(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run(xs)
  xs.map do |v: int|
    v
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForCompatibleUnions(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1, "x"].each do |v: int | string|
    v
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForDestructuredParams(t *testing.T) {
	t.Parallel()

	// Array elements destructure into the parenthesized targets, so the
	// element parameter shape no longer matches a single scalar yield.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [[1, 2]].map do |(a: int, b: int)|
    a + b
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForUntypedParams(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  ["x"].map do |v|
    v
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForUserDefinedIterators(t *testing.T) {
	t.Parallel()

	// Yield sites inside user functions are runtime-enforced; the literal
	// receiver check only covers builtin array iterators.
	requireNoCheckWarnings(t, compileScript(t, `
def my_each(items)
  yield items[0]
end

def run()
  my_each(["x"]) do |v: int|
    v
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForHashReceivers(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  {a: 1}.each do |k: string, v: int|
    [k, v]
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForEmptyLiteralReceivers(t *testing.T) {
	t.Parallel()

	// An empty literal never yields, so the runtime never rejects the block.
	script := compileScript(t, `
def run()
  [].each do |v: int|
    v
  end
end
`)

	requireNoCheckWarnings(t, script)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("Call() over empty literal returned error: %v", err)
	}
}

func TestCheckWarningsSkipBlockParamTypesForExtraParams(t *testing.T) {
	t.Parallel()

	// A second parameter on a single-yield iterator binds nil at runtime;
	// the checker stays silent rather than model that shape.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  [1].each do |v: int, extra: string|
    [v, extra]
  end
end
`))
}

func TestCheckWarningsSkipBlockParamTypesForUncoveredIterators(t *testing.T) {
	t.Parallel()

	// reverse_each yields in reverse order and each_slice yields arrays;
	// both stay outside the covered iterator set.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  ["x"].reverse_each do |v: int|
    v
  end
  [1, 2].each_slice(2) do |pair: int|
    pair
  end
end
`))
}

func TestCheckWarningsResolveModuleAndDirectiveNames(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
module Named
  def display_name
    "named"
  end
end

module Billing
  LIMIT = 100

  module Codes
    PREFIX = "B"
  end

  def self.code
    "B-1"
  end
end

class Invoice
  include Named
  extend Billing

  protected def guard
    1
  end

  def total
    Billing::LIMIT
  end

  private :guard
end

def run
  handle = Billing
  [handle.code(), Billing::LIMIT, Billing::Codes::PREFIX, Invoice.new.total]
end
`)

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsStillFlagUndefinedNamesAlongsideModules(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
module Billing
  LIMIT = 100
end

def run
  Billing::LIMIT + missing_name
end
`)

	requireCheckWarningContains(t, script, "undefined variable missing_name")
}

func TestCheckWarningsResolveLocalsBoundInsideDestructureIndexTargets(t *testing.T) {
	t.Parallel()

	// The index selector's begin/end body runs in the enclosing scope, so c
	// is bound by the time the trailing reference evaluates.
	requireNoCheckWarnings(t, compileScript(t, `
def run()
  b = [0]
  a, b[begin
    c = 1
    0
  end] = [1, 2]
  c
end
`))
}

func requireNoCheckWarnings(t *testing.T, script *Script) {
	t.Helper()

	if warnings := script.CheckWarnings(); len(warnings) > 0 {
		t.Fatalf("CheckWarnings() = %#v, want none", warnings)
	}
}

func requireNoCheckWarningsWithOptions(t *testing.T, script *Script, opts CallOptions) {
	t.Helper()

	if warnings := script.CheckWarningsWithOptions(opts); len(warnings) > 0 {
		t.Fatalf("CheckWarningsWithOptions() = %#v, want none", warnings)
	}
}

func requireCheckWarningContains(t *testing.T, script *Script, want string) {
	t.Helper()

	requireCheckWarningContainsWithOptions(t, script, CallOptions{}, want)
}

func requireCheckWarningContainsWithOptions(t *testing.T, script *Script, opts CallOptions, want string) {
	t.Helper()

	warnings := script.CheckWarningsWithOptions(opts)
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	got := strings.Join(messages, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("CheckWarningsWithOptions() = %q, want substring %q", got, want)
	}
}
