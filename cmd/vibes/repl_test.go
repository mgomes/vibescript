package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

func TestUpdateQuitCommandReturnsQuit(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue(":quit")

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm, ok := model.(replModel)
	if !ok {
		t.Fatalf("unexpected model type %T", model)
	}

	if !rm.quitting {
		t.Fatalf("quitting flag not set")
	}
	if rm.textInput.Value() != "" {
		t.Fatalf("input not cleared after quit command")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit command")
	}
	if msg := cmd(); msg != nil {
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Fatalf("expected QuitMsg, got %T", msg)
		}
	}
}

func TestUpdateNonQuitCommandDoesNotReturnCmd(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue(":help")

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm, ok := model.(replModel)
	if !ok {
		t.Fatalf("unexpected model type %T", model)
	}

	if cmd != nil {
		t.Fatalf("expected no command for non-quit input")
	}
	if rm.quitting {
		t.Fatalf("quitting should remain false")
	}
	if !rm.showHelp {
		t.Fatalf("help toggle should be enabled")
	}
	if rm.textInput.Value() != "" {
		t.Fatalf("input not cleared after command")
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		setup     func(*replModel)
		input     string
		wantErr   bool
		check     func(t *testing.T, m *replModel, output string)
		wantOut   string // substring to require in output when wantErr=true
		errInLast string // substring to require in m.lastError when wantErr=true
	}{
		{
			name:  "assignment_stores_variable",
			input: "score = 42",
			check: func(t *testing.T, m *replModel, _ string) {
				score, ok := m.env["score"]
				if !ok {
					t.Fatalf("expected score to be stored in repl env")
				}
				if score.Kind() != value.KindInt || score.Int() != 42 {
					t.Fatalf("unexpected score value: %#v", score)
				}
			},
		},
		{
			name:  "destructuring_assignment_stores_variables",
			input: "first, *rest, last = [1, 2, 3, 4]",
			check: func(t *testing.T, m *replModel, _ string) {
				first, ok := m.env["first"]
				if !ok {
					t.Fatalf("expected first to be stored in repl env")
				}
				if first.Kind() != value.KindInt || first.Int() != 1 {
					t.Fatalf("unexpected first value: %#v", first)
				}

				rest, ok := m.env["rest"]
				if !ok {
					t.Fatalf("expected rest to be stored in repl env")
				}
				if rest.Kind() != value.KindArray {
					t.Fatalf("unexpected rest kind: %#v", rest)
				}
				restValues := rest.Array()
				if len(restValues) != 2 || restValues[0].Int() != 2 || restValues[1].Int() != 3 {
					t.Fatalf("unexpected rest values: %#v", restValues)
				}

				last, ok := m.env["last"]
				if !ok {
					t.Fatalf("expected last to be stored in repl env")
				}
				if last.Kind() != value.KindInt || last.Int() != 4 {
					t.Fatalf("unexpected last value: %#v", last)
				}
			},
		},
		{
			name:  "equality_does_not_overwrite_variable",
			setup: func(m *replModel) { m.env["a"] = value.NewInt(5) },
			input: "a == 5",
			check: func(t *testing.T, m *replModel, _ string) {
				a := m.env["a"]
				if a.Kind() != value.KindInt || a.Int() != 5 {
					t.Fatalf("variable a was clobbered by equality expression: %#v", a)
				}
			},
		},
		{
			name:  "sets_underscore_to_last_result",
			input: "40 + 2",
			check: func(t *testing.T, m *replModel, _ string) {
				last, ok := m.env["_"]
				if !ok {
					t.Fatalf("expected underscore variable to be set")
				}
				if last.Kind() != value.KindInt || last.Int() != 42 {
					t.Fatalf("unexpected underscore value: %#v", last)
				}
			},
		},
		{
			name:    "compile_error_returns_error",
			input:   "def broken(",
			wantErr: true,
			check: func(t *testing.T, _ *replModel, output string) {
				if output == "" {
					t.Fatalf("expected non-empty compile error")
				}
			},
		},
		{
			name:      "runtime_error_returns_error",
			input:     "unknown_var",
			wantErr:   true,
			wantOut:   "undefined variable",
			errInLast: "runtime error:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := newREPLModel()
			if err != nil {
				t.Fatalf("newREPLModel failed: %v", err)
			}
			if tc.setup != nil {
				tc.setup(&m)
			}
			output, isErr := m.evaluate(tc.input)
			if isErr != tc.wantErr {
				t.Fatalf("evaluate(%q) isErr = %v, want %v (output=%q)", tc.input, isErr, tc.wantErr, output)
			}
			if tc.wantOut != "" && !strings.Contains(output, tc.wantOut) {
				t.Fatalf("evaluate(%q) output = %q, want substring %q", tc.input, output, tc.wantOut)
			}
			if tc.errInLast != "" {
				if m.lastError == "" {
					t.Fatalf("expected last error to be captured")
				}
				if !strings.Contains(m.lastError, tc.errInLast) {
					t.Fatalf("evaluate(%q) lastError = %q, want substring %q", tc.input, m.lastError, tc.errInLast)
				}
			}
			if tc.check != nil {
				tc.check(t, &m, output)
			}
		})
	}
}

func TestEvaluateErrorsUseREPLSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		want     []string
		notWant  []string
		wantType string
	}{
		{
			name:     "compile_error",
			input:    "y = (",
			wantType: "compile error:",
			want:     []string{"parse error at 1:", "y = (", "unexpected end of snippet"},
			notWant:  []string{"__repl__", "line 3"},
		},
		{
			name:     "runtime_error",
			input:    "1 / 0",
			wantType: "runtime error:",
			want:     []string{"division by zero", "line 1", "at <repl> (1:"},
			notWant:  []string{"__repl__"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := newREPLModel()
			if err != nil {
				t.Fatalf("newREPLModel failed: %v", err)
			}
			output, isErr := m.evaluate(tc.input)
			if !isErr {
				t.Fatalf("evaluate(%q) isErr = false, want true", tc.input)
			}
			if !strings.Contains(output, tc.wantType) {
				t.Fatalf("evaluate(%q) output = %q, want substring %q", tc.input, output, tc.wantType)
			}
			for _, want := range tc.want {
				if !strings.Contains(output, want) {
					t.Fatalf("evaluate(%q) output = %q, want substring %q", tc.input, output, want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(output, notWant) {
					t.Fatalf("evaluate(%q) output = %q, must not contain %q", tc.input, output, notWant)
				}
			}
		})
	}
}

func TestLastErrorCommandShowsPreviousError(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	_, isErr := m.evaluate("unknown_var")
	if !isErr {
		t.Fatalf("expected runtime error")
	}

	m, _ = m.handleCommand(":last_error")
	if len(m.history) == 0 {
		t.Fatalf("expected history entry for :last_error")
	}
	last := m.history[len(m.history)-1]
	if !last.isErr {
		t.Fatalf("expected :last_error to render as error entry")
	}
	if !strings.Contains(last.output, "runtime error:") {
		t.Fatalf("expected runtime error output, got %q", last.output)
	}
}

func TestLastErrorCommandWhenNoError(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	m, _ = m.handleCommand(":last_error")
	if len(m.history) == 0 {
		t.Fatalf("expected history entry for :last_error")
	}
	last := m.history[len(m.history)-1]
	if last.isErr {
		t.Fatalf("expected non-error status when there is no previous error")
	}
	if last.output != "No previous error" {
		t.Fatalf("unexpected output: %q", last.output)
	}
}

func TestGlobalsCommandPrintsSortedGlobals(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["zeta"] = value.NewString("last")
	m.env["alpha"] = value.NewInt(1)

	m, _ = m.handleCommand(":globals")
	if len(m.history) == 0 {
		t.Fatalf("expected history entry for :globals")
	}
	last := m.history[len(m.history)-1]
	if last.isErr {
		t.Fatalf("expected :globals result to be non-error")
	}
	if last.output != "alpha = 1\nzeta = last" {
		t.Fatalf("unexpected globals output: %q", last.output)
	}
}

func TestFunctionsCommandListsBuiltinsAndEnvCallables(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["worker"] = vibes.NewBuiltin("worker.call", func(exec *vibes.Execution, receiver value.Value, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
		return value.NewString("ok"), nil
	})
	m.env["count"] = value.NewInt(1)

	m, _ = m.handleCommand(":functions")
	if len(m.history) == 0 {
		t.Fatalf("expected history entry for :functions")
	}
	last := m.history[len(m.history)-1]
	if last.isErr {
		t.Fatalf("expected :functions result to be non-error")
	}
	if !strings.Contains(last.output, "JSON.parse") {
		t.Fatalf("expected JSON.parse in functions output: %q", last.output)
	}
	if !strings.Contains(last.output, "worker") {
		t.Fatalf("expected env callable in functions output: %q", last.output)
	}
	if strings.Contains(last.output, "count") {
		t.Fatalf("non-callable env value should not appear in functions output: %q", last.output)
	}
}

func TestTypesCommandShowsKinds(t *testing.T) {
	t.Parallel()
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["count"] = value.NewInt(1)
	m.env["name"] = value.NewString("alex")

	m, _ = m.handleCommand(":types")
	if len(m.history) == 0 {
		t.Fatalf("expected history entry for :types")
	}
	last := m.history[len(m.history)-1]
	if last.isErr {
		t.Fatalf("expected :types result to be non-error")
	}
	if !strings.Contains(last.output, "count: int") {
		t.Fatalf("missing count type output: %q", last.output)
	}
	if !strings.Contains(last.output, "name: string") {
		t.Fatalf("missing name type output: %q", last.output)
	}
}

func TestAutocomplete(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		setup          func(*replModel)
		input          string
		wantValue      string // expected textInput.Value after handleAutocomplete; "" means don't check
		wantHistorySub string // substring to require in last history entry; "" means require no history entry produced by autocomplete
	}{
		{
			name:      "single_completion",
			input:     "requ",
			wantValue: "require",
		},
		{
			name:           "multiple_completions_add_history_entry",
			input:          "m",
			wantHistorySub: "Completions:",
		},
		{
			name:      "uses_env_variables",
			setup:     func(m *replModel) { m.env["tenant_id"] = value.NewString("acme") },
			input:     "tenant",
			wantValue: "tenant_id",
		},
		{
			name:      "completes_commands",
			input:     ":gl",
			wantValue: ":globals",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := newREPLModel()
			if err != nil {
				t.Fatalf("newREPLModel failed: %v", err)
			}
			if tc.setup != nil {
				tc.setup(&m)
			}
			m.textInput.SetValue(tc.input)
			m = m.handleAutocomplete()

			if tc.wantValue != "" {
				if got := m.textInput.Value(); got != tc.wantValue {
					t.Fatalf("handleAutocomplete(%q) text = %q, want %q", tc.input, got, tc.wantValue)
				}
			}
			if tc.wantHistorySub != "" {
				if len(m.history) == 0 {
					t.Fatalf("expected completion history entry for %q", tc.input)
				}
				last := m.history[len(m.history)-1]
				if !strings.Contains(last.output, tc.wantHistorySub) {
					t.Fatalf("handleAutocomplete(%q) last history = %q, want substring %q", tc.input, last.output, tc.wantHistorySub)
				}
				if tc.name == "multiple_completions_add_history_entry" && !strings.Contains(last.output, "money") {
					t.Fatalf("expected builtins in completion output: %q", last.output)
				}
			}
		})
	}
}
