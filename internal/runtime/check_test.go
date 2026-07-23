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

func TestCheckWarningsForFunctionTracksCollapsedOptionsHashFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "binds synthesized hash",
			source: `def takes_string(value: string)
  value
end

def target(opts = "default")
  takes_string(opts)
end

def run()
  target(name: 1)
end`,
			want: "call to takes_string argument value expected string, got { name: int }",
		},
		{
			name: "consumes later keyword names",
			source: `def takes_string(value: string)
  value
end

def target(opts = "unused", later = "safe")
  takes_string(later)
end

def run()
  target(name: 1, later: 2)
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			warnings := script.CheckWarningsForFunction("run")
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, want none", "run", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, tc.want)
			}
		})
	}
}

func TestCheckWarningsForFunctionChecksBareMemberBeforeItsNamespaceEffects(t *testing.T) {
	t.Parallel()

	const source = `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Installer
  def self.fire
    takes_int(JSON.stringify({}))
    JSON.stringify = replacement
  end
end

def run
  $CALL
end
	`

	for _, call := range []string{"Installer.fire", "Installer.fire()"} {
		t.Run(call, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, strings.Replace(source, "$CALL", call, 1))
			warnings := script.CheckWarningsForFunction("run")
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			want := "call to takes_int argument value expected int, got string"
			if !strings.Contains(got, want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
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

func TestCheckWarningsForFunctionRechecksReachableFunctionsPerNamespaceState(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def replacement(value)
  value
end

def parse()
  JSON.parse()
end

def run(flag: bool)
  if flag
    JSON.parse = replacement
    parse()
  else
    parse()
  end
end
`)

	warnings := script.CheckWarningsForFunction("run")
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	got := strings.Join(messages, "\n")
	want := "call to JSON.parse has too few arguments"
	if !strings.Contains(got, want) {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
	}
}

func TestReachableFunctionCheckKeyIncludesNamespaceMutations(t *testing.T) {
	t.Parallel()

	checker := scriptChecker{}
	fn := &ScriptFunction{}
	before := checker.reachableFunctionCheckKey(fn, nil)
	checker.runtimeNamespaceMembers = map[string]struct{}{"JSON.stringify": {}}
	after := checker.reachableFunctionCheckKey(fn, nil)
	if before == after {
		t.Fatalf("reachable function key stayed %q after namespace mutation", before)
	}
}

