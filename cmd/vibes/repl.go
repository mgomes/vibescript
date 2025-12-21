package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mgomes/vibescript/vibes"
)

var (
	accentColor    = lipgloss.Color("#3B82F6")
	successColor   = lipgloss.Color("#10B981")
	errorColor     = lipgloss.Color("#EF4444")
	mutedColor     = lipgloss.Color("#6B7280")
	highlightColor = lipgloss.Color("#F59E0B")

	promptStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	resultStyle = lipgloss.NewStyle().
			Foreground(successColor)

	errorStyle = lipgloss.NewStyle().
			Foreground(errorColor)

	mutedStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	headerStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true).
			Padding(0, 1)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(highlightColor)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(0, 1)
)

type historyEntry struct {
	input  string
	output string
	isErr  bool
}

type replModel struct {
	textInput   textinput.Model
	engine      *vibes.Engine
	env         map[string]vibes.Value
	history     []historyEntry
	cmdHistory  []string
	historyIdx  int
	width       int
	height      int
	showHelp    bool
	showVars    bool
	quitting    bool
	initialized bool
}

type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Enter     key.Binding
	CtrlC     key.Binding
	CtrlD     key.Binding
	CtrlL     key.Binding
	Tab       key.Binding
	CtrlV     key.Binding
	CtrlH     key.Binding
	ShiftUp   key.Binding
	ShiftDown key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑", "previous command"),
	),
	Down: key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next command"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "execute"),
	),
	CtrlC: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
	CtrlD: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "quit"),
	),
	CtrlL: key.NewBinding(
		key.WithKeys("ctrl+l"),
		key.WithHelp("ctrl+l", "clear"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "autocomplete"),
	),
	CtrlV: key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "toggle vars"),
	),
	CtrlH: key.NewBinding(
		key.WithKeys("ctrl+k"),
		key.WithHelp("ctrl+k", "toggle help"),
	),
	ShiftUp: key.NewBinding(
		key.WithKeys("shift+up"),
	),
	ShiftDown: key.NewBinding(
		key.WithKeys("shift+down"),
	),
}

func newREPLModel() replModel {
	ti := textinput.New()
	ti.Placeholder = "type an expression..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 60
	ti.PromptStyle = promptStyle
	ti.Prompt = "vibes> "

	engine := vibes.NewEngine(vibes.Config{})

	return replModel{
		textInput:  ti,
		engine:     engine,
		env:        make(map[string]vibes.Value),
		history:    make([]historyEntry, 0),
		cmdHistory: make([]string, 0),
		historyIdx: -1,
		showHelp:   false,
		showVars:   false,
	}
}

func (m replModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tea.EnterAltScreen)
}

func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.Width = msg.Width - 10
		m.initialized = true
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.CtrlC), key.Matches(msg, keys.CtrlD):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, keys.CtrlL):
			m.history = make([]historyEntry, 0)
			return m, nil

		case key.Matches(msg, keys.CtrlV):
			m.showVars = !m.showVars
			return m, nil

		case key.Matches(msg, keys.CtrlH):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, keys.Up):
			if len(m.cmdHistory) > 0 {
				if m.historyIdx == -1 {
					m.historyIdx = len(m.cmdHistory) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.textInput.SetValue(m.cmdHistory[m.historyIdx])
				m.textInput.CursorEnd()
			}
			return m, nil

		case key.Matches(msg, keys.Down):
			if m.historyIdx != -1 {
				if m.historyIdx < len(m.cmdHistory)-1 {
					m.historyIdx++
					m.textInput.SetValue(m.cmdHistory[m.historyIdx])
				} else {
					m.historyIdx = -1
					m.textInput.SetValue("")
				}
				m.textInput.CursorEnd()
			}
			return m, nil

		case key.Matches(msg, keys.Tab):
			m = m.handleAutocomplete()
			return m, nil

		case key.Matches(msg, keys.Enter):
			input := strings.TrimSpace(m.textInput.Value())
			if input == "" {
				return m, nil
			}

			if strings.HasPrefix(input, ":") {
				var cmd tea.Cmd
				m, cmd = m.handleCommand(input)
				m.textInput.SetValue("")
				m.historyIdx = -1
				return m, cmd
			}

			output, isErr := m.evaluate(input)
			m.history = append(m.history, historyEntry{
				input:  input,
				output: output,
				isErr:  isErr,
			})
			m.cmdHistory = append(m.cmdHistory, input)
			m.textInput.SetValue("")
			m.historyIdx = -1
			return m, nil
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m replModel) handleCommand(input string) (replModel, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case ":help", ":h":
		m.showHelp = !m.showHelp
	case ":clear", ":c":
		m.history = make([]historyEntry, 0)
	case ":vars", ":v":
		m.showVars = !m.showVars
	case ":reset", ":r":
		m.env = make(map[string]vibes.Value)
		m.history = append(m.history, historyEntry{
			input:  input,
			output: "Environment reset",
			isErr:  false,
		})
	case ":quit", ":q":
		m.quitting = true
		return m, tea.Quit
	default:
		m.history = append(m.history, historyEntry{
			input:  input,
			output: fmt.Sprintf("Unknown command: %s", cmd),
			isErr:  true,
		})
	}
	return m, nil
}

