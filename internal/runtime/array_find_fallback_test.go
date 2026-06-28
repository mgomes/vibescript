package runtime

import (
	"context"
	"testing"
)

func TestArrayFindOptionalFallbackCallable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def fallback(unused = nil)
  :none
end

def exploding_fallback(unused = nil)
  raise "fallback should not run"
end

def miss_with_fallback
  [1, 2].find(fallback) { |x| x > 3 }
end

def miss_without_fallback
  [1, 2].find { |x| x > 3 }
end

def miss_with_nil_fallback
  maybe = nil
  [1, 2].find(maybe) { |x| x > 3 }
end

def hit_ignores_fallback
  [1, 2].find(exploding_fallback) { |x| x == 2 }
end

def raw_fallback
  [1, 2].find("none") { |x| x > 3 }
end`)

	if got := callScript(t, context.Background(), script, "miss_with_fallback", nil, CallOptions{}); !got.Equal(NewSymbol("none")) {
		t.Fatalf("miss_with_fallback = %#v, want :none", got)
	}
	if got := callScript(t, context.Background(), script, "miss_without_fallback", nil, CallOptions{}); !got.Equal(NewNil()) {
		t.Fatalf("miss_without_fallback = %#v, want nil", got)
	}
	if got := callScript(t, context.Background(), script, "miss_with_nil_fallback", nil, CallOptions{}); !got.Equal(NewNil()) {
		t.Fatalf("miss_with_nil_fallback = %#v, want nil", got)
	}
	if got := callScript(t, context.Background(), script, "hit_ignores_fallback", nil, CallOptions{}); !got.Equal(NewInt(2)) {
		t.Fatalf("hit_ignores_fallback = %#v, want 2", got)
	}
	requireCallErrorContains(t, script, "raw_fallback", nil, CallOptions{}, "attempted to call non-callable value")
}
