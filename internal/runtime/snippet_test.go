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

func TestCompileSnippetInitializesDeclarationOnlyClassBodyBeforeNamedFunction(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`class Settings
  @@limit = 10

  def self.limit
    @@limit
  end
end

def run
  Settings.limit
end
`, "<script>")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}
	if len(script.deferredClassBodies) != 0 {
		t.Fatalf("declaration-only class body was deferred: %v", script.deferredClassBodies)
	}

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 10 {
		t.Fatalf("run() = %#v, want 10", got)
	}
}
