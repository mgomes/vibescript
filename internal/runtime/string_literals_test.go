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