func TestScriptFunctionNamespaceMutationsResolveSelfClassAssignments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name: "instance method",
			source: `class Holder
  def stash(value)
    self.class.User = value
  end
end
`,
			want: []string{"Holder.User"},
		},
		{
			name: "included method uses receiver class",
			source: `module Mutator
  def self.User=(value)
    value
  end

  def stash(value)
    self.class.User = value
  end
end

class Holder
  include Mutator
end
`,
			want: []string{"Holder.User"},
		},
		{
			name: "class setter effects",
			source: `def replacement(value)
  value
end

class Holder
  def self.User=(value)
    JSON.stringify = replacement
  end

  def stash(value)
    self.class.User = value
  end
end
`,
			want: []string{"JSON.stringify"},
		},
		{
			name: "getter-only member",
			source: `class Holder
  def self.User()
    1
  end

  def stash(value)
    self.class.User = value
  end
end
`,
		},
		{
			name: "destructured assignment",
			source: `class Holder
  def stash(value)
    self.class.User, ignored = [value, nil]
  end
end
`,
			want: []string{"Holder.User"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			fn := script.classes["Holder"].Methods["stash"]
			checker := &scriptChecker{script: script}
			got := checker.scriptFunctionNamespaceMutations(nil, staticCallable{
				name: "Holder#stash",
				fn:   fn,
			})
			if len(got) != len(tc.want) {
				t.Fatalf("scriptFunctionNamespaceMutations() = %v, want %v", got, tc.want)
			}
			for _, member := range tc.want {
				if _, ok := got[member]; !ok {
					t.Fatalf("scriptFunctionNamespaceMutations() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestCheckWarningsForFunctionClearsPinnedFactsBetweenReachableChecks(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def use()
  takes_int(JSON.stringify({}))
end

def run(flag)
  if flag
    use()
  else
    require("enum_status")
    JSON.stringify = replacement
    use()
  end
end
`)

	warnings := script.CheckWarningsForFunction("run")
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("CheckWarningsForFunction(%q) reported boundary warning %d times, want 1: %#v", "run", count, warnings)
	}
}

func TestCheckWarningsForFunctionAppliesExactDynamicCallNamespaceMutations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		definitions string
		invoke      string
		wantCount   int
	}{
		{
			name: "constructor",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer][0].new()",
		},
		{
			name: "instance method",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer.new][0].install()",
		},
		{
			name: "self class setter",
			definitions: `module Mutator
  def stash(value)
    self.class.User = value
  end
end

class Holder
  include Mutator

  def self.User=(value)
    JSON.stringify = replacement
  end
end`,
			invoke: "[Holder.new][0].stash(1)",
		},
		{
			name: "class method",
			definitions: `class Installer
  def self.install()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer][0].install()",
		},
		{
			name: "ordinary call method",
			definitions: `class Installer
  def call()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer.new][0].call()",
		},
		{
			name: "forwarded constructor",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer][0].public_send(:new)",
		},
		{
			name: "forwarded constructor instance method",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer][0].public_send(:new).install()",
		},
		{
			name: "send constructor instance method",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer][0].send(:new).install()",
		},
		{
			name: "mixed class and plain module constructor",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end

module Factory
end`,
			invoke: "begin; (flag ? Installer : Factory).new.install(); rescue; nil; end",
		},
		{
			name: "module constructor override stays gradual",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end

class Other
  def install()
    nil
  end
end

module Factory
  def self.new()
    Other.new
  end
end`,
			invoke:    "(flag ? Installer : Factory).new.install()",
			wantCount: 2,
		},
		{
			name: "direct plain module constructor skips positional argument effects",
			definitions: `module Factory
end

def install()
  JSON.stringify = replacement
  []
end`,
			invoke:    "begin; Factory.new(*install()); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "forwarded plain module constructor evaluates positional argument effects",
			definitions: `module Factory
end

def install()
  JSON.stringify = replacement
  []
end`,
			invoke: "begin; Factory.send(:new, *install()); rescue; nil; end",
		},
		{
			name: "direct plain module constructor skips keyword argument effects",
			definitions: `module Factory
end

def install()
  JSON.stringify = replacement
  {}
end`,
			invoke:    "begin; Factory.new(**install()); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "forwarded plain module constructor evaluates keyword argument effects",
			definitions: `module Factory
end

def install()
  JSON.stringify = replacement
  {}
end`,
			invoke: "begin; Factory.public_send(:new, **install()); rescue; nil; end",
		},
		{
			name: "invalid direct constructor positional splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.new(*1); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "invalid direct constructor keyword splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.new(**1); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "nonempty bad-key keyword splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `bad = {}
bad[2] = "x"
begin; Installer.new(**bad); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "bad-key or scalar keyword splat always blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `bad = {}
bad[2] = "x"
value = flag ? bad : 1
begin; Installer.new(**value); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "bad-key or empty hash keyword splat may reach initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `bad = {}
bad[2] = "x"
value = flag ? bad : {}
begin; Installer.new(**value); rescue; nil; end`,
		},
		{
			name: "nested bad-key hash does not poison outer keyword splat",
			definitions: `class Installer
  def initialize(nested:)
    JSON.stringify = replacement
  end
end`,
			invoke: `outer = {nested: {}}
outer[:nested][2] = "x"
Installer.new(**outer)`,
		},
		{
			name: "deleting bad key restores keyword splat reachability",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `bad = {}
bad[2] = "x"
bad.delete(2)
Installer.new(**bad)`,
		},
		{
			name: "alias deletion clears bad-key keyword splat witness",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `bad = {}
bad[2] = "x"
copy = bad
copy.delete(2)
Installer.new(**bad)`,
		},
		{
			name: "empty bad-key typed hash can reach initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end

def empty() -> hash<int, int>
  values = {}
  values[2] = 1
  values.delete(2)
  values
end`,
			invoke: "Installer.new(**empty())",
		},
		{
			name: "invalid forwarded constructor positional splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.send(:new, *1); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "invalid forwarded constructor keyword splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.public_send(:new, **1); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "builtin argument type failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(Duration.build(weeks: "bad"), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "builtin shape failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(Duration.parse(), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "raising script call skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def abort()
  raise "boom"
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(abort(), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "raising bare zero arity function skips later statements",
			definitions: `def abort()
  raise "boom"
end`,
			invoke:    `begin; abort; rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "parameterized bare function remains a value",
			definitions: `def callback(required)
  JSON.stringify = replacement
end`,
			invoke:    `callback`,
			wantCount: 2,
		},
		{
			name: "exact zero arity callback parameter auto invokes",
			definitions: `def callback()
  JSON.stringify = replacement
end

def invoke(callback: function)
  callback
end`,
			invoke: `invoke(callback)`,
		},
		{
			name: "required implicit self bare method rejects before body",
			definitions: `class Runner
  def callback(required)
    JSON.stringify = replacement
  end

  def run()
    callback
  end
end`,
			invoke:    `begin; Runner.new.run(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "optional implicit self bare method enters body",
			definitions: `class Runner
  def callback(value = 1)
    JSON.stringify = replacement
  end

  def run()
    callback
  end
end`,
			invoke: `Runner.new.run()`,
		},
		{
			name: "required function call member rejects before body",
			definitions: `def callback(required)
  JSON.stringify = replacement
end`,
			invoke:    `begin; callback.call; rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "namespace assignment after raise stays unreachable",
			definitions: `def abort()
  raise "boom"
  JSON.stringify = replacement
end`,
			invoke:    `begin; abort(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "namespace assignment after raising call stays unreachable",
			definitions: `def boom()
  raise "boom"
end

def abort()
  boom()
  JSON.stringify = replacement
end`,
			invoke:    `begin; abort(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "namespace assignment before raise remains visible",
			definitions: `def abort()
  JSON.stringify = replacement
  raise "boom"
end`,
			invoke: `begin; abort(); rescue; nil; end`,
		},
		{
			name: "namespace assignment after return stays unreachable",
			definitions: `def target()
  return 1
  JSON.stringify = replacement
end`,
			invoke:    `target()`,
			wantCount: 2,
		},
		{
			name: "bad annotated return skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def bad() -> int
  "bad"
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(bad(), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "compatible annotated return arm keeps later arguments reachable",
			definitions: `def consume(first, second)
  nil
end

def maybe(flag: bool) -> int
  flag ? "bad" : 1
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke: `begin; consume(maybe(flag), install()); rescue; nil; end`,
		},
		{
			name: "raising default skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def abort()
  raise "boom"
end

def target(value = abort())
  1
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(target(), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "raising default skips later default and body effects",
			definitions: `def abort()
  raise "boom"
end

def mutate()
  JSON.stringify = replacement
  1
end

def target(first = abort(), second = mutate())
  JSON.stringify = replacement
end`,
			invoke:    `begin; target(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "effectful default prefix survives later raising default",
			definitions: `def before()
  JSON.stringify = replacement
  1
end

def abort()
  raise "boom"
end

def target(first = before(), second = abort())
end`,
			invoke: `begin; target(); rescue; nil; end`,
		},
		{
			name: "later ordinary parameter stays unbound during earlier default",
			definitions: `def maybe_mutate(value)
  if value
    JSON.stringify = replacement
  end
  true
end

class Installer
  def initialize(first = maybe_mutate(flag), flag = true)
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "earlier ordinary parameter is bound before later default",
			definitions: `def maybe_mutate(value)
  if value
    JSON.stringify = replacement
  end
  true
end

class Installer
  def initialize(flag = true, second = maybe_mutate(flag))
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "later ivar parameter stays unset during earlier default",
			definitions: `def maybe_mutate(value)
  if value
    JSON.stringify = replacement
  end
  true
end

class Installer
  property flag: bool

  def initialize(first = maybe_mutate(@flag), @flag = true)
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "earlier ivar parameter is bound before later default",
			definitions: `def maybe_mutate(value)
  if value
    JSON.stringify = replacement
  end
  true
end

class Installer
  property flag: bool

  def initialize(@flag = true, second = maybe_mutate(@flag))
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "ivar property rejection skips initializer body",
			definitions: `class Installer
  property flag: bool

  def initialize(@flag: bool | int)
    JSON.stringify = replacement
  end
end`,
			invoke:    `begin; Installer.new(1); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "compatible ivar parameter reaches initializer body",
			definitions: `class Installer
  property flag: bool

  def initialize(@flag: bool | int)
    JSON.stringify = replacement
  end
end`,
			invoke: `Installer.new(true)`,
		},
		{
			name: "earlier default effect survives later ivar property rejection",
			definitions: `def mutate()
  JSON.stringify = replacement
  1
end

class Installer
  property flag: bool

  def initialize(first = mutate(), @flag: bool | int = 1)
  end
end`,
			invoke: `begin; Installer.new(); rescue; nil; end`,
		},
		{
			name: "later callable parameter stays unbound during earlier default",
			definitions: `def callback()
  JSON.stringify = replacement
end

class Installer
  def initialize(first = later.call(), later: function = callback)
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "earlier callable parameter is bound before later default",
			definitions: `def callback()
  JSON.stringify = replacement
end

class Installer
  def initialize(later: function = callback, second = later.call())
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "later nominal receiver stays unbound during earlier default",
			definitions: `class Mutator
  def install()
    JSON.stringify = replacement
  end
end

class Installer
  def initialize(first = later.install(), later: Mutator = Mutator.new())
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "earlier nominal receiver is bound before later default",
			definitions: `class Mutator
  def install()
    JSON.stringify = replacement
  end
end

class Installer
  def initialize(later: Mutator = Mutator.new(), second = later.install())
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "unbound later parameter falls back to top level callable",
			definitions: `def later()
  JSON.stringify = replacement
  1
end

class Installer
  def initialize(first = later.call(), later = 1)
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "bound later parameter shadows top level callable",
			definitions: `def later()
  JSON.stringify = replacement
  1
end

class Installer
  def initialize(later = 1, second = later.call())
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "unbound later parameter falls back to implicit self method",
			definitions: `class Installer
  def later()
    JSON.stringify = replacement
    1
  end

  def initialize(first = later.call(), later = 1)
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "callable ivar default is stored without invocation",
			definitions: `def callback()
  JSON.stringify = replacement
end

class Installer
  property callback: function

  def initialize(@callback = callback)
  end
end`,
			invoke:    `Installer.new()`,
			wantCount: 2,
		},
		{
			name: "later default annotation uses earlier parameter fact",
			definitions: `def target(seed: string = "x", value: int = seed)
  JSON.stringify = replacement
end`,
			invoke:    `begin; target(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "later ivar default uses earlier parameter fact",
			definitions: `class Installer
  property name: string

  def initialize(seed: int = 1, @name = seed)
    JSON.stringify = replacement
  end
end`,
			invoke:    `begin; Installer.new(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "earlier default invalidates aliased later parameter fact",
			definitions: `def target(first, middle = first.clear(), later:)
  if later.empty?()
    JSON.stringify = replacement
  end
end`,
			invoke: `values = [1]; target(values, later: values)`,
		},
		{
			name: "callee default ignores caller local with same name",
			definitions: `def seed()
  1
end

def target(value: int = seed)
  JSON.stringify = replacement
end`,
			invoke: `seed = "bad"; target()`,
		},
		{
			name: "dynamic splat may suppress invalid optional default",
			definitions: `def bad()
  "bad"
end

def target(value: int = bad())
  JSON.stringify = replacement
end

def values(flag: bool) -> array<int>
  flag ? [1] : [2]
end`,
			invoke: `target(*values(flag))`,
		},
		{
			name: "generic callable may suppress raising optional default",
			definitions: `def bad()
  raise "stop"
end

def target(value: int = bad())
  JSON.stringify = replacement
end`,
			invoke: `[1].map(&target)`,
		},
		{
			name: "generic callable does not apply optional default fact to body",
			definitions: `def target(flag = true)
  unless flag
    JSON.stringify = replacement
  end
end`,
			invoke: `[false].map(&target)`,
		},
		{
			name: "raising callable default is stored without invocation",
			definitions: `def abort()
  raise "stop"
end

def target(callback: function = abort)
  JSON.stringify = replacement
end`,
			invoke: `target()`,
		},
		{
			name: "raising callable ivar default is stored without invocation",
			definitions: `def abort()
  raise "stop"
end

class Installer
  property callback: function

  def initialize(@callback = abort)
    JSON.stringify = replacement
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "conditional callable default branches stay inert",
			definitions: `def first()
  JSON.stringify = replacement
  raise "stop"
end

def second()
  JSON.stringify = replacement
  raise "stop"
end

def target(flag: bool, callback: function = flag ? first : second)
  nil
end`,
			invoke:    `target(flag)`,
			wantCount: 2,
		},
		{
			name: "conditional callable default reaches body",
			definitions: `def first()
  raise "stop"
end

def second()
  raise "stop"
end

def target(flag: bool, callback: function = flag ? first : second)
  JSON.stringify = replacement
end`,
			invoke: `target(flag)`,
		},
		{
			name: "conditional default follows earlier parameter fact",
			definitions: `def abort()
  raise "stop"
end

def target(flag = true, value = flag ? abort() : 1)
  JSON.stringify = replacement
end`,
			invoke:    `begin; target(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "nested callable default branches stay inert",
			definitions: `def first()
  JSON.stringify = replacement
  raise "stop"
end

def second()
  JSON.stringify = replacement
  raise "stop"
end

def target(flag: bool, callbacks: array<function> = [flag ? first : second])
  nil
end`,
			invoke:    `target(flag)`,
			wantCount: 2,
		},
		{
			name: "constructor ignores initializer return annotation",
			definitions: `class Installer
  def initialize() -> int
    JSON.stringify = replacement
    "ignored"
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "parse as raw failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(JSON.parse_as(1, {name: string}), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "parse as shape failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(JSON.parse_as("{}", 1), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name:      "parse as rejection does not invoke lambda argument",
			invoke:    `begin; JSON.parse_as("{}", -> { JSON.stringify = replacement }); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "class predicate argument failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(1.is_a?(2), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "is type atom failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(1.is_type?("array<int>"), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "assigned is type atom failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `atom = "array<int>"; begin; consume(1.is_type?(atom), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "special builtin literal splat failure skips later outer arguments",
			definitions: `def consume(first, second)
  nil
end

def install()
  JSON.stringify = replacement
  1
end`,
			invoke:    `begin; consume(1.is_a?(*[2]), install()); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "static splat preserves auto-invoked element type",
			definitions: `def make()
  1
end

def install(callback: function)
  JSON.stringify = replacement
end`,
			invoke:    `begin; install(*[make]); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "exact class value cannot expand positionally",
			definitions: `class Token
end

def install(*values)
  JSON.stringify = replacement
end`,
			invoke:    `begin; klass = Token; install(*klass); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "exact class value cannot expand as keywords",
			definitions: `class Token
end

def install(**values)
  JSON.stringify = replacement
end`,
			invoke:    `begin; klass = Token; install(**klass); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "exact class value cannot convert to block",
			definitions: `class Token
end

def install(&block)
  JSON.stringify = replacement
end`,
			invoke:    `begin; klass = Token; install(&klass); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "yielding default runs literal block before later binding failure",
			definitions: `def install(first = yield, later: int:)
end`,
			invoke: `begin
  install(later: "bad") { JSON.stringify = replacement }
rescue
  nil
end`,
		},
		{
			name: "yielding default runs script block argument before later binding failure",
			definitions: `def callback()
  JSON.stringify = replacement
end

def install(first = yield, later: int:)
end`,
			invoke: `begin
  install(later: "bad", &callback)
rescue
  nil
end`,
		},
		{
			name: "yielding default runs lambda block argument before later binding failure",
			definitions: `def install(first = yield, later: int:)
end`,
			invoke: `begin
  install(later: "bad", &-> { JSON.stringify = replacement })
rescue
  nil
end`,
		},
		{
			name: "default invokes named callback before later binding failure",
			definitions: `def callback()
  JSON.stringify = replacement
end

def install(callback: function, trigger = callback.call, later: int:)
end`,
			invoke: `begin
  install(callback, later: "bad")
rescue
  nil
end`,
		},
		{
			name: "default invokes lambda callback before later binding failure",
			definitions: `def install(callback: function, trigger = callback.call, later: int:)
end`,
			invoke: `begin
  install(-> { JSON.stringify = replacement }, later: "bad")
rescue
  nil
end`,
		},
		{
			name: "unused lambda callback stays inert before later binding failure",
			definitions: `def install(callback: function, trigger = 1, later: int:)
end`,
			invoke: `begin
  install(-> { JSON.stringify = replacement }, later: "bad")
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "successful logical ivar skip keeps namespace mutation reachable",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    @flag = value
    @flag ||= false
    if @flag
      JSON.stringify = replacement
    end
  end
end`,
			invoke: `Installer.new.install(flag)`,
		},
		{
			name: "initializer and assignment skips impossible rhs mutation",
			definitions: `def mutate()
  JSON.stringify = replacement
  true
end

class Installer
  property flag: bool

  def initialize()
    @flag &&= mutate()
  end
end`,
			invoke:    `Installer.new()`,
			wantCount: 2,
		},
		{
			name: "raising compound operator preserves prior namespace mutation",
			definitions: `class NumberBox
  def +(other)
    JSON.stringify = replacement
    raise "stop"
  end
end

class Installer
  property value: NumberBox

  def install()
    @value = NumberBox.new
    begin
      @value += 1
    rescue
      nil
    end
  end
end`,
			invoke: `Installer.new.install()`,
		},
		{
			name: "rejected compound setter preserves operator namespace mutation",
			definitions: `class NumberBox
  def +(other)
    JSON.stringify = replacement
    1
  end
end

class Installer
  property value: NumberBox

  def install()
    @value = NumberBox.new
    begin
      @value += 1
    rescue
      nil
    end
  end
end`,
			invoke: `Installer.new.install()`,
		},
		{
			name: "rejected or assignment has no ordinary namespace mutation",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    @flag = value
    @flag ||= 1
    unless @flag
      JSON.stringify = replacement
    end
  end
end`,
			invoke:    `begin; Installer.new.install(flag); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "rejected and assignment has no ordinary namespace mutation",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    @flag = value
    @flag &&= 1
    if @flag
      JSON.stringify = replacement
    end
  end
end`,
			invoke:    `begin; Installer.new.install(flag); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "rejected or assignment rescue sees the writing arm",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    @flag = value
    begin
      @flag ||= 1
    rescue
      if @flag
        JSON.stringify = replacement
      end
    end
  end
end`,
			invoke:    `Installer.new.install(flag)`,
			wantCount: 2,
		},
		{
			name: "rejected and assignment rescue sees the writing arm",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    @flag = value
    begin
      @flag &&= 1
    rescue
      unless @flag
        JSON.stringify = replacement
      end
    end
  end
end`,
			invoke:    `Installer.new.install(flag)`,
			wantCount: 2,
		},
		{
			name: "rejected logical assignment rescue keeps rhs local effects",
			definitions: `class Installer
  property flag: bool

  def install(value: bool)
    marker = false
    @flag = value
    begin
      @flag ||= JSON.stringify(begin marker = true; {} end)
    rescue
      if marker
        JSON.stringify = replacement
      end
    end
  end
end`,
			invoke: `Installer.new.install(flag)`,
		},
		{
			name: "failed destructure rescue sees completed local prefix",
			definitions: `class Installer
  property label: string

  def install()
    marker = false
    begin
      marker, @label = [true, 1]
    rescue
      unless marker
        JSON.stringify = replacement
      end
    end
  end
end`,
			invoke:    `Installer.new.install()`,
			wantCount: 2,
		},
		{
			name: "untyped ivar assignment cannot reach rescue mutation",
			definitions: `class Installer
  def install()
    begin
      @value = 1
    rescue
      JSON.stringify = replacement
    end
  end
end`,
			invoke:    `Installer.new.install()`,
			wantCount: 2,
		},
		{
			name: "proven compound ivar assignment cannot reach rescue mutation",
			definitions: `class Installer
  property count: int

  def install()
    @count = 1
    begin
      @count += 1
    rescue
      JSON.stringify = replacement
    end
  end
end`,
			invoke:    `Installer.new.install()`,
			wantCount: 2,
		},
		{
			name: "integer division by zero reaches rescue mutation",
			definitions: `class Installer
  property count: int

  def install()
    @count = 1
    begin
      @count /= 0
    rescue
      JSON.stringify = replacement
    end
  end
end`,
			invoke: `Installer.new.install()`,
		},
		{
			name: "integer modulo by zero reaches rescue mutation",
			definitions: `class Installer
  property count: int

  def install()
    @count = 1
    begin
      @count %= 0
    rescue
      JSON.stringify = replacement
    end
  end
end`,
			invoke: `Installer.new.install()`,
		},
		{
			name: "compatible destructure prefix narrows failed setter rescue",
			definitions: `class Installer
  property flag: bool
  property label: string

  def install()
    begin
      @flag, @label = [true, 1]
    rescue
      unless @flag
        JSON.stringify = replacement
      end
    end
  end
end`,
			invoke:    `Installer.new.install()`,
			wantCount: 2,
		},
		{
			name: "successful body invokes exact callback parameter",
			definitions: `def callback()
  JSON.stringify = replacement
end

def install(callback: function)
  callback.call()
end`,
			invoke: `install(callback)`,
		},
		{
			name: "dynamic callable default yields before later binding failure",
			definitions: `def first(value = yield, later: int:)
end

def second(value = yield, later: int:)
end`,
			invoke: `callback = flag ? first : second
begin
  callback.call(later: "bad") { JSON.stringify = replacement }
rescue
  nil
end`,
		},
		{
			name: "invalid nested call does not invoke forwarded block",
			definitions: `def never(value)
  yield
end

def outer(&block)
  never(&block)
end`,
			invoke: `begin
  outer() { JSON.stringify = replacement }
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "raising nested argument skips forwarded block",
			definitions: `def abort()
  raise "stop"
end

def invoke_block(value)
  yield
end

def outer(&block)
  invoke_block(abort(), &block)
end`,
			invoke: `begin
  outer() { JSON.stringify = replacement }
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "bound default short circuits call block",
			definitions: `def outer(flag = true)
  flag || yield
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "bound default selects nonyielding statement branch",
			definitions: `def outer(flag = true)
  if flag
    nil
  else
    yield
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "exact dynamic callees that ignore blocks stay inert",
			definitions: `def first(&block)
  nil
end

def second(&block)
  nil
end

def outer(flag: bool, &block)
  callback = flag ? first : second
  callback.call(&block)
end`,
			invoke:    `outer(flag) { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "forwarded literal blocks stay inert for exact nonyielding callees",
			definitions: `def first(&block)
  nil
end

def second(&block)
  nil
end

def outer(callback: function, &block)
  callback.call() { yield }
end`,
			invoke: `callback = flag ? first : second
outer(callback) { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "logical callable reassignment updates forwarded block dispatch",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  target &&= second
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "destructured callable reassignment updates forwarded block dispatch",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  target, ignored = [second, nil]
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "branch callable reassignment preserves a yielding alternative",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(flag: bool, &block)
  target = first
  if flag
    target = second
  else
    target = first
  end
  target.call(&block)
end`,
			invoke: `outer(flag) { JSON.stringify = replacement }`,
		},
		{
			name: "unknown branch assignment does not leak into alternate block dispatch",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(flag: bool, &block)
  target = second
  if flag
    target = first
  else
    target.call(&block)
  end
end`,
			invoke: `outer(flag) { JSON.stringify = replacement }`,
		},
		{
			name: "assigned callable shadows same named global function",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  target = yielding
  target(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "branch assigned callable shadows same named global function",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(flag: bool, &block)
  if flag
    target = yielding
  end
  target(&block)
end`,
			invoke: `outer(flag) { JSON.stringify = replacement }`,
		},
		{
			name: "later for iteration sees callable reassignment",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  for value in [1, 2]
    target.call(&block)
    target = second
  end
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "later for iteration sees local shadowing global function",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  for value in [1, 2]
    target(&block)
    target = yielding
  end
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "single for iteration keeps later local assignment unreachable",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  for value in [1]
    target(&block)
    target = yielding
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "breaking for loop keeps later local assignment unreachable",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  for value in [1, 2]
    target(&block)
    target = yielding
    break
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "later while condition sees local shadowing global function",
			definitions: `def target(&block)
  true
end

def yielding(&block)
  yield
  false
end

def outer(&block)
  while target(&block)
    target = yielding
  end
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "breaking while loop skips later local condition dispatch",
			definitions: `def target(&block)
  true
end

def yielding(&block)
  yield
  false
end

def outer(&block)
  while target(&block)
    target = yielding
    break
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "fresh block local does not persist across invocations",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  [1, 2].each() {
    target(&block)
    target = yielding
  }
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "later block iteration sees nested block reassignment",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  [1, 2].each() {
    target.call(&block)
    [nil].each() { target = second }
  }
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "maybe zero run block assignment degrades a captured callable",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = second
  [].each() { target = first }
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "running block assignment keeps a yielding callable possible",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  [nil].each() { target = second }
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "block parameter assignment does not replace captured callable",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = second
  [nil].each() { |target| target = first }
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "class body assignment does not replace enclosing callable",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

class Temporary
end

def outer(&block)
  target = second
  class Temporary
    target = first
  end
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "rescue binding assignment does not replace enclosing callable",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = second
  begin
    raise "stop"
  rescue => target
    target = first
  end
  target.call(&block)
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "rescue sees local callable bound before raise",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  begin
    target = yielding
    raise "stop"
  rescue
    target(&block)
  end
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "rescue ignores local callable assignment after raise",
			definitions: `def target(&block)
  nil
end

def yielding(&block)
  yield
end

def outer(&block)
  begin
    raise "stop"
    target = yielding
  rescue
    target(&block)
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "try body keeps sequential callable fact before later assignment",
			definitions: `def first(&block)
  nil
end

def second(&block)
  yield
end

def outer(&block)
  target = first
  begin
    target.call(&block)
    target = second
  rescue
    nil
  end
end`,
			invoke:    `outer() { JSON.stringify = replacement }`,
			wantCount: 2,
		},
		{
			name: "array elements stop before a later yield",
			definitions: `def abort()
  raise "stop"
end

def outer(&block)
  [abort(), yield]
end`,
			invoke: `begin
  outer() { JSON.stringify = replacement }
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "interpolation stops before a later yield",
			definitions: `def abort()
  raise "stop"
end

def outer(&block)
  "#{abort()}#{yield}"
end`,
			invoke: `begin
  outer() { JSON.stringify = replacement }
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "rescue conditional callable default keeps inner auto calls",
			definitions: `def abort()
  raise "stop"
end

def target(flag: bool, callback: function = ((flag ? abort : abort) rescue (flag ? abort : abort)))
  JSON.stringify = replacement
end`,
			invoke: `begin
  target(flag)
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "rescue array callable default keeps element auto calls",
			definitions: `def abort()
  raise "stop"
end

def target(callbacks: array<function> = ([abort] rescue [abort]))
  JSON.stringify = replacement
end`,
			invoke: `begin
  target()
rescue
  nil
end`,
			wantCount: 2,
		},
		{
			name: "implicit self callable default is stored without invocation",
			definitions: `class Installer
  def abort()
    raise "stop"
  end

  def initialize(callback: function = abort)
    JSON.stringify = replacement
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "pending parameter name falls back to implicit self callable",
			definitions: `class Installer
  def callback()
    raise "stop"
  end

  def initialize(first: function = callback, callback = 1)
    JSON.stringify = replacement
  end
end`,
			invoke: `Installer.new()`,
		},
		{
			name: "bound local callable default is stored without invocation",
			definitions: `def abort()
  raise "stop"
end

def target(source: function = abort, copy: function = source)
  JSON.stringify = replacement
end`,
			invoke: `target()`,
		},
		{
			name: "next value evaluates a forwarded block",
			definitions: `def outer(&block)
  for value in [1]
    next yield
  end
end`,
			invoke: `outer() { JSON.stringify = replacement }`,
		},
		{
			name: "failed early default skips later default and body effects",
			definitions: `def bad()
  "bad"
end

def mutate()
  JSON.stringify = replacement
  1
end

def install(first: int = bad(), second = mutate())
  JSON.stringify = replacement
end`,
			invoke:    `begin; install(); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "failed safe navigation arm does not leak argument effects",
			definitions: `class Installer
  def install(value: int)
  end
end

def mutate()
  JSON.stringify = replacement
  "bad"
end`,
			invoke: `target = flag ? nil : Installer.new
target&.install(mutate())`,
			wantCount: 2,
		},
		{
			name: "empty forwarded name splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.send(*[]); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "invalid forwarded name splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.send(*[123]); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "non-array forwarded name splat blocks initializer effects",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.send(*1); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "static forwarded name splat reaches initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "Installer.send(*[:new])",
		},
		{
			name: "static forwarded name splat preserves constructor receiver",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "Installer.send(*[:new]).install()",
		},
		{
			name: "assigned forwarded constructor name reaches initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "name = :new; Installer.send(name)",
		},
		{
			name: "assigned constructor name excludes unrelated class method effects",
			definitions: `class Installer
  def initialize()
  end

  def self.mutate()
    JSON.stringify = replacement
  end
end`,
			invoke:    "name = :new; Installer.send(name)",
			wantCount: 2,
		},
		{
			name: "assigned forwarded constructor name splat reaches initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "names = [:new]; Installer.public_send(*names)",
		},
		{
			name: "assigned forwarded constructor name splat preserves receiver",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "names = [:new]; Installer.send(*names).install()",
		},
		{
			name: "branch-assigned forwarded names preserve exact alternatives",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `if flag
  names = [:new]
else
  names = [:missing]
end
begin; Installer.send(*names); rescue; nil; end`,
		},
		{
			name: "nested forwarded name alternatives stay correlated",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "names = flag ? [:send, :new] : [:new]; Installer.send(*names)",
		},
		{
			name: "assigned empty forwarded name splat uses following name",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "names = []; Installer.send(*names, :new)",
		},
		{
			name: "shovel-updated forwarded name splat reaches initializer",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: "names = []; names << :new; Installer.send(*names)",
		},
		{
			name: "aliased forwarded name mutation invalidates exact values",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke: `names = [:new]
alias_names = names
alias_names[0] = :missing
begin; Installer.send(*names); rescue; nil; end`,
			wantCount: 2,
		},
		{
			name: "forwarded name splat snapshots before later argument mutation",
			definitions: `class Installer
  def initialize(ignored)
    JSON.stringify = replacement
  end
end`,
			invoke: "names = [:new]; Installer.send(*names, names.clear())",
		},
		{
			name: "assigned empty forwarded name splat blocks dispatch",
			definitions: `class Installer
  def initialize()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; names = []; Installer.send(*names); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "assigned invalid forwarded name blocks unrelated effects",
			definitions: `class Installer
  def self.mutate()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; name = 123; Installer.send(name); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "assigned invalid forwarded name splat blocks unrelated effects",
			definitions: `class Installer
  def self.mutate()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; names = [123]; Installer.public_send(*names); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "dynamic forwarded name splat does not apply unrelated effects",
			definitions: `class Installer
  def self.mutate()
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; Installer.send(*(flag ? [:missing] : [:other])); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "dynamic name splat still reaches explicit forwarding override",
			definitions: `class Installer
  def self.send(name)
    JSON.stringify = replacement
  end
end`,
			invoke: "Installer.send(*(flag ? [:first] : [:second]))",
		},
		{
			name: "static module constructor target effects remain exact",
			definitions: `module Factory
  def self.new()
    JSON.stringify = replacement
    nil
  end
end`,
			invoke: "[Factory][0].send(:new)",
		},
		{
			name: "aliased module constructor mutation keeps partial targets gradual",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end

class Other
  def install()
    nil
  end
end

module Factory
end

def mutate(target)
  target.new = -> { Other.new }
end`,
			invoke:    "mutate(Factory); (flag ? Installer : Factory).new.install()",
			wantCount: 2,
		},
		{
			name: "forwarded instance method",
			definitions: `class Installer
  def install()
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer.new][0].public_send(:install)",
		},
		{
			name: "forwarding override",
			definitions: `class Installer
  def public_send(name)
    JSON.stringify = replacement
  end
end`,
			invoke: "[Installer.new][0].public_send(:ignored)",
		},
		{
			name: "forwarding override accepts non-name argument",
			definitions: `class FirstInstaller
  def public_send(name)
    JSON.stringify = replacement
  end
end

class SecondInstaller
  def public_send(name)
    JSON.stringify = replacement
  end
end`,
			invoke: "(flag ? FirstInstaller.new : SecondInstaller.new).public_send(123)",
		},
		{
			name: "private send override blocks dispatch",
			definitions: `class FirstInstaller
  private def send(name)
    JSON.stringify = replacement
  end
end

class SecondInstaller
  private def send(name)
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; (flag ? FirstInstaller.new : SecondInstaller.new).send(:ignored); rescue; nil; end",
			wantCount: 2,
		},
		{
			name: "protected public send override blocks dispatch",
			definitions: `class FirstInstaller
  protected def public_send(name)
    JSON.stringify = replacement
  end
end

class SecondInstaller
  protected def public_send(name)
    JSON.stringify = replacement
  end
end`,
			invoke:    "begin; (flag ? FirstInstaller.new : SecondInstaller.new).public_send(:ignored); rescue; nil; end",
			wantCount: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
def replacement(value)
  1
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

`+tc.definitions+`

def run(flag: bool)
  `+tc.invoke+`
  takes_int(serialize())
end
`)
			warnings := script.CheckWarningsForFunction("run")
			count := 0
			for _, warning := range warnings {
				if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
					count++
				}
			}
			wantCount := 0
			if tc.wantCount != 0 {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("CheckWarningsForFunction(%q) reported boundary warning %d times, want %d: %#v", "run", count, wantCount, warnings)
			}
		})
	}
}

func TestCheckStaticSplatPreservesAutoInvokedResultIdentity(t *testing.T) {
	t.Parallel()

	t.Run("callable", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
def returned(value: int)
  value
end

def make()
  returned
end

def invoke(callback: function)
  callback.call("bad")
end

def run()
  invoke(*[make])
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if got := strings.Join(checkWarningMessages(warnings), "\n"); !strings.Contains(got, "call to returned.call argument value expected int, got string") {
			t.Fatalf("CheckWarningsForFunction(%q) = %q, want returned callback mismatch", "run", got)
		}
	})

	t.Run("class", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
class User
  def self.pick(value: int)
    value
  end
end

def make()
  User
end

def invoke(klass)
  klass.pick("bad")
end

def run()
  invoke(*[make])
end
`)
		warnings := script.CheckWarningsForFunction("run")
		if got := strings.Join(checkWarningMessages(warnings), "\n"); !strings.Contains(got, "call to User.pick argument value expected int, got string") {
			t.Fatalf("CheckWarningsForFunction(%q) = %q, want returned class mismatch", "run", got)
		}
	})
}

func TestCheckDirectPlainModuleConstructorSkipsArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		invoke    string
		wantCount int
	}{
		{
			name:   "direct",
			invoke: "Factory.new(*[takes_int(\"bad\")])",
		},
		{
			name:      "send",
			invoke:    "Factory.send(:new, *[takes_int(\"bad\")])",
			wantCount: 1,
		},
		{
			name:      "public send",
			invoke:    "Factory.public_send(:new, *[takes_int(\"bad\")])",
			wantCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
module Factory
end

def takes_int(value: int)
  value
end

def run()
  begin
    `+tc.invoke+`
  rescue
    nil
  end
end
`)
			warnings := script.CheckWarningsForFunction("run")
			count := 0
			for _, warning := range warnings {
				if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
					count++
				}
			}
			if count != tc.wantCount {
				t.Fatalf("CheckWarningsForFunction(%q) reported argument warning %d times, want %d: %#v", "run", count, tc.wantCount, warnings)
			}
		})
	}
}

func TestClassConstantEffectProofUsesCalleeSelf(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def replacement(value)
  1
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def mutate(target)
  target.new = -> { Other.new }
end

class Installer
  def install()
    JSON.stringify = replacement
  end
end

class Other
  def install()
    nil
  end
end

module Factory
end

class C
  def helper()
    mutate(Factory)
  end

  def wrapper()
    helper()
  end
end

class D
  def helper()
    1
  end

  def run(flag: bool)
    C.new.wrapper()
    (flag ? Installer : Factory).new.install()
    takes_int(serialize())
  end
end

def run(flag: bool)
  D.new.run(flag)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("CheckWarningsForFunction(%q) reported boundary warning %d times, want 1: %#v", "run", count, warnings)
	}
}

