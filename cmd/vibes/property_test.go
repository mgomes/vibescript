package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/parser"
	"github.com/mgomes/vibescript/vibes"
	"pgregory.net/rapid"
)

func TestRapidFormatGeneratedValidSourceIsIdempotent(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		source := drawRapidVibeProgram(rt)
		if _, parseErrors := parser.Parse(source); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(drawRapidVibeProgram()) errors = %v for source %q, want none", parseErrors, source)
		}

		formatted := formatVibeSource(source)
		if !strings.HasSuffix(formatted, "\n") {
			rt.Fatalf("formatVibeSource(%q) = %q, want trailing newline", source, formatted)
		}
		if strings.Contains(formatted, "\r") {
			rt.Fatalf("formatVibeSource(%q) = %q, want no carriage returns", source, formatted)
		}
		for lineNumber, line := range strings.Split(strings.TrimSuffix(formatted, "\n"), "\n") {
			if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
				rt.Fatalf("formatVibeSource(%q) line %d = %q, want no trailing horizontal whitespace", source, lineNumber+1, line)
			}
		}
		if second := formatVibeSource(formatted); second != formatted {
			rt.Fatalf("formatVibeSource(formatVibeSource(%q)) = %q, want %q", source, second, formatted)
		}
		if _, parseErrors := parser.Parse(formatted); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(formatVibeSource(%q)) errors = %v, want none", source, parseErrors)
		}

		engine, err := vibes.NewEngine(vibes.Config{})
		if err != nil {
			rt.Fatalf("vibes.NewEngine(vibes.Config{}) error = %v, want nil", err)
		}
		if _, err := engine.Compile(formatted); err != nil {
			rt.Fatalf("Compile(formatVibeSource(%q)) error = %v, want nil", source, err)
		}
	})
}

func drawRapidVibeProgram(rt *rapid.T) string {
	functionName := fmt.Sprintf("prop_%d", rapid.IntRange(0, 1_000_000).Draw(rt, "function suffix"))
	paramCount := rapid.IntRange(0, 3).Draw(rt, "parameter count")
	params := make([]string, paramCount)
	for i := range params {
		params[i] = fmt.Sprintf("p%d", i)
	}

	lines := []string{
		fmt.Sprintf("def %s(%s)%s", functionName, strings.Join(params, ", "), drawRapidTrailingWhitespace(rt)),
	}

	if len(params) > 0 && rapid.Bool().Draw(rt, "include assignment") {
		lines = append(lines, fmt.Sprintf("  local = %s%s", params[0], drawRapidTrailingWhitespace(rt)))
	}
	lines = append(lines, fmt.Sprintf("  return %s%s", drawRapidVibeExpression(rt, params, 0), drawRapidTrailingWhitespace(rt)))
	lines = append(lines, "end"+drawRapidTrailingWhitespace(rt))
	return strings.Join(lines, "\n") + "\n"
}

func drawRapidVibeExpression(rt *rapid.T, params []string, depth int) string {
	choices := []string{"nil", "bool", "int", "string"}
	if len(params) > 0 {
		choices = append(choices, "param")
	}
	if depth < 2 {
		choices = append(choices, "array", "hash", "binary")
	}

	switch rapid.SampledFrom(choices).Draw(rt, "expression kind") {
	case "nil":
		return "nil"
	case "bool":
		if rapid.Bool().Draw(rt, "bool literal") {
			return "true"
		}
		return "false"
	case "int":
		return strconv.Itoa(rapid.IntRange(-1000, 1000).Draw(rt, "int literal"))
	case "string":
		return strconv.Quote(rapid.StringMatching(`[A-Za-z0-9 _.\-]{0,24}`).Draw(rt, "string literal"))
	case "param":
		return rapid.SampledFrom(params).Draw(rt, "param reference")
	case "array":
		size := rapid.IntRange(1, 4).Draw(rt, "array size")
		items := make([]string, size)
		for i := range items {
			items[i] = drawRapidVibeExpression(rt, params, depth+1)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case "hash":
		size := rapid.IntRange(1, 4).Draw(rt, "hash size")
		pairs := make([]string, size)
		for i := range pairs {
			pairs[i] = fmt.Sprintf("k%d: %s", i, drawRapidVibeExpression(rt, params, depth+1))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	default:
		left := rapid.IntRange(-1000, 1000).Draw(rt, "left int")
		right := rapid.IntRange(-1000, 1000).Draw(rt, "right int")
		return fmt.Sprintf("(%d + %d)", left, right)
	}
}

func drawRapidTrailingWhitespace(rt *rapid.T) string {
	spaces := strings.Repeat(" ", rapid.IntRange(0, 3).Draw(rt, "trailing spaces"))
	tabs := strings.Repeat("\t", rapid.IntRange(0, 1).Draw(rt, "trailing tabs"))
	return spaces + tabs
}
