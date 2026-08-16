package runtime

import (
	"context"
	"strings"
	"testing"
)

// bindRecordingCapability records whether the runtime ever called its Bind.
type bindRecordingCapability struct{ bound *bool }

func (c bindRecordingCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	*c.bound = true
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"noop": NewBuiltin("cap.noop", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				return NewNil(), nil
			}),
		}),
	}, nil
}

// TestOverQuotaCallRefusesBeforeBindingCapabilities pins that a call whose own
// setup already exceeds the memory quota is refused before any host adapter
// runs.
//
// A call builds its root env and clones the script's classes and enums before
// its Execution exists, and a definition-heavy script can exhaust a small quota
// on that alone. The refusal is certain at that point -- nothing later reduces a
// reachable graph -- so running an adapter's Bind first executes host code on a
// call the runtime has already decided to fail. Bind is arbitrary host code that
// may open connections, take locks, or block indefinitely.
//
// The ordering existed on master as part of the memory chain, justified as
// publishing this level to its ancestors before blocking. That justification
// went with the chain; this one does not depend on it, which is why the check
// stays and why it needs a test of its own now that the chain's has gone.
func TestOverQuotaCallRefusesBeforeBindingCapabilities(t *testing.T) {
	t.Parallel()

	var source strings.Builder
	for i := range 300 {
		source.WriteString("class Padding")
		source.WriteString(string(rune('A' + i%26)))
		source.WriteString(string(rune('a' + (i/26)%26)))
		source.WriteString("\n  CONST = \"")
		source.WriteString(strings.Repeat("x", 200))
		source.WriteString("\"\nend\n\n")
	}
	source.WriteString("def run()\n  1\nend\n")

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 << 10}, source.String())

	bound := false
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{bindRecordingCapability{bound: &bound}},
	})
	if err == nil {
		t.Fatal("a call whose setup exceeds the memory quota must fail")
	}
	requireErrorContains(t, err, "memory quota exceeded")
	if bound {
		t.Fatal("a capability adapter was bound on a call the runtime had already decided to refuse")
	}
}
