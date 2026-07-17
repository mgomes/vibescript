package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		script      string
		wantOut     []string
		wantErr     string
		expectNoErr bool
	}{
		{
			name: "no_issues",
			script: `def run()
  value = 1
  value
end`,
			wantOut:     []string{"No issues found"},
			expectNoErr: true,
		},
		{
			name: "inferred_local_contradiction",
			script: `def takes_int(value: int)
  value
end

value = "1"
takes_int(value)`,
			wantOut: []string{"call to takes_int argument value expected int, got string"},
			wantErr: "check failed with 1 issue(s)",
		},
		{
			name: "checks_uncalled_functions",
			script: `def helper -> int
  "text"
end`,
			wantOut: []string{"return value expected int, got string", "(helper)"},
			wantErr: "check failed with 1 issue(s)",
		},
		{
			name: "reassignment_conflict",
			script: `name = "Mauricio"
count = 1
count = name`,
			wantOut: []string{"reassignment of count expected int, got string"},
			wantErr: "check failed with 1 issue(s)",
		},
		{
			name: "parse_as_shape_flows_downstream",
			script: `def create_user(name: string)
  name
end

body = JSON.parse_as("{}", { name: string, age: int })
create_user(body["age"])`,
			wantOut: []string{"call to create_user argument name expected string, got int"},
			wantErr: "check failed with 1 issue(s)",
		},
		{
			name: "unknown_values_pass",
			script: `def create_user(name: string)
  name
end

body = JSON.parse("{}")
create_user(body["name"])`,
			wantOut:     []string{"No issues found"},
			expectNoErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scriptPath := writeVibeScript(t, tc.script)
			out, err := dispatchCommand(t, "check", []string{scriptPath})
			if tc.expectNoErr {
				if err != nil {
					t.Fatalf("checkCommand(%q) err = %v, want nil", scriptPath, err)
				}
			} else {
				if err == nil {
					t.Fatalf("checkCommand(%q) err = nil, want %q", scriptPath, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("checkCommand(%q) err = %v, want substring %q", scriptPath, err, tc.wantErr)
				}
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out, want) {
					t.Fatalf("checkCommand(%q) stdout = %q, want substring %q", scriptPath, out, want)
				}
			}
		})
	}
}

func TestCheckCommandRequiresScriptPath(t *testing.T) {
	t.Parallel()
	_, err := dispatchCommand(t, "check", nil)
	if err == nil || !strings.Contains(err.Error(), "script path required") {
		t.Fatalf("checkCommand(nil) err = %v, want script path required", err)
	}

	_, err = dispatchCommand(t, "check", []string{"a.vibe", "b.vibe"})
	if err == nil || !strings.Contains(err.Error(), "expected a single script path") {
		t.Fatalf("checkCommand(two paths) err = %v, want single-path rejection", err)
	}
}

func TestCheckCommandResolvesModulePaths(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	script := `require "helpers"

count = 1
shout(count)`
	scriptPath := writeVibeScript(t, script)

	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, scriptPath})
	if err == nil || !strings.Contains(err.Error(), "check failed with") {
		t.Fatalf("checkCommand err = %v, want check failure", err)
	}
	if !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand stdout = %q, want module-typed argument warning", out)
	}
}

func TestCheckCommandSeedsTopLevelRequires(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	// The mismatched call lives in a function nothing invokes: whole-script
	// checking must still resolve the export's signature through the
	// top-level require.
	script := `require "helpers"

def broken
  shout(1)
end`
	scriptPath := writeVibeScript(t, script)

	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, scriptPath})
	if err == nil || !strings.Contains(err.Error(), "check failed with") {
		t.Fatalf("checkCommand err = %v, want check failure", err)
	}
	if !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand stdout = %q, want required-export argument warning", out)
	}
}