func TestCheckWarningsForFunctionAppliesExactCallableNamespaceMutations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def replacement(value)
  1
end

def install()
  JSON.stringify = replacement
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def invoke(callback: function)
  takes_int(serialize())
  callback.call()
  takes_int(serialize())
end

def run()
  invoke(install)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("CheckWarningsForFunction(%q) reported boundary warning %d times, want 1: %#v", "run", count, warnings)
	}
}

func TestCheckWarningsForFunctionUnionsExactDynamicCallNamespaceMutations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def replacement(value)
  1
end

class JSONInstaller
  def install()
    JSON.stringify = replacement
  end
end

class MathInstaller
  def install()
    Math.sqrt = replacement
  end
end

def takes_int(value: int)
  value
end

def run(flag: bool)
  (flag ? JSONInstaller.new : MathInstaller.new).install()
  takes_int(JSON.stringify({}))
  takes_int(Math.sqrt(4))
end
`)
	warnings := script.CheckWarningsForFunction("run")
	wants := []string{
		"call to takes_int argument value expected int, got string",
		"call to takes_int argument value expected int, got float",
	}
	for _, want := range wants {
		count := 0
		for _, warning := range warnings {
			if strings.Contains(warning.Message, want) {
				count++
			}
		}
		if count != 0 {
			t.Fatalf("CheckWarningsForFunction(%q) reported %q %d times, want 0: %#v", "run", want, count, warnings)
		}
	}
}

func TestCheckWarningsForFunctionAllowsProtectedDynamicOverrideFromSameClass(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class FirstInstaller
  protected def public_send(name)
    JSON.stringify = replacement
  end

  def exercise(first: FirstInstaller, other: SecondInstaller, flag: bool)
    takes_int(JSON.stringify({}))
    (flag ? first : other).public_send(:ignored)
    takes_int(JSON.stringify({}))
  end
end

class SecondInstaller
  protected def public_send(name)
    nil
  end
end

def run(flag: bool)
  FirstInstaller.new.exercise(FirstInstaller.new, SecondInstaller.new, flag)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("CheckWarningsForFunction(%q) reported boundary warning %d times, want 1: %#v", "run", count, warnings)
	}
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

func TestCheckWarningsSeedEntrypointExportsAfterNilPredicateGuard(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		guard string
	}{
		{name: "bare member", guard: "value.nil?"},
		{name: "parenthesized call", guard: "value.nil?()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := moduleTestEngine(t)
			script, err := engine.CompileSnippet(`
