package runtime

import (
	"strings"
	"testing"
)

func TestCheckFunctionFailureSummariesCaptureExplicitRaises(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "exact indexed write reaches the caller rescue",
			source: `
def mutate(values)
  values[0] = "ok"
  raise "stop"
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "unmatched typed rescue propagates the failure facts",
			source: `
def mutate(values)
  values[0] = "ok"
  begin
    raise RuntimeError, "stop"
  rescue TypeError
    nil
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "empty matching rescue propagates the failure facts",
			source: `
def mutate(values)
  values[0] = "ok"
  begin
    raise RuntimeError, "stop"
  rescue RuntimeError
  rescue
    nil
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "raise from a rescue body propagates its failure facts",
			source: `
def mutate(values)
  begin
    raise "first"
  rescue
    values[0] = "ok"
    raise "second"
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "ensure transforms the propagated failure state",
			source: `
def mutate(values)
  begin
    values[0] = "bad"
    raise "stop"
  ensure
    values[0] = 1
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
		},
		{
			name: "nested ensures preserve the propagated failure state",
			source: `
def mutate(values)
  begin
    begin
      begin
        values[0] = "ok"
        raise "first"
      ensure
        nil
      end
    ensure
      nil
    end
  rescue
    raise "second"
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
			want: "call to takes_int argument value expected int, got string",
		},
		{
			name: "rescue consumes a noncompleting expression failure",
			source: `
def crash()
  raise "stop"
end

def mutate(values)
  begin
    values[0] = "bad"
    crash()
  rescue
    values[0] = 1
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
		},
		{
			name: "ensure transforms a noncompleting expression failure",
			source: `
def crash()
  raise "stop"
end

def mutate(values)
  begin
    values[0] = "bad"
    crash()
  ensure
    values[0] = 1
  end
end

def takes_string(value: string)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_string(values[0])
end
`,
			want: "call to takes_string argument value expected string, got int",
		},
		{
			name: "returning ensure swallows a noncompleting expression failure",
			source: `
def crash()
  raise "stop"
end

def mutate(values)
  begin
    values[0] = "bad"
    crash()
  ensure
    return nil
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
		},
		{
			name: "raising ensure replaces the pending failure state",
			source: `
def mutate(values)
  begin
    values[0] = "bad"
    raise "first"
  ensure
    values[0] = 1
    raise "second"
  end
end

def takes_string(value: string)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_string(values[0])
end
`,
			want: "call to takes_string argument value expected string, got int",
		},
		{
			name: "returning ensure swallows the pending failure",
			source: `
def mutate(values)
  begin
    values[0] = "bad"
    raise "stop"
  ensure
    return nil
  end
end

def takes_int(value: int)
  value
end

def run()
  values = [1]
  mutate(values) rescue takes_int(values[0])
end
`,
		},
		{
			name: "explicit raise retains static poison from a possible mutation",
			source: `
def mutate(flag: bool, values: array<int | string>)
  if flag
    values.push("changed")
  end
  raise "stop"
end

def takes_string(value: string)
  value
end

def run(flag: bool)
  values = [1]
  mutate(flag, values) rescue takes_string(values[0])
end
`,
			want: "call to takes_string argument value expected string, got int | string | nil",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := compileScriptDefault(t, tc.source).CheckWarningsForFunction("run")
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
			if len(warnings) != 1 {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want exactly one warning", "run", warnings)
			}
		})
	}
}

func TestCheckFunctionReturnSummariesDoNotEnqueueUnreachableCalls(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def helper() -> int
  "bad"
end

def wrapper()
  helper()
end

def takes_bool(value: bool)
  value
end

def run()
  takes_bool(true || wrapper())
end
`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
		t.Errorf("CheckWarningsForFunction(%q) = %#v, want none for short-circuited wrapper", "run", warnings)
	}
}

func TestCheckFunctionReturnSummariesDoNotCapturePreRequireCallState(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, moduleTestEngine(t), `
def helper() -> Status
  :draft
end

def wrapper()
  helper()
end

def takes_bool(value: bool)
  value
end

def run()
  takes_bool(true || wrapper())
  require("enum_status")
  helper()
end
`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
		t.Errorf("CheckWarningsForFunction(%q) = %#v, want none after require", "run", warnings)
	}
}

func TestCheckFunctionReturnSummariesUseEntrypointImports(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count() -> int
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script, err := engine.CompileSnippet(`
require("counts")

def wrapper()
  build_count()
end

def takes_string(value: string)
  value
end

takes_string(wrapper())
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

// TestCheckFunctionReturnSummariesCollectDefaultRequires pins that a require
// inside a parameter default binds its exports for the body walk, so the
// summary sees the module function the body calls.
func TestCheckFunctionReturnSummariesCollectDefaultRequires(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count() -> int
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script, err := engine.CompileSnippet(`
def wrapper(_ = require("counts"))
  build_count()
end

def takes_string(value: string)
  value
end

takes_string(wrapper())
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

// TestCheckFunctionReturnSummariesSkipForeignFunctions pins the issue scope:
// required-module functions keep unknown results even when their bodies are
// summarizable.
func TestCheckFunctionReturnSummariesSkipForeignFunctions(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count()
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
def takes_string(value: string)
  value
end

def run()
  require("counts")
  takes_string(build_count())
end
`)
	requireNoCheckWarnings(t, script)
}
