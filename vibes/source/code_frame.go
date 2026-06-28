package source

import (
	"fmt"
	"strconv"
	"strings"
)

const maxCodeFrameLineRunes = 160

// CodeFrameFormatter renders code frames against a pre-split source.
type CodeFrameFormatter struct {
	lines []string
}

// NewCodeFrameFormatter returns a reusable formatter for source.
func NewCodeFrameFormatter(source string) *CodeFrameFormatter {
	if source == "" {
		return &CodeFrameFormatter{}
	}
	return &CodeFrameFormatter{lines: strings.Split(source, "\n")}
}

// FormatCodeFrame returns a human-readable source snippet highlighting
// the column at the given position. It returns the empty string when
// no useful frame can be produced (missing source, out-of-range line,
// etc.).
func FormatCodeFrame(source string, pos Position) string {
	return NewCodeFrameFormatter(source).Format(pos)
}

// Format returns a human-readable source snippet highlighting the column at
// the given position.
func (f *CodeFrameFormatter) Format(pos Position) string {
	if f == nil || len(f.lines) == 0 || pos.Line <= 0 {
		return ""
	}
	if pos.Line > len(f.lines) {
		return ""
	}

	lineText := f.lines[pos.Line-1]
	lineRunes := []rune(lineText)

	column := pos.Column
	if column <= 0 {
		column = 1
	}
	if column > len(lineRunes)+1 {
		column = len(lineRunes) + 1
	}
	displayText, displayColumn := codeFrameLineWindow(lineRunes, column)

	lineLabel := strconv.Itoa(pos.Line)
	gutterPad := strings.Repeat(" ", len(lineLabel))
	caretPad := strings.Repeat(" ", displayColumn-1)

	return fmt.Sprintf(
		"  --> line %d, column %d\n %s | %s\n %s | %s^",
		pos.Line,
		column,
		lineLabel,
		displayText,
		gutterPad,
		caretPad,
	)
}

func codeFrameLineWindow(lineRunes []rune, column int) (string, int) {
	if len(lineRunes) <= maxCodeFrameLineRunes {
		return string(lineRunes), column
	}
	caretIndex := column - 1
	start := caretIndex - maxCodeFrameLineRunes/2
	if start < 0 {
		start = 0
	}
	if start+maxCodeFrameLineRunes > len(lineRunes) {
		start = len(lineRunes) - maxCodeFrameLineRunes
	}
	end := start + maxCodeFrameLineRunes
	displayColumn := caretIndex - start + 1
	display := string(lineRunes[start:end])
	if start > 0 {
		display = "..." + display
		displayColumn += 3
	}
	if end < len(lineRunes) {
		display += "..."
	}
	return display, displayColumn
}
