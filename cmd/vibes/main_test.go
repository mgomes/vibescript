package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "help", args: []string{"help"}},
		{name: "invalid_command", args: []string{"unknown"}, wantErr: "invalid command"},
		{name: "missing_command", args: nil, wantErr: "invalid command"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := dispatchCLI(t, tc.args...)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("dispatchCLI(%v) err = %v, want nil", tc.args, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("dispatchCLI(%v) err = nil, want %q", tc.args, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("dispatchCLI(%v) err = %v, want substring %q", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestRunCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		script  string
		args    func(scriptPath string) []string
		wantOut string
		wantErr string
	}{
		{
			name: "check_only",
			script: `def run
  "ok"
end`,
			args: func(p string) []string { return []string{"-check", p} },
		},
		{
			name: "executes_function_and_prints_result",
			script: `def greet(name)
  name
end`,
			args:    func(p string) []string { return []string{"-function", "greet", p, "hello"} },
			wantOut: "hello",
		},
		{
			name: "default_function_without_top_level_body",
			script: `def run
  "ok"
end`,
			args:    func(p string) []string { return []string{p} },
			wantOut: "ok",
		},
		{
			name: "default_function_with_class_initializer",
			script: `class Settings
  @@limit = 10

  def self.limit
    @@limit
  end
end

def run
  Settings.limit
end`,
			args:    func(p string) []string { return []string{p} },
			wantOut: "10",
		},
		{
			name: "executes_top_level_script_body",
			script: `def double(x)
  x * 2
end

double(3)`,
			args:    func(p string) []string { return []string{p} },
			wantOut: "6",
		},
		{
			name: "check_only_allows_top_level_script_body",
			script: `def double(x)
  x * 2
end

double(3)`,
			args: func(p string) []string { return []string{"-check", p} },
		},
		{
			name: "explicit_function_ignores_top_level_script_body",
			script: `def greet(name)
  name
end

greet("top")`,
			args:    func(p string) []string { return []string{"-function", "greet", p, "hello"} },
			wantOut: "hello",
		},
		{
			name: "explicit_function_initializes_deferred_class_body",
			script: `class Settings
  @@limit = 10

  def self.limit
    @@limit
  end
end

def run
  Settings.limit
end

99`,
			args:    func(p string) []string { return []string{"-function", "run", p} },
			wantOut: "10",
		},
		{
			name:    "requires_script_path",
			args:    func(string) []string { return nil },
			wantErr: "script path required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var args []string
			if tc.script != "" {
				args = tc.args(writeVibeScript(t, tc.script))
			} else {
				args = tc.args("")
			}
			out, err := captureStdout(t, func() error {
				return runCommand(args)
			})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("runCommand(%v) err = nil, want %q", args, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("runCommand(%v) err = %v, want substring %q", args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runCommand(%v) err = %v, want nil", args, err)
			}
			if tc.wantOut != "" {
				if got := strings.TrimSpace(out); got != tc.wantOut {
					t.Fatalf("runCommand(%v) stdout = %q, want %q", args, got, tc.wantOut)
				}
			}
		})
	}
}

func TestRunCommandInlineEval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantOut string
		wantErr string
	}{
		{
			name:    "prints_expression_result",
			args:    []string{"-e", "1 + 2"},
			wantOut: "3",
		},
		{
			name:    "multi_statement_snippet",
			args:    []string{"-e", "x = 2\ny = 3\nx * y"},
			wantOut: "6",
		},
		{
			name: "top_level_function_declaration",
			args: []string{"-e", `def helper
  1
end
helper`},
			wantOut: "1",
		},
		{
			name: "top_level_class_declaration",
			args: []string{"-e", `class Helper
  def value
    42
  end
end
Helper.new.value`},
			wantOut: "42",
		},
		{
			name: "top_level_enum_declaration",
			args: []string{"-e", `enum Status
  Draft
end
Status::Draft.name`},
			wantOut: "Draft",
		},
		{
			name: "top_level_export_declaration",
			args: []string{"-e", `export def helper
  "exported"
end
helper`},
			wantOut: "exported",
		},
		{
			name: "top_level_private_declaration",
			args: []string{"-e", `private def helper
  "private"
end
helper`},
			wantOut: "private",
		},
		{
			name: "check_only_compiles_without_executing",
			args: []string{"-check", "-e", "1 + 2"},
		},
		{
			name:    "compile_error_surfaces",
			args:    []string{"-e", "def oops("},
			wantErr: "compile failed",
		},
		{
			name:    "empty_snippet_rejected",
			args:    []string{"-e", "   "},
			wantErr: "requires a non-empty snippet",
		},
		{
			name:    "watch_combination_rejected",
			args:    []string{"-watch", "-e", "1"},
			wantErr: "-e cannot be combined with -watch",
		},
		{
			name:    "function_combination_rejected",
			args:    []string{"-function", "main", "-e", "1"},
			wantErr: "-e cannot be combined with -function",
		},
		{
			name:    "positional_args_rejected",
			args:    []string{"-e", "1", "extra"},
			wantErr: "-e does not accept positional arguments",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				return runCommand(tc.args)
			})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("runCommand(%v) err = nil, want %q", tc.args, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("runCommand(%v) err = %v, want substring %q", tc.args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runCommand(%v) err = %v, want nil", tc.args, err)
			}
			if got := strings.TrimSpace(out); got != tc.wantOut {
				t.Fatalf("runCommand(%v) stdout = %q, want %q", tc.args, got, tc.wantOut)
			}
		})
	}
}

