package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/source"
)

type snippetSourceMap struct {
	syntheticFunction     string
	displayFunction       string
	lineOffset            int
	firstLineColumnOffset int
}

type snippetCompileError struct {
	cause    error
	rendered string
}

func (e *snippetCompileError) Error() string {
	return e.rendered
}

func (e *snippetCompileError) Unwrap() error {
	return e.cause
}

func remapSnippetCompileError(err error, snippet string, sourceMap snippetSourceMap) error {
	issues := vibes.ParseIssues(err)
	if len(issues) == 0 {
		return err
	}

	var b strings.Builder
	for i, issue := range issues {
		if i > 0 {
			b.WriteString("\n\n")
		}
		pos := sourceMap.remapPosition(issue.Pos, snippet)
		message := issue.Message
		if sourceMap.isSyntheticEndPosition(issue.Pos, snippet) && strings.Contains(message, "unexpected token 'end'") {
			message = "unexpected end of snippet"
		}
		if strings.Contains(message, "end of input") {
			message = "unexpected end of snippet"
		}
		fmt.Fprintf(&b, "parse error at %d:%d: %s", pos.Line, pos.Column, message)
		if frame := source.FormatCodeFrame(snippet, pos); frame != "" {
			b.WriteString("\n")
			b.WriteString(frame)
		}
	}
	return &snippetCompileError{cause: err, rendered: b.String()}
}

func remapSnippetRuntimeError(err error, snippet string, sourceMap snippetSourceMap) error {
	var runtimeErr *vibes.RuntimeError
	if !errors.As(err, &runtimeErr) {
		return err
	}

	remapped := *runtimeErr
	remapped.Frames = make([]vibes.StackFrame, len(runtimeErr.Frames))
	for i, frame := range runtimeErr.Frames {
		if sourceMap.shouldRemapFrame(frame) {
			frame.Pos = sourceMap.remapPosition(frame.Pos, snippet)
		}
		if frame.Function == sourceMap.syntheticFunction {
			frame.Function = sourceMap.displayFunction
		}
		remapped.Frames[i] = frame
	}
	if len(runtimeErr.Frames) > 0 && sourceMap.shouldRemapFrame(runtimeErr.Frames[0]) {
		remapped.CodeFrame = source.FormatCodeFrame(snippet, remapped.Frames[0].Pos)
	} else {
		remapped.CodeFrame = runtimeErr.CodeFrame
	}
	return &remapped
}

func (m snippetSourceMap) shouldRemapFrame(frame vibes.StackFrame) bool {
	return frame.Source == ""
}

func (m snippetSourceMap) isSyntheticEndPosition(pos source.Position, snippet string) bool {
	return pos.Line > m.lineOffset+snippetLineCount(snippet)
}

func (m snippetSourceMap) remapPosition(pos source.Position, snippet string) source.Position {
	if pos.Line <= 0 {
		return pos
	}

	line := pos.Line - m.lineOffset
	if line < 1 {
		line = 1
	}
	lineCount := snippetLineCount(snippet)
	if line > lineCount {
		return snippetEOFPosition(snippet)
	}

	column := pos.Column
	if line == 1 {
		column -= m.firstLineColumnOffset
	}
	if column < 1 {
		column = 1
	}
	return source.Position{Line: line, Column: column}
}

func snippetLineCount(snippet string) int {
	return len(strings.Split(snippet, "\n"))
}

func snippetEOFPosition(snippet string) source.Position {
	lines := strings.Split(snippet, "\n")
	if len(lines) == 0 {
		return source.Position{Line: 1, Column: 1}
	}
	last := lines[len(lines)-1]
	return source.Position{Line: len(lines), Column: len([]rune(last)) + 1}
}
