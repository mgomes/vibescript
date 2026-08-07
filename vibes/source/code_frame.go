package source

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxCodeFrameLineRunes = 160

// codeFrameCheckpointStride is how many lines separate the offsets the
// formatter retains. Splitting a source into one string header per line costs
// about sixteen bytes a line and is held for as long as the compiled script
// is, so a mostly-newline source near the size limit retained many times its
// own length -- multiplied by every module a long-lived engine caches.
// Recording every stride-th line start instead makes that a small fraction of
// the source, and a lookup scans at most one stride of lines.
const codeFrameCheckpointStride = 64

// CodeFrameFormatter renders code frames against an indexed source.
type CodeFrameFormatter struct {
	source      string
	checkpoints []int
	lineCount   int
}

// NewCodeFrameFormatter returns a reusable formatter for source.
func NewCodeFrameFormatter(source string) *CodeFrameFormatter {
	if source == "" {
		return &CodeFrameFormatter{}
	}
	f := &CodeFrameFormatter{
		source:      source,
		checkpoints: []int{0},
		lineCount:   1,
	}
	for i := 0; i < len(source); i++ {
		if source[i] != '\n' {
			continue
		}
		f.lineCount++
		if (f.lineCount-1)%codeFrameCheckpointStride == 0 {
			f.checkpoints = append(f.checkpoints, i+1)
		}
	}
	return f
}

// lineText returns the text of the given one-based line.
func (f *CodeFrameFormatter) lineText(line int) (string, bool) {
	if line <= 0 || line > f.lineCount {
		return "", false
	}
	offset := f.checkpoints[(line-1)/codeFrameCheckpointStride]
	for remaining := (line - 1) % codeFrameCheckpointStride; remaining > 0; remaining-- {
		next := strings.IndexByte(f.source[offset:], '\n')
		if next < 0 {
			return "", false
		}
		offset += next + 1
	}
	if end := strings.IndexByte(f.source[offset:], '\n'); end >= 0 {
		return f.source[offset : offset+end], true
	}
	return f.source[offset:], true
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
	if f == nil || pos.Line <= 0 {
		return ""
	}
	lineText, ok := f.lineText(pos.Line)
	if !ok {
		return ""
	}
	column := pos.Column
	if column <= 0 {
		column = 1
	}
	displayText, displayColumn, column := codeFrameLineWindow(lineText, column)

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

func codeFrameLineWindow(lineText string, column int) (string, int, int) {
	lineRunes := utf8.RuneCountInString(lineText)
	if column > lineRunes+1 {
		column = lineRunes + 1
	}
	if lineRunes <= maxCodeFrameLineRunes {
		return lineText, column, column
	}
	caretIndex := column - 1
	start := max(caretIndex-maxCodeFrameLineRunes/2, 0)
	if start+maxCodeFrameLineRunes > lineRunes {
		start = lineRunes - maxCodeFrameLineRunes
	}
	end := start + maxCodeFrameLineRunes
	displayColumn := caretIndex - start + 1
	display := lineText[byteOffsetForRuneIndex(lineText, start):byteOffsetForRuneIndex(lineText, end)]
	if start > 0 {
		display = "..." + display
		displayColumn += 3
	}
	if end < lineRunes {
		display += "..."
	}
	return display, displayColumn, column
}

func byteOffsetForRuneIndex(s string, target int) int {
	if target <= 0 {
		return 0
	}
	runes := 0
	for offset := range s {
		if runes == target {
			return offset
		}
		runes++
	}
	return len(s)
}