def maybe_value() -> int?
  nil
end

value = maybe_value()
return unless `+tc.guard+`
value || require("enum_status")

def normalize(status: Status) -> Status
  status
end
`, "<script>")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckWarningsSeedEntrypointExportsFromNarrowedConditionalExpressions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		guard       string
		conditional string
	}{
		{
			name:        "ternary nil bare member",
			guard:       "return unless value.nil?",
			conditional: "value.nil? ? require(\"enum_status\") : nil",
		},
		{
			name:        "ternary nil parenthesized call",
			guard:       "return unless value.nil?()",
			conditional: "value.nil?() ? require(\"enum_status\") : nil",
		},
		{
			name:        "ternary non-nil bare member",
			guard:       "return if value.nil?",
			conditional: "value.nil? ? nil : require(\"enum_status\")",
		},
		{
			name:        "ternary non-nil parenthesized call",
			guard:       "return if value.nil?()",
			conditional: "value.nil?() ? nil : require(\"enum_status\")",
		},
		{
			name:        "if expression nil bare member",
			guard:       "return unless value.nil?",
			conditional: "if value.nil? then require(\"enum_status\") else nil end",
		},
		{
			name:        "if expression nil parenthesized call",
			guard:       "return unless value.nil?()",
			conditional: "if value.nil?() then require(\"enum_status\") else nil end",
		},
		{
			name:        "if expression non-nil bare member",
			guard:       "return if value.nil?",
			conditional: "if value.nil? then nil else require(\"enum_status\") end",
		},
		{
			name:        "if expression non-nil parenthesized call",
			guard:       "return if value.nil?()",
			conditional: "if value.nil?() then nil else require(\"enum_status\") end",
		},
		{
			name:  "if expression elsif",
			guard: "return unless value.nil?",
			conditional: `if !value.nil?
  nil