func TestCheckCommandResolvesRequiredTypesInAnnotations(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `enum Status
  Draft
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	// The annotation lives in a function nothing invokes: the required
	// enum must resolve in the per-function runtime roots too, not warn as
	// an unknown type.
	script := `require "helpers"

def advance(status: Status) -> Status
  status
end`
	scriptPath := writeVibeScript(t, script)

	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, scriptPath})
	if err != nil {
		t.Fatalf("checkCommand err = %v, want nil", err)
	}
	if !strings.Contains(out, "No issues found") {
		t.Fatalf("checkCommand stdout = %q, want no issues", out)
	}
}

func TestCheckCommandAttributesModuleDiagnosticsToModuleFiles(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def bad -> int
  "text"
end`
	modulePath := filepath.Join(moduleDir, "helpers.vibe")
	if err := os.WriteFile(modulePath, []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	script := `require "helpers"

bad`
	scriptPath := writeVibeScript(t, script)

	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, scriptPath})
	if err == nil || !strings.Contains(err.Error(), "check failed with") {
		t.Fatalf("checkCommand err = %v, want check failure", err)
	}
	if !strings.Contains(out, "helpers.vibe:2:") {
		t.Fatalf("checkCommand stdout = %q, want module-file-prefixed warning", out)
	}
}

func TestCheckCommandReportsModuleDiagnosticsForRequireOnlyScripts(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def bad -> int
  "text"
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	// A script that only requires the module still surfaces the module's
	// own diagnostics; the suppressed require seed must not consume them.
	scriptPath := writeVibeScript(t, `require "helpers"`)

	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, scriptPath})
	if err == nil || !strings.Contains(err.Error(), "check failed with") {
		t.Fatalf("checkCommand err = %v, want check failure", err)
	}
	if !strings.Contains(out, "return value expected int, got string (helpers.bad)") {
		t.Fatalf("checkCommand stdout = %q, want module diagnostic", out)
	}
}

func TestCheckCommandRespectsEntrypointRequireOrder(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	// A top-level call before the require runs without the exports, so the
	// export's contract must not resolve early (matching run -check); the
	// same bad call after the require reports.
	before := writeVibeScript(t, `shout(1)
require "helpers"`)
	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, before})
	if err != nil {
		t.Fatalf("checkCommand before-require err = %v (out %q), want nil", err, out)
	}

	after := writeVibeScriptNamed(t, "after.vibe", `require "helpers"
shout(1)`)
	out, err = dispatchCommand(t, "check", []string{"-module-path", moduleDir, after})
	if err == nil || !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand after-require = %v (out %q), want argument warning", err, out)
	}
}

func TestCheckCommandChecksEntrypointCalleesInCallOrder(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	// A callee invoked before the require runs without the exports, so the
	// export's contract must not resolve early — matching the direct
	// top-level call semantics.
	before := writeVibeScript(t, `def run
  shout(1)
end

run
require "helpers"`)
	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, before})
	if err != nil {
		t.Fatalf("checkCommand callee-before-require err = %v (out %q), want nil", err, out)
	}

	// The same callee after the require checks under the loaded exports.
	after := writeVibeScriptNamed(t, "after.vibe", `require "helpers"

def run
  shout(1)
end

run`)
	out, err = dispatchCommand(t, "check", []string{"-module-path", moduleDir, after})
	if err == nil || !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand callee-after-require = %v (out %q), want argument warning", err, out)
	}
}

func TestCheckCommandSeedsInferredGuaranteedEntrypointRequires(t *testing.T) {
	t.Parallel()
	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	// The guard is a local whose inferred type proves the require always
	// runs, so the exports seed for later functions exactly as at runtime.
	guaranteed := writeVibeScript(t, `flag = "yes"
flag && require "helpers"

def run
  shout(1)
end`)
	out, err := dispatchCommand(t, "check", []string{"-module-path", moduleDir, guaranteed})
	if err == nil || !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand guaranteed guard = %v (out %q), want argument warning", err, out)
	}

	// A guard the checker cannot prove keeps the seed conservative: the
	// module may never load, so the unknown callee stays permitted.
	unknown := writeVibeScriptNamed(t, "unknown.vibe", `flag = JSON.parse("true")
flag && require "helpers"

def run
  shout(1)
end`)
	out, err = dispatchCommand(t, "check", []string{"-module-path", moduleDir, unknown})
	if err != nil {
		t.Fatalf("checkCommand unknown guard err = %v (out %q), want nil", err, out)
	}
}

func TestCheckCommandRejectsOversizedScript(t *testing.T) {
	t.Parallel()
	path := writeOversizedScript(t, "script.vibe")

	_, err := dispatchCommand(t, "check", []string{path})
	if err == nil {
		t.Fatalf("checkCommand(%q) err = nil, want source-size rejection", path)
	}
	for _, want := range []string{"read script", "source exceeds maximum size"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("checkCommand(%q) err = %v, want substring %q", path, err, want)
		}
	}
}
