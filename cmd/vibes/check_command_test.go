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
			out, err := captureStdout(t, func() error {
				return checkCommand([]string{scriptPath})
			})
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
	_, err := captureStdout(t, func() error {
		return checkCommand(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "script path required") {
		t.Fatalf("checkCommand(nil) err = %v, want script path required", err)
	}

	_, err = captureStdout(t, func() error {
		return checkCommand([]string{"a.vibe", "b.vibe"})
	})
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

	out, err := captureStdout(t, func() error {
		return checkCommand([]string{"-module-path", moduleDir, scriptPath})
	})
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

	out, err := captureStdout(t, func() error {
		return checkCommand([]string{"-module-path", moduleDir, scriptPath})
	})
	if err == nil || !strings.Contains(err.Error(), "check failed with") {
		t.Fatalf("checkCommand err = %v, want check failure", err)
	}
	if !strings.Contains(out, "call to shout argument value expected string, got int") {
		t.Fatalf("checkCommand stdout = %q, want required-export argument warning", out)
	}
}

func TestCheckCommandRejectsOversizedScript(t *testing.T) {
	t.Parallel()
	path := writeOversizedScript(t, "script.vibe")

	_, err := captureStdout(t, func() error {
		return checkCommand([]string{path})
	})
	if err == nil {
		t.Fatalf("checkCommand(%q) err = nil, want source-size rejection", path)
	}
	for _, want := range []string{"read script", "source exceeds maximum size"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("checkCommand(%q) err = %v, want substring %q", path, err, want)
		}
	}
}