elsif value.nil?
  require("enum_status")
else
  nil
end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := moduleTestEngine(t)
			script, err := engine.CompileSnippet(`
def maybe_value() -> int?
  nil
end

value = maybe_value()
`+tc.guard+`
`+tc.conditional+`

def normalize(status: Status) -> Status
  status
end
`, "<script>")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckWarningsRewidenConditionalExpressionFactsDuringExportCollection(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{
		"!value.nil? ? nil : nil",
		"if !value.nil? then nil else nil end",
	} {
		t.Run(expression, func(t *testing.T) {
			engine := moduleTestEngine(t)
			script, err := engine.CompileSnippet(`
def maybe_value() -> int?
  nil
end

value = maybe_value()
`+expression+`
value || require("enum_status")

def normalize(status: Status) -> Status
  status
end
`, "<script>")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			requireCheckWarningContains(t, script, "unknown type Status")
		})
	}
}

func TestCheckWarningsDoNotLeakConditionalExportFactsToFollowingArguments(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{
		"!value.nil? ? 1 : 2",
		"if !value.nil? then 1 else 2 end",
	} {
		t.Run(expression, func(t *testing.T) {
			engine := MustNewEngine(Config{})
			script, err := engine.CompileSnippet(`
def maybe_value() -> int?
  nil
end

def pair(left, right)
  [left, right]
end

value = maybe_value()
pair(`+expression+`, -value)
`, "<script>")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckWarningsKeepForcedNilPredicateShortCircuitEffects(t *testing.T) {
	t.Parallel()

	for _, predicate := range []string{"value.nil?", "value.nil?()"} {
		t.Run(predicate, func(t *testing.T) {
			engine := moduleTestEngine(t)
			script, err := engine.CompileSnippet(`
def maybe_value() -> int?
  nil
end

value = maybe_value()
return unless value.nil?
`+predicate+` && require("enum_status")
normalize("bad")
`, "<script>")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			requireCheckWarningContains(t, script, "call to normalize argument status expected Status, got string")
		})
	}
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

func TestCheckPlainAssignmentAppliesRequireEffectsInEvaluationOrder(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "right side before target",
			source: `
def run()
  values = [0]
  values[normalize(:bogus)] = require("enum_status")
end
`,
		},
		{
			name: "destructured targets left to right",
			source: `
def run()
  values = [0]
  require("enum_status").foo, values[normalize(:bogus)] = [1, 2]
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithEngine(t, engine, tc.source)
			requireCheckWarningContains(t, script, "call to normalize argument status expected Status, got symbol")
		})
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

func TestCheckCallArgumentRequireEffectsStopAfterNonCompletingArgument(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script, _, _, err := CompileSnippetWithProgram(engine, `def pair(first, second)
  first
end

def abort
  raise "boom"
end

pair(abort, require("enum_status")) rescue nil
normalize(:bogus)`, "main")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	for _, warning := range script.CheckWarnings() {
		if strings.Contains(warning.Message, "call to normalize argument status expected Status") {
			t.Fatalf("CheckWarnings() = %#v, unreachable require exposed normalize contract", script.CheckWarnings())
		}
	}
}

func TestCheckCompletionFollowsExactDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		source      string
		wantMissing bool
		wantWarning string
	}{
		{
			name: "safe call lookup failure keeps nil path",
			source: `class Box
  private

  def secret()
    1
  end
end

def consume(first, second)
end

def run(value: Box | nil)
  consume(value&.secret(), missing_name)
end`,
			wantMissing: true,
		},
		{
			name: "safe member lookup failure keeps nil path",
			source: `class Box
  private

  def secret()
    1
  end
end

def consume(first, second)
end

def run(value: Box | nil)
  consume(value&.secret, missing_name)
end`,
			wantMissing: true,
		},
		{
			name: "exact dynamic methods all raise",
			source: `class Alpha
  def boom()
    raise "boom"
  end
end

class Beta
  def boom()
    raise "boom"
  end
end

def consume(first, second)
end

def run(value: Alpha | Beta)
  consume(value.boom(), missing_name)
end`,
		},
		{
			name: "annotated return rejects supplied fact",
			source: `def identity(value) -> int
  value
end

def consume(first, second)
end

def run()
  consume(identity("bad"), missing_name)
end`,
			wantWarning: "return value expected int, got string",
		},
		{
			name: "dependent default rejection stops later statements",
			source: `def target(seed: string = "x", value: int = seed)
  1
end

def run()
  target()
  missing_name
end`,
			wantWarning: "default value for value expected int, got string",
		},
		{
			name: "undefined later parameter in default stops later statements",
			source: `def target(first = consume(later), later = 1)
  1
end

def consume(value)
  value
end

def run()
  target()
  missing_name
end`,
		},
		{
			name: "raising callable default remains a value",
			source: `def abort()
  raise "stop"
end

def target(callback: function = abort)
  1
end

def run()
  target()
  missing_name
end`,
			wantMissing: true,
		},
		{
			name: "invalid case splat stops every result arm",
			source: `def run()
  case 1
  when *1
    missing_name
  else
    missing_name
  end
end`,
		},
		{
			name: "raising setter stops later statements",
			source: `class Box
  def value=(value)
    raise "stop"
  end
end

def run()
  box = Box.new
  box.value = 1
			missing_name
end`,
		},
		{
			name: "raising compound setter stops later statements",
			source: `class Box
  def value()
    1
  end

  def value=(value)
    raise "stop"
  end
end

def run()
  box = Box.new
  box.value += 1
  missing_name
end`,
		},
		{
			name: "raising logical setter stops later statements",
			source: `class Box
  def value()
    nil
  end

  def value=(value)
    raise "stop"
  end
end

def run()
  box = Box.new
  box.value ||= 1
  missing_name
end`,
		},
		{
			name: "raising compound operator stops before assignment",
			source: `class Box
  def +(value)
    raise "stop"
  end
end

def run()
  box = Box.new
  box += 1
  missing_name
end`,
		},
		{
			name: "raising index reader stops later statements",
			source: `class Box
  def [](index)
    raise "stop"
  end
end

def run()
  Box.new[0]
  missing_name
end`,
		},
		{
			name: "invalid unary operation stops later statements",
			source: `def run()
  -"bad"
  missing_name
end`,
			wantWarning: "unsupported unary - operand string",
		},
		{
			name: "blockless yield stops its caller",
			source: `def consume()
  yield
end

def run()
  consume()
  missing_name
end`,
		},
		{
			name: "retry stops the current rescue body",
			source: `def run()
  begin
    raise "boom"
  rescue
    retry
    missing_name
  end
end`,
		},
		{
			name: "read only member rejects assignment",
			source: `class Box
  def value()
    1
  end
end

def run()
  box = Box.new
  box.value = 2
  missing_name
end`,
		},
		{
			name: "private setter rejects assignment",
			source: `class Box
  private

  def value=(value)
    value
  end
end

def run()
  box = Box.new
  box.value = 2
  missing_name
end`,
		},
		{
			name: "destructure rest passes an array to setter",
			source: `class Box
  def value=(value: array<int>)
    value
  end
end

def run()
  box = Box.new
  first, *box.value, last = [1, 2, 3]
  missing_name
end`,
			wantMissing: true,
		},
		{
			name: "setter function expectation stores bare callable",
			source: `def callback()
  raise "stop"
end

class Box
  property cb: function
end

def run()
  box = Box.new
  box.cb = callback
  missing_name
end`,
			wantMissing: true,
		},
		{
			name: "literal bool argument selects rejecting return arm",
			source: `def choose(flag: bool) -> int
  if flag then "bad" else 1 end
end

def run()
  choose(true)
  missing_name
end`,
			wantWarning: "return value expected int, got string",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			warnings := script.CheckWarningsForFunction("run")
			gotMissing := false
			gotWarning := tc.wantWarning == ""
			for _, warning := range warnings {
				gotMissing = gotMissing || strings.Contains(warning.Message, "missing_name")
				gotWarning = gotWarning || strings.Contains(warning.Message, tc.wantWarning)
			}
			if gotMissing != tc.wantMissing {
				t.Fatalf("CheckWarningsForFunction(%q) missing_name warning = %t, want %t: %#v", "run", gotMissing, tc.wantMissing, warnings)
			}
			if !gotWarning {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want warning containing %q", "run", warnings, tc.wantWarning)
			}
		})
	}
}

func TestCheckNamespaceMutationEffectsRespectEvaluationOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		setup  string
		invoke string
	}{
		{
			name: "later argument after raising helper",
			setup: `def consume(first, second)
end

def abort()
  raise "boom"
end

def mutate()
  JSON.stringify = replacement
end

def wrapper()
  consume(abort(), mutate())
end`,
			invoke: "wrapper()",
		},
		{
			name: "assignment after raising right side",
			setup: `def abort()
  raise "boom"
end

def wrapper()
  JSON.stringify = abort()
end`,
			invoke: "wrapper()",
		},
		{
			name: "unused lambda argument",
			setup: `def ignore(callback: function)
  nil
end

def wrapper()
  ignore(-> { JSON.stringify = replacement })
end`,
			invoke: "wrapper()",
		},
		{
			name: "assigned empty positional splat",
			setup: `def install(required)
  JSON.stringify = replacement
end

def wrapper()
  arguments = []
  install(*arguments)
end`,
			invoke: "wrapper()",
		},
		{
			name: "assigned empty keyword splat",
			setup: `def install(required:)
  JSON.stringify = replacement
end

def wrapper()
  keywords = {}
  install(**keywords)
end`,
			invoke: "wrapper()",
		},
		{
			name: "class predicate failure skips later argument",
			setup: `class Token
end

def consume(first, second)
end

def install()
  JSON.stringify = replacement
end

def wrapper()
  consume(Token.is_a?(2), install())
end`,
			invoke: "wrapper()",
		},
		{
			name: "invalid splat in default stops later default",
			setup: `def helper(required)
end

def install(first = helper(*1), second = begin
  JSON.stringify = replacement
end)
end

def wrapper()
  install()
end`,
			invoke: "wrapper()",
		},
		{
			name: "try else after raising body",
			setup: `def abort()
  raise "boom"
end

def wrapper()
  begin
    abort()
  rescue
    nil
  else
    JSON.stringify = replacement
  end
end`,
			invoke: "wrapper()",
		},
		{
			name: "required callback call rejects before body",
			setup: `def callback(required)
  JSON.stringify = replacement
end

def invoke(callback: function)
  callback.call()
end

def wrapper()
  invoke(callback)
end`,
			invoke: "wrapper()",
		},
		{
			name: "aliased invalid keyword splat",
			setup: `class Installer
  def initialize(**options)
    JSON.stringify = replacement
  end
end

def wrapper()
  bad = {}
  copy = bad
  bad[2] = "x"
  Installer.new(**copy)
end`,
			invoke: "wrapper()",
		},
		{
			name: "unrelated delete preserves invalid keyword splat",
			setup: `class Installer
  def initialize(**options)
    JSON.stringify = replacement
  end
end

def wrapper()
  bad = {}
  bad[2] = "x"
  bad.delete(:other)
  Installer.new(**bad)
end`,
			invoke: "wrapper()",
		},
		{
			name: "block parameter shadows outer callable",
			setup: `def callback()
  JSON.stringify = replacement
end

def wrapper()
  [1].each { |callback| callback }
end`,
			invoke: "wrapper()",
		},
		{
			name: "ordinary parameter shadows outer callable",
			setup: `def callback()
  JSON.stringify = replacement
end

def ignore(callback)
  callback
end

def wrapper()
  ignore(1)
end`,
			invoke: "wrapper()",
		},
		{
			name: "raising prefix prevents yield",
			setup: `def abort()
  raise "boom"
end

def consume()
  abort()
  yield
end

def wrapper()
  consume() { JSON.stringify = replacement }
end`,
			invoke: "wrapper()",
		},
		{
			name: "plain module constructor stops later default",
			setup: `module Plain
end

def install(first = Plain.new, second = begin
  JSON.stringify = replacement
end)
end

def wrapper()
  install()
end`,
			invoke: "wrapper()",
		},
		{
			name: "raising compound assignment skips setter effect",
			setup: `def abort()
  raise "boom"
end

def wrapper()
  JSON.stringify += abort()
end`,
			invoke: "wrapper()",
		},
		{
			name: "class setter rejects value before body effect",
			setup: `class Config
  def self.value=(value: int)
    JSON.stringify = replacement
  end
end

def wrapper()
  Config.value = "bad"
end`,
			invoke: "wrapper()",
		},
		{
			name: "exact parameter branch prevents yield",
			setup: `def consume(flag: bool)
  if flag
    raise "stop"
  else
    nil
  end
  yield
end

def wrapper()
  consume(true) { JSON.stringify = replacement }
end`,
			invoke: "wrapper()",
		},
		{
			name: "empty matching rescue propagates the exception",
			setup: `def wrapper()
  begin
    raise TypeError, "boom"
  rescue TypeError
  rescue
    nil
  end
  JSON.stringify = replacement
end`,
			invoke: "wrapper()",
		},
		{
			name: "block call parameter shadows outer callable",
			setup: `def mutate()
  JSON.stringify = replacement
end

def invoke(callback: function)
  [->() { 1 }].each { |callback| callback.call() }
end

def wrapper()
  invoke(mutate)
end`,
			invoke: "wrapper()",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

`+tc.setup+`

def run()
  begin
    `+tc.invoke+`
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`)
			requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
		})
	}
}

func TestCheckEntrypointNamespaceMutationScanRespectsExactCallFacts(t *testing.T) {
	t.Parallel()

	script, err := MustNewEngine(Config{}).CompileSnippet(`def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def consume(flag: bool)
  if flag
    raise "stop"
  else
    nil
  end
  yield
end

def run()
  begin
    consume(true) { JSON.stringify = replacement }
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end

run()`, "main")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckReachableParamsPreserveForwardedStaticNames(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Installer
  def self.install(value: int)
    value
  end
end

def dispatch(name)
  Installer.send(name, "bad")
end

def run()
  dispatch(:install)
end`)

	warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	if !strings.Contains(warnings, "call to Installer.install argument value expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want forwarded argument mismatch", "run", warnings)
	}
}

func TestCheckStaticArrayWritesUpdateAliasedForwardedNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "conditional alias",
			body: `names = [:first]
  other = [:second]
  copy = flag ? names : other
  names[0] = :third
  Receiver.new.send(*copy, "bad")`,
		},
		{
			name: "equal valued conditional alias",
			body: `names = [:first]
  other = [:first]
  copy = flag ? other : names
  names[0] = :third
  Receiver.new.send(*copy, "bad")`,
		},
		{
			name: "nested array write",
			body: `names = [[:first]]
  names[0][0] = :third
  Receiver.new.send(*names[0], "bad")`,
		},
		{
			name: "projected alias",
			body: `outer = [[:first]]
  inner = outer[0]
  inner[0] = :third
  Receiver.new.send(*outer[0], "bad")`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def second(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def run(flag: bool)
  `+tc.body+`
end`)

			warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
			if !strings.Contains(warnings, "call to takes_int argument value expected int, got string") {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want forwarded argument mismatch", "run", warnings)
			}
		})
	}
}

