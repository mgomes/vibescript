package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// paddingScript builds a script whose class definitions alone are large enough
// to matter against a small quota, so a call can be pushed over its own budget
// by setup rather than by anything the script runs.
func paddingScript(classes int) string {
	var source strings.Builder
	for i := range classes {
		source.WriteString("class Padding")
		source.WriteString(string(rune('A' + i%26)))
		source.WriteString(string(rune('a' + (i/26)%26)))
		source.WriteString("\n  CONST = \"")
		source.WriteString(strings.Repeat("x", 200))
		source.WriteString("\"\nend\n\n")
	}
	source.WriteString("def run()\n  1\nend\n")
	return source.String()
}

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

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 << 10}, paddingScript(300))

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

// contractFloodCapability declares a large map of capability contracts and
// records whether its Bind ran afterwards. The contracts are host data the
// execution retains: capabilityContractsByName is charged per entry and per key
// length, so a provider can put the call over its quota by answering.
type contractFloodCapability struct {
	entries int
	bound   *bool
}

func (c contractFloodCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	*c.bound = true
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"noop": NewBuiltin("cap.noop", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				return NewNil(), nil
			}),
		}),
	}, nil
}

func (c contractFloodCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	out := make(map[string]CapabilityMethodContract, c.entries)
	for i := range c.entries {
		// Distinct keys matter: a colliding suffix silently collapses the map
		// and the charge with it, leaving a test that exceeds nothing.
		out[fmt.Sprintf("cap.%s%d", strings.Repeat("m", 200), i)] = CapabilityMethodContract{}
	}
	if len(out) != c.entries {
		panic("contract keys collided, so this fixture charges less than it claims")
	}
	return out
}

// TestContractProviderOverQuotaRefusesBeforeBind pins the second host callback
// in the binding loop: a provider that answers with enough contracts to put the
// execution over its quota must not then have its Bind called.
//
// The contracts a provider returns are host data with no bound on entry count or
// key length, and the execution retains them, so answering is itself an
// allocation this level is charged for. Once that charge exceeds the quota the
// call is certain to fail, and Bind is arbitrary host code.
func TestContractProviderOverQuotaRefusesBeforeBind(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 512 << 10}, paddingScript(20))

	bound := false
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{contractFloodCapability{entries: 4000, bound: &bound}},
	})
	if err == nil {
		t.Fatal("contracts large enough to exceed the quota must fail the call")
	}
	requireErrorContains(t, err, "memory quota exceeded")
	if bound {
		t.Fatal("Bind ran after the declared contracts had already put the call over its quota")
	}
}

// globalFloodCapability returns a large global, so the bindings one adapter
// leaves behind are charged to the execution before the next adapter runs.
type globalFloodCapability struct{ bytes int }

func (c globalFloodCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"payload": NewString(strings.Repeat("y", c.bytes)),
	}, nil
}

// TestEarlierAdapterOverQuotaRefusesBeforeNextAdapter pins the per-iteration
// half of the same rule: what one adapter retained on the execution is charged
// before the next adapter's callbacks run.
//
// A single check ahead of the loop cannot cover this. It is true when it runs
// and stale by the second iteration, because the first adapter's globals landed
// in between -- which is exactly the shape that made a publication at the top of
// the loop body insufficient on master.
func TestEarlierAdapterOverQuotaRefusesBeforeNextAdapter(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 512 << 10}, paddingScript(20))

	bound := false
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			globalFloodCapability{bytes: 1 << 20},
			bindRecordingCapability{bound: &bound},
		},
	})
	if err == nil {
		t.Fatal("a global large enough to exceed the quota must fail the call")
	}
	requireErrorContains(t, err, "memory quota exceeded")
	if bound {
		t.Fatal("the second adapter was bound after the first had already put the call over its quota")
	}
}
