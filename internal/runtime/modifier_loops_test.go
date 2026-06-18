package runtime

import (
	"context"
	"testing"
)

func TestModifierWhileLoop(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  i = 0
  i = i + 1 while i < 3
  i
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewInt(3)) {
		t.Fatalf("run() = %#v, want 3", got)
	}
}

func TestModifierUntilLoop(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  i = 0
  i = i + 1 until i >= 3
  i
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewInt(3)) {
		t.Fatalf("run() = %#v, want 3", got)
	}
}

func TestModifierIfConditional(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  value = 0
  value = 1 if true
  value = 2 if false
  value
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewInt(1)) {
		t.Fatalf("run() = %#v, want 1", got)
	}
}

func TestModifierUnlessConditional(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  value = 0
  value = 1 unless false
  value = 2 unless true
  value
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewInt(1)) {
		t.Fatalf("run() = %#v, want 1", got)
	}
}
