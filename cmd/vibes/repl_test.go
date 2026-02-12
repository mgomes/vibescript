package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mgomes/vibescript/vibes"
)

func TestUpdateQuitCommandReturnsQuit(t *testing.T) {
	m := newREPLModel()
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
	m := newREPLModel()
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
	m := newREPLModel()

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
	m := newREPLModel()
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
