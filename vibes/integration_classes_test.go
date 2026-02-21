package vibes

import (
	"context"
	"strings"
	"testing"
)

func TestClassPrivacyEnforced(t *testing.T) {
	script := compileTestProgram(t, "classes/privacy.vibe")
	_, err := script.Call(context.Background(), "violate", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected privacy violation")
	}
	if !strings.Contains(err.Error(), "private method secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClassErrorCases(t *testing.T) {
	script := compileTestProgram(t, "errors/classes.vibe")

	checkErr := func(fn, contains string) {
		t.Helper()
		_, err := script.Call(context.Background(), fn, nil, CallOptions{})
		if err == nil {
			t.Fatalf("%s: expected error", fn)
		}
		if !strings.Contains(err.Error(), contains) {
			t.Fatalf("%s: unexpected error '%v', want '%s'", fn, err, contains)
		}
	}

	checkErr("undefined_method", "unknown")
	checkErr("private_method_external", "private method")
	checkErr("write_to_readonly", "read-only property")
	checkErr("wrong_init_args", "argument")

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
