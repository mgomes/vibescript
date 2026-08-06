package runtime

import (
	"context"
	"testing"
)

// TestClassBodyClosureKeepsItsConstantBoundaryAcrossCalls pins that the
// class-body marker survives the clones an escaping closure goes through.
//
// A constant-shaped identifier resolves by walking out of its scope and stops
// at the class body that encloses it, so the class constant wins over any
// same-named outer local. Neither the host clone nor the inbound rebind
// carried that marker, so a closure created in a class body and passed back
// into a later call walked past its own class into the rebound outer frames
// and returned a local derived from a host global instead (#24).
//
// Snippet mode with an executable top level is what puts an outer local in
// that path: it defers the class body into the synthesized entrypoint, so the
// body runs in a scope that holds the top-level binding.
func TestClassBodyClosureKeepsItsConstantBoundaryAcrossCalls(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`SECRET = leaked
class Vault
  SECRET = "class-constant"
  HOLDER = -> { SECRET }
end
Vault::HOLDER`, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	opts := CallOptions{Globals: map[string]Value{"leaked": NewString("host-secret")}}

	holder, err := script.Call(context.Background(), "__main__", nil, opts)
	if err != nil {
		t.Fatalf("building the closure failed: %v", err)
	}

	caller, err := engine.CompileSnippet("def invoke(fn: function)\n  fn.call()\nend", "__unused__")
	if err != nil {
		t.Fatalf("compile caller: %v", err)
	}
	got, err := caller.Call(context.Background(), "invoke", []Value{holder}, opts)
	if err != nil {
		t.Fatalf("re-entry failed: %v", err)
	}
	if got.Kind() != KindString || got.String() != "class-constant" {
		t.Fatalf("re-entered class-body closure resolved %#v, want the class constant", got)
	}
}
