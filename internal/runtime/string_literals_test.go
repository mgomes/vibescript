package runtime

import (
	"context"
	"testing"
)

func TestSingleQuotedStringLiteralExecution(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run
  'hello'
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.String() != "hello" {
		t.Fatalf("Call(run) = %q, want %q", got.String(), "hello")
	}
}

func TestSingleQuotedStringEscapes(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def quote
  'don\'t'
end

def newline_text
  'a\nb'
end`)

	quote := callScript(t, context.Background(), script, "quote", nil, CallOptions{})
	if quote.String() != "don't" {
		t.Fatalf("Call(quote) = %q, want %q", quote.String(), "don't")
	}
	newlineText := callScript(t, context.Background(), script, "newline_text", nil, CallOptions{})
	if newlineText.String() != `a\nb` {
		t.Fatalf("Call(newline_text) = %q, want %q", newlineText.String(), `a\nb`)
	}
}

func TestDoubleQuotedStringInterpolationExecution(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def interpolated
  name = "Ada"
  score = 2
  "hi #{name}, score #{score + 1}, active #{true}"
end

def escaped_marker
  name = "Ada"
  "\#{name}"
end

def single_quoted_marker
  name = "Ada"
  'hi #{name}'
end`)

	got := callScript(t, context.Background(), script, "interpolated", nil, CallOptions{})
	if got.String() != "hi Ada, score 3, active true" {
		t.Fatalf("Call(interpolated) = %q, want %q", got.String(), "hi Ada, score 3, active true")
	}
	escaped := callScript(t, context.Background(), script, "escaped_marker", nil, CallOptions{})
	if escaped.String() != "#{name}" {
		t.Fatalf("Call(escaped_marker) = %q, want %q", escaped.String(), "#{name}")
	}
	singleQuoted := callScript(t, context.Background(), script, "single_quoted_marker", nil, CallOptions{})
	if singleQuoted.String() != "hi #{name}" {
		t.Fatalf("Call(single_quoted_marker) = %q, want %q", singleQuoted.String(), "hi #{name}")
	}
}
