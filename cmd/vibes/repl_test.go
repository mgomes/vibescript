package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
