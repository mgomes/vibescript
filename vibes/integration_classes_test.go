package vibes

import (
	"context"
	"testing"
)

func TestClassPrivacyEnforced(t *testing.T) {
	script := compileTestProgram(t, "classes/privacy.vibe")
	requireCallErrorContains(t, script, "violate", nil, CallOptions{}, "private method secret")
}

func TestClassErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/classes.vibe")

	requireCallErrorContains(t, script, "undefined_method", nil, CallOptions{}, "unknown")
	requireCallErrorContains(t, script, "private_method_external", nil, CallOptions{}, "private method")
	requireCallErrorContains(t, script, "write_to_readonly", nil, CallOptions{}, "read-only property")
	requireCallErrorContains(t, script, "wrong_init_args", nil, CallOptions{}, "argument")

	// run function should work
	val, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}
	if val.Kind() != KindHash {
		t.Fatalf("run: expected hash, got %v", val.Kind())
	}
	h := val.Hash()
	if h["counter"].Int() != 7 {
		t.Fatalf("run: counter mismatch: %v", h["counter"])
	}
	if h["readonly"].String() != "hello" {
		t.Fatalf("run: readonly mismatch: %v", h["readonly"])
	}
	if h["writeonly"].Int() != 99 {
		t.Fatalf("run: writeonly mismatch: %v", h["writeonly"])
	}
}