func TestRunCommandInlineEvalCompileErrorUsesSnippetSource(t *testing.T) {
	t.Parallel()
	_, err := captureStdout(t, func() error {
		return runCommand([]string{"-e", "x = 1\ny = ("})
	})
	if err == nil {
		t.Fatal("runCommand(-e invalid snippet) err = nil, want compile error")
	}
	msg := err.Error()
	for _, want := range []string{"compile failed", "parse error at 2:", "y = (", "unexpected end of snippet"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCommand(-e invalid snippet) err = %q, want substring %q", msg, want)
		}
	}
	for _, notWant := range []string{"__eval__", "line 4"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("runCommand(-e invalid snippet) err = %q, must not contain %q", msg, notWant)
		}
	}
}

func TestRunCommandInlineEvalRuntimeErrorUsesSnippetFrame(t *testing.T) {
	t.Parallel()
	_, err := captureStdout(t, func() error {
		return runCommand([]string{"-e", "x = 1\n1 / 0"})
	})
	if err == nil {
		t.Fatal("runCommand(-e runtime error) err = nil, want runtime error")
	}
	msg := err.Error()
	for _, want := range []string{"execution failed", "division by zero", "line 2", "at <snippet> (2:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCommand(-e runtime error) err = %q, want substring %q", msg, want)
		}
	}
	if strings.Contains(msg, "__eval__") {
		t.Fatalf("runCommand(-e runtime error) err = %q, must not mention __eval__", msg)
	}
}

func TestRunCommandInlineEvalPreservesModuleRuntimeFrame(t *testing.T) {
	t.Parallel()
	dir := newTestCLI(t)
	helperPath := filepath.Join(dir, "helper.vibe")
	if err := os.WriteFile(helperPath, []byte("def boom()\n  1 / 0\nend\n"), 0o644); err != nil {
		t.Fatalf("write helper module: %v", err)
	}

	_, err := captureStdout(t, func() error {
		return runCommand([]string{"-module-path", dir, "-e", "helper = require(\"helper\")\nhelper.boom()"})
	})
	if err == nil {
		t.Fatal("runCommand(-e module runtime error) err = nil, want runtime error")
	}
	msg := err.Error()
	for _, want := range []string{"execution failed", "division by zero", "1 / 0", "at boom (2:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCommand(-e module runtime error) err = %q, want substring %q", msg, want)
		}
	}
	for _, notWant := range []string{"__eval__", "helper = require"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("runCommand(-e module runtime error) err = %q, must not contain %q", msg, notWant)
		}
	}
}

func TestAnalyzeCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		script      string
		wantOut     []string
		notWantOut  []string
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
			name: "top_level_script_body",
			script: `def double(x)
  x * 2
end

double(3)`,
			wantOut:     []string{"No issues found"},
			expectNoErr: true,
		},
		{
			name: "unreachable_statements",
			script: `def run()
  return 1
  2
end`,
			wantOut: []string{"unreachable statement"},
			wantErr: "analysis found 1 issue(s)",
		},
		{
			name: "unreachable_after_terminating_elsif_chain",
			script: `def run()
  if false
    return 1
  elsif true
    return 2
  else
    return 3
  end
  4
end`,
			wantOut: []string{"unreachable statement"},
			wantErr: "analysis found 1 issue(s)",
		},
		{
			name: "unreachable_after_begin_ensure_without_rescue",
			script: `def run()
  begin
    return 1
  ensure
    value = 2
  end
  3
end`,
			wantOut: []string{"unreachable statement"},
			wantErr: "analysis found 1 issue(s)",
		},
		{
			name: "unreachable_statements_in_class_methods",
			script: `class Reporter
  def instance_path()
    return 1
    2
  end

  def self.class_path()
    return 3
    4
  end
end

def run()
  Reporter.new.instance_path
end`,
			wantOut: []string{"(Reporter#instance_path)", "(Reporter.class_path)"},
			wantErr: "analysis found 2 issue(s)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scriptPath := writeVibeScript(t, tc.script)
			out, err := captureStdout(t, func() error {
				return analyzeCommand([]string{scriptPath})
			})
			if tc.expectNoErr {
				if err != nil {
					t.Fatalf("analyzeCommand(%q) err = %v, want nil", scriptPath, err)
				}
			} else {
				if err == nil {
					t.Fatalf("analyzeCommand(%q) err = nil, want %q", scriptPath, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("analyzeCommand(%q) err = %v, want substring %q", scriptPath, err, tc.wantErr)
				}
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out, want) {
					t.Fatalf("analyzeCommand(%q) stdout = %q, want substring %q", scriptPath, out, want)
				}
			}
			for _, notWant := range tc.notWantOut {
				if strings.Contains(out, notWant) {
					t.Fatalf("analyzeCommand(%q) stdout = %q, must not contain %q", scriptPath, out, notWant)
				}
			}
		})
	}
}

func TestComputeModulePathsIncludesScriptDirAndDedupesExtras(t *testing.T) {
	t.Parallel()
	scriptDir := newTestCLI(t)
	extraDir := newTestCLI(t)

	dirs, err := computeModulePaths(scriptDir, []string{scriptDir, extraDir, extraDir})
	if err != nil {
		t.Fatalf("computeModulePaths failed: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d (%v)", len(dirs), dirs)
	}

	wantScript, err := filepath.Abs(scriptDir)
	if err != nil {
		t.Fatalf("abs script dir: %v", err)
	}
	wantExtra, err := filepath.Abs(extraDir)
	if err != nil {
		t.Fatalf("abs extra dir: %v", err)
	}
	if dirs[0] != wantScript {
		t.Fatalf("expected first dir %q, got %q", wantScript, dirs[0])
	}
	if dirs[1] != wantExtra {
		t.Fatalf("expected second dir %q, got %q", wantExtra, dirs[1])
	}
}

func TestComputeModulePathsRejectsNonDirectoryExtra(t *testing.T) {
	t.Parallel()
	scriptDir := newTestCLI(t)
	file := filepath.Join(newTestCLI(t), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := computeModulePaths(scriptDir, []string{file})
	if err == nil {
		t.Fatalf("expected non-directory module path error")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
