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

func TestCheckWarningsDoNotHoistSafeNavigationRequiredModuleEnumExports(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `
def run() -> Status
  nil&.load(require("enum_status")) do
    require("enum_status")
  end
  :draft
end
`)

	requireCheckWarningContains(t, script, "unknown type Status")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Status")
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
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
