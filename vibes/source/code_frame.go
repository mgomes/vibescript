package source

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatCodeFrame returns a human-readable source snippet highlighting
// the column at the given position. It returns the empty string when
// no useful frame can be produced (missing source, out-of-range line,
// etc.).
func FormatCodeFrame(source string, pos Position) string {
	if source == "" || pos.Line <= 0 {
		return ""
	}

	lines := strings.Split(source, "\n")
	if pos.Line > len(lines) {
		return ""
	}

	lineText := lines[pos.Line-1]
	lineRunes := []rune(lineText)

	column := pos.Column
	if column <= 0 {
		column = 1
	}
	if column > len(lineRunes)+1 {
		column = len(lineRunes) + 1
	}

	lineLabel := strconv.Itoa(pos.Line)
	gutterPad := strings.Repeat(" ", len(lineLabel))
	caretPad := strings.Repeat(" ", column-1)

	return fmt.Sprintf(
		"  --> line %d, column %d\n %s | %s\n %s | %s^",
		pos.Line,
		column,
		lineLabel,
		lineText,
		gutterPad,
		caretPad,
	)
}