func (m replModel) handleAutocomplete() replModel {
	input := m.textInput.Value()
	if input == "" {
		return m
	}

	// Get the last word for completion
	words := strings.Fields(input)
	if len(words) == 0 {
		return m
	}
	lastWord := words[len(words)-1]

	// Collect completions
	var completions []string

	// Built-in functions
	builtins := []string{"assert", "money", "money_cents", "require", "now"}
	for _, b := range builtins {
		if strings.HasPrefix(b, lastWord) {
			completions = append(completions, b)
		}
	}

	// Keywords
	keywords := []string{"fn", "if", "else", "for", "in", "return", "true", "false", "nil", "and", "or"}
	for _, k := range keywords {
		if strings.HasPrefix(k, lastWord) {
			completions = append(completions, k)
		}
	}

	// Environment variables
	for name := range m.env {
		if strings.HasPrefix(name, lastWord) {
			completions = append(completions, name)
		}
	}

	if len(completions) == 1 {
		// Single match - complete it
		prefix := strings.TrimSuffix(input, lastWord)
		m.textInput.SetValue(prefix + completions[0])
		m.textInput.CursorEnd()
	} else if len(completions) > 1 {
		// Multiple matches - show them in history
		m.history = append(m.history, historyEntry{
			input:  "",
			output: "Completions: " + strings.Join(completions, ", "),
			isErr:  false,
		})
	}

	return m
}

func (m replModel) evaluate(input string) (string, bool) {
	wrapped := fmt.Sprintf("def __repl__()\n  %s\nend", input)

	script, err := m.engine.Compile(wrapped)
	if err != nil {
		return err.Error(), true
	}

	opts := vibes.CallOptions{
		Globals: m.env,
	}

	result, err := script.Call(context.Background(), "__repl__", nil, opts)
	if err != nil {
		return err.Error(), true
	}

	m.extractAssignments(input, result)

	m.env["_"] = result

	if result.IsNil() {
		return "nil", false
	}
	return formatValue(result), false
}

func (m *replModel) extractAssignments(input string, result vibes.Value) {
	parts := strings.SplitN(input, "=", 2)
	if len(parts) == 2 {
		name := strings.TrimSpace(parts[0])
		if isValidIdentifier(name) && !result.IsNil() {
			m.env[name] = result
		}
	}
}

func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return true
}

func formatValue(v vibes.Value) string {
	return v.String()
}

func (m replModel) View() string {
	if !m.initialized {
		return "Loading..."
	}

	if m.quitting {
		return mutedStyle.Render("Goodbye!\n")
	}

	var b strings.Builder

	header := headerStyle.Render("VibeScript REPL")
	version := mutedStyle.Render("v0.1.0")
	b.WriteString(header + " " + version + "\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", min(m.width-2, 60))) + "\n\n")

	reservedLines := 8 // header, input, help hint, etc.
	if m.showHelp {
		reservedLines += 10
	}
	if m.showVars {
		reservedLines += len(m.env) + 3
	}
	availableHeight := m.height - reservedLines

	historyStart := 0
	if len(m.history) > availableHeight {
		historyStart = len(m.history) - availableHeight
	}

	for i := historyStart; i < len(m.history); i++ {
		entry := m.history[i]
		if entry.input != "" {
			b.WriteString(mutedStyle.Render("  › ") + entry.input + "\n")
		}
		if entry.isErr {
			b.WriteString("  " + errorStyle.Render("✗ "+entry.output) + "\n")
		} else {
			b.WriteString("  " + resultStyle.Render("→ "+entry.output) + "\n")
		}
		b.WriteString("\n")
	}

	if m.showVars {
		b.WriteString(renderVarsPanel(m.env, m.width))
		b.WriteString("\n")
	}

	if m.showHelp {
		b.WriteString(renderHelpPanel(m.width))
		b.WriteString("\n")
	}

	b.WriteString(m.textInput.View() + "\n\n")

	footer := helpKeyStyle.Render("ctrl+k") + helpDescStyle.Render(" help  ") +
		helpKeyStyle.Render("ctrl+v") + helpDescStyle.Render(" vars  ") +
		helpKeyStyle.Render("ctrl+l") + helpDescStyle.Render(" clear  ") +
		helpKeyStyle.Render("ctrl+c") + helpDescStyle.Render(" quit")
	b.WriteString(footer)

	return b.String()
}

func renderVarsPanel(env map[string]vibes.Value, width int) string {
	if len(env) == 0 {
		return borderStyle.Render(mutedStyle.Render("No variables defined"))
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accentColor).Render("Variables"))
	varNameStyle := lipgloss.NewStyle().Foreground(highlightColor)
	for name, val := range env {
		line := fmt.Sprintf("  %s = %s", varNameStyle.Render(name), val.String())
		lines = append(lines, line)
	}
	return borderStyle.Render(strings.Join(lines, "\n"))
}

func renderHelpPanel(width int) string {
	help := []struct {
		key  string
		desc string
	}{
		{"↑/↓", "Navigate command history"},
		{"Tab", "Autocomplete"},
		{"Enter", "Execute expression"},
		{":help", "Toggle this help"},
		{":vars", "Toggle variables panel"},
		{":clear", "Clear history"},
		{":reset", "Reset environment"},
		{":quit", "Exit REPL"},
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accentColor).Render("Help"))
	for _, h := range help {
		line := fmt.Sprintf("  %s  %s",
			helpKeyStyle.Render(fmt.Sprintf("%-8s", h.key)),
			helpDescStyle.Render(h.desc))
		lines = append(lines, line)
	}

	return borderStyle.Render(strings.Join(lines, "\n"))
}

func runREPL() error {
	p := tea.NewProgram(newREPLModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