func TestCheckStaticIfExpressionSelectsExactClassValue(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

class Good
  def run(value)
    value
  end
end

class Bad
  def run(value)
    takes_int(value)
  end
end

def run()
  target = if true then Good else Bad end
  target.new.run("bad")
end`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want only the selected class target", "run", warnings)
	}
}

func TestCheckForwardedConstructorFactsKeepSuccessfulArms(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Installer
  def install()
    JSON.stringify = replacement
  end
end

def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run(flag: bool)
  names = flag ? [:new] : [:missing]
  begin
    Installer.send(*names).install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`)

	for _, warning := range script.CheckWarningsForFunction("run") {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			t.Fatalf("CheckWarningsForFunction(%q) = %#v, successful constructor arm lost", "run", script.CheckWarningsForFunction("run"))
		}
	}
}

func TestCheckSkippedDefaultsDoNotPoisonBodyFacts(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

def inspect(value, skipped = value.delete(:name))
  takes_int(value[:name])
end

def run()
  inspect({name: "bad"}, 0)
end`)

	warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	if !strings.Contains(warnings, "call to takes_int argument value expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want preserved hash value mismatch", "run", warnings)
	}
}

func TestCheckSafeNavigationResultKeepsCalleeNilArm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "resolved target",
			source: `class Box
  def value(effect) -> string
    "ok"
  end
end

def takes_int(value: int)
  value
end

def run(flag: bool)
  box = flag ? nil : Box.new
  takes_int(box&.value(-> { box = Box.new }.call))
end`,
		},
		{
			name: "exact dynamic targets",
			source: `class FirstBox
  def value(effect) -> string
    "first"
  end
end

class SecondBox
  def value(effect) -> string
    "second"
  end
end

def takes_int(value: int)
  value
end

def run(flag: bool, other: bool)
  box = flag ? nil : (other ? FirstBox.new : SecondBox.new)
  takes_int(box&.value(-> { box = FirstBox.new }.call))
end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
			if !strings.Contains(warnings, "call to takes_int argument value expected int, got string | nil") {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want captured nullable safe-navigation fact", "run", warnings)
			}
		})
	}
}

