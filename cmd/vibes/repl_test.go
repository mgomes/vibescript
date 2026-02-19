package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mgomes/vibescript/vibes"
)

func TestUpdateQuitCommandReturnsQuit(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue(":quit")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue(":help")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

func TestEvaluateAssignmentStoresVariable(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	output, isErr := m.evaluate("score = 42")
	if isErr {
		t.Fatalf("unexpected eval error: %s", output)
	}

	score, ok := m.env["score"]
	if !ok {
		t.Fatalf("expected score to be stored in repl env")
	}
	if score.Kind() != vibes.KindInt || score.Int() != 42 {
		t.Fatalf("unexpected score value: %#v", score)
	}
}

func TestEvaluateEqualityDoesNotOverwriteVariable(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["a"] = vibes.NewInt(5)

	output, isErr := m.evaluate("a == 5")
	if isErr {
		t.Fatalf("unexpected eval error: %s", output)
	}

	a := m.env["a"]
	if a.Kind() != vibes.KindInt || a.Int() != 5 {
		t.Fatalf("variable a was clobbered by equality expression: %#v", a)
	}
}

func TestEvaluateSetsUnderscoreToLastResult(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	output, isErr := m.evaluate("40 + 2")
	if isErr {
		t.Fatalf("unexpected eval error: %s", output)
	}

	last, ok := m.env["_"]
	if !ok {
		t.Fatalf("expected underscore variable to be set")
	}
	if last.Kind() != vibes.KindInt || last.Int() != 42 {
		t.Fatalf("unexpected underscore value: %#v", last)
	}
}

func TestEvaluateCompileErrorReturnsError(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	output, isErr := m.evaluate("def broken(")
	if !isErr {
		t.Fatalf("expected compile error")
	}
	if output == "" {
		t.Fatalf("expected non-empty compile error")
	}
}

func TestEvaluateRuntimeErrorReturnsError(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}

	output, isErr := m.evaluate("unknown_var")
	if !isErr {
		t.Fatalf("expected runtime error")
	}
	if !strings.Contains(output, "undefined variable") {
		t.Fatalf("unexpected runtime error: %s", output)
	}
	if m.lastError == "" {
		t.Fatalf("expected last error to be captured")
	}
	if !strings.Contains(m.lastError, "runtime error:") {
		t.Fatalf("expected runtime error prefix, got %q", m.lastError)
	}
}

func TestLastErrorCommandShowsPreviousError(t *testing.T) {
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
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["zeta"] = vibes.NewString("last")
	m.env["alpha"] = vibes.NewInt(1)

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
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["worker"] = vibes.NewBuiltin("worker.call", func(exec *vibes.Execution, receiver vibes.Value, args []vibes.Value, kwargs map[string]vibes.Value, block vibes.Value) (vibes.Value, error) {
		return vibes.NewString("ok"), nil
	})
	m.env["count"] = vibes.NewInt(1)

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
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["count"] = vibes.NewInt(1)
	m.env["name"] = vibes.NewString("alex")

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

func TestAutocompleteSingleCompletion(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue("requ")

	m = m.handleAutocomplete()
	if got := m.textInput.Value(); got != "require" {
		t.Fatalf("expected single completion, got %q", got)
	}
}

func TestAutocompleteMultipleCompletionsAddsHistoryEntry(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue("m")

	m = m.handleAutocomplete()
	if len(m.history) == 0 {
		t.Fatalf("expected completion history entry")
	}
	last := m.history[len(m.history)-1]
	if !strings.Contains(last.output, "Completions:") {
		t.Fatalf("unexpected completion output: %q", last.output)
	}
	if !strings.Contains(last.output, "money") {
		t.Fatalf("expected builtins in completion output: %q", last.output)
	}
}

func TestAutocompleteUsesEnvVariables(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.env["tenant_id"] = vibes.NewString("acme")
	m.textInput.SetValue("tenant")

	m = m.handleAutocomplete()
	if got := m.textInput.Value(); got != "tenant_id" {
		t.Fatalf("expected env completion, got %q", got)
	}
}

func TestAutocompleteCompletesCommands(t *testing.T) {
	m, err := newREPLModel()
	if err != nil {
		t.Fatalf("newREPLModel failed: %v", err)
	}
	m.textInput.SetValue(":gl")

	m = m.handleAutocomplete()
	if got := m.textInput.Value(); got != ":globals" {
		t.Fatalf("expected command completion, got %q", got)
	}
}
