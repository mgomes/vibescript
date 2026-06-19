package runtime

import (
	"context"
	"testing"
)

func TestCompileSnippetInitializesClassBodyInSourceOrder(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`limit = 10

class Settings
  @@limit = limit

  def self.limit
    @@limit
  end
end

Settings.limit
`, "__eval__")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	got := callScript(t, context.Background(), script, "__eval__", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 10 {
		t.Fatalf("__eval__() = %#v, want 10", got)
	}
}