func TestCheckSafeNavigationBareMemberPinsNilAfterFailedDispatch(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Box
  private

  def secret() -> string
    "x"
  end
end

def takes_string(value: string)
  value
end

def run(value: Box | nil)
  result = value&.secret
  takes_string(result)
end`)

	warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	if !strings.Contains(warnings, "call to takes_string argument value expected string, got nil") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want failed safe-navigation nil result", "run", warnings)
	}
}

func TestCheckNamespaceMutationScanUsesReachableReceiverFacts(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Installer
  def install()
    JSON.stringify = replacement
  end
end

def replacement(value)
  1
end

def invoke(target)
  target.install()
end

def takes_int(value: int)
  value
end

def run()
  invoke(Installer.new)
  takes_int(JSON.stringify({}))
end`)

	for _, warning := range script.CheckWarningsForFunction("run") {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			t.Fatalf("CheckWarningsForFunction(%q) = %#v, reachable receiver mutation was lost", "run", script.CheckWarningsForFunction("run"))
		}
	}

	implicitCallable := compileScript(t, `def replacement(value)
  1
end

def invoke(callback: function)
  callback.call(1)
end

class Runner
  def mutate(value)
    JSON.stringify = replacement
  end

  def pass()
    invoke(mutate)
  end
end

def takes_int(value: int)
  value
end

def run()
  Runner.new.pass()
  takes_int(JSON.stringify({}))
end`)

	for _, warning := range implicitCallable.CheckWarningsForFunction("run") {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			t.Fatalf("CheckWarningsForFunction(%q) = %#v, implicit-self callable mutation was lost", "run", implicitCallable.CheckWarningsForFunction("run"))
		}
	}
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
			name: "implicit logical assignment returns assigned target",
			source: `def run() -> int
  value = "bad"
  value ||= 1
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

func TestCheckWarningsSkipUnreachableImplicitTryBodyLeaf(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def fail() -> int
  raise "stop"
end

def recover() -> string
  begin
    fail()
  rescue
    "ok"
  end
end`)

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsFollowRetryIntoCompletingBody(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

def run()
  flag = true
  begin
    if flag
      raise "again"
    end
  rescue
    flag = false
    retry
  end
  takes_int("bad")
end`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
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
		1 + 0
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
			name: "nested ensure return masks inner body return type",
			source: `def run() -> int
  begin
    begin
      return "bad"
    ensure
      return 1
    end
  ensure
    cleanup = true
  end
end`,
		},
		{
			name: "inferred exiting ensure masks body return type",
			source: `def run() -> string
  begin
    return 1
  ensure
    value = nil
    if value == nil
      return "forced"
    end
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

func TestCheckWarningsSkipUnreachableRescueClauses(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

def run()
  begin
    raise TypeError, "boom"
  rescue TypeError
    1
  rescue
    takes_int("bad")
  end
end`)

	requireNoCheckWarnings(t, script)
}

func TestCheckWarningsKeepRaiseOperandFailureRescuesReachable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def fail()
  raise AssertionError, "inner"
end

def takes_int(value: int)
  value
end

def run()
  begin
    raise TypeError, fail()
  rescue AssertionError
    takes_int("bad")
  rescue TypeError
    nil
  end
end`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckWarningsSelectTypeErrorRescueForInvalidRaiseMessage(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

def run()
  begin
    raise AssertionError, 1
  rescue AssertionError
    nil
  rescue TypeError
    takes_int("bad")
  end
end`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckWarningsInferOnlyReachableRescueAndCaseExpressions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "nonraising rescue expression",
			source: `def recover()
  1 rescue "ok"
end

def takes_string(value: string)
  value
end

def run()
  takes_string(recover())
end`,
			want: "call to takes_string argument value expected string, got int",
		},
		{
			name: "static case selection under expectation",
			source: `def takes_string(value: string)
  value
end

def run()
  takes_string(case 1 when 1 then 2 else "ok" end)
end`,
			want: "call to takes_string argument value expected string, got int",
		},
		{
			name: "exact local case selection under expectation",
			source: `def takes_string(value: string)
  value
end

def run()
  target = 1
  takes_string(case target when 1 then 2 else "ok" end)
end`,
			want: "call to takes_string argument value expected string, got int",
		},
		{
			name: "rescue fallback sees effects before definite failure",
			source: `def crash(value)
  raise "stop"
end

def takes_int(value: int)
  value
end

def run()
  values = []
  crash(values << "bad") rescue takes_int(values[0])
end`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "rescue fallback sees effects from failing conditional arm",
			source: `def crash(value)
  raise "stop"
end

def takes_int(value: int)
  value
end

def run(flag)
  values = []
  (flag ? crash(values << "bad") : 1) rescue takes_int(values[0])
end`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "rescue fallback sees callee parameter effects from failing arm",
			source: `def crash(value)
  raise "stop"
end

def takes_int(value: int)
  value
end

def maybe(flag: bool, values)
  flag ? crash(values << "bad") : 1
end

def run(flag: bool)
  values = []
  maybe(flag, values) rescue takes_int(values[0])
end`,
			want: "call to takes_int argument value expected int, got string",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScript(t, tc.source), tc.want)
		})
	}
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

func TestCheckWarningsInferNilForUnreachedRescueLocals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_string(value: string)
  value
end

def run()
  begin
    1
  rescue
    skipped = 1
  end
  takes_string(skipped)
end`)

	warnings := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	if !strings.Contains(warnings, "call to takes_string argument value expected string, got nil") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want unreached rescue local nil fact", "run", warnings)
	}
}

func TestCheckWarningsDoNotFollowLimitErrorRescueModifierFallback(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `def stop()
  raise LimitError, "stop"
end

def takes_string(value: string)
  value
end

def run()
  takes_string(stop() rescue 1)
end`))
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

func TestCheckWarningsForFunctionDoesNotLeakBlockClassValueFacts(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
class Holder
  def initialize()
  end

  def check(value: int)
    value
  end
end

def run()
  klass = nil
  [].each do
    klass = Holder
  end
  klass.new.check("bad")
end
`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForFunction() = %#v, want none", warnings)
	}
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

func TestCheckInferenceDoesNotBindConditionalRequireExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run(flag)
  value = flag ? require("helper").double : nil
  double(1, 2)
  value
end
`)

	requireNoCheckWarnings(t, script)
}
