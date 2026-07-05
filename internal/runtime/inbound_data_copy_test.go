package runtime

import (
	"context"
	"testing"
)

// TestInboundDataFastPathIsolatesHostArguments pins that a data-only argument
// graph — which takes the tight-copy fast path instead of the rebinder walk —
// is still a full deep copy: script-side in-place mutators write into the
// call's clones, never into host memory.
func TestInboundDataFastPathIsolatesHostArguments(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate(rows, config)
  rows << { id: 99 }
  rows[0][:name] = "changed"
  rows.map! { |row| row }
  config[:limits].store("max", 10)
  config.merge!({ extra: true })
  [rows.size, config.size]
end`)

	row := map[string]Value{"id": NewInt(1), "name": NewString("original")}
	rows := NewArray([]Value{NewHash(row)})
	limits := map[string]Value{"min": NewInt(0)}
	config := map[string]Value{"limits": NewHash(limits)}

	result := callScript(t, context.Background(), script, "mutate",
		[]Value{rows, NewHash(config)}, CallOptions{})
	sizes := result.Array()
	if len(sizes) != 2 || !sizes[0].Equal(NewInt(2)) || !sizes[1].Equal(NewInt(2)) {
		t.Fatalf("mutate returned %v, want [2, 2]", result)
	}

	if got := len(rows.Array()); got != 1 {
		t.Fatalf("host rows length = %d after script push, want 1", got)
	}
	if !row["name"].Equal(NewString("original")) {
		t.Fatalf("host row name = %v after script index write, want original", row["name"])
	}
	if _, ok := limits["max"]; ok {
		t.Fatalf("script store leaked into host limits map: %v", limits)
	}
	if _, ok := config["extra"]; ok {
		t.Fatalf("script merge! leaked into host config map: %v", config)
	}
}

// TestInboundAliasedArgumentsShareOneClone pins that a repeated composite in
// the argument list disables the fast path and keeps the rebinder's alias
// semantics: both arguments rebind to one shared clone, so an in-place
// mutation through one stays visible through the other — while the host
// original stays untouched.
func TestInboundAliasedArgumentsShareOneClone(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate(a, b)
  a << 4
  [a.equal?(b), b.size]
end`)

	shared := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	result := callScript(t, context.Background(), script, "mutate",
		[]Value{shared, shared}, CallOptions{})
	got := result.Array()
	if len(got) != 2 || !got[0].Equal(NewBool(true)) || !got[1].Equal(NewInt(4)) {
		t.Fatalf("mutate returned %v, want [true, 4]", result)
	}
	if len(shared.Array()) != 3 {
		t.Fatalf("host array length = %d after script push, want 3", len(shared.Array()))
	}
}

// TestInboundSharedEntryMapAliasingPreserved pins that two distinct host hash
// wrappers sharing one mutable entry map still rebind onto one cloned entry
// map (the rebinder's seenHashEntries contract): the scan detects the shared
// map as a repeat and routes the call through the slow path.
func TestInboundSharedEntryMapAliasingPreserved(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate(a, b)
  a[:x] = 1
  b[:x]
end`)

	entries := map[string]Value{}
	a := NewHash(entries)
	b := NewHash(entries)
	result := callScript(t, context.Background(), script, "mutate",
		[]Value{a, b}, CallOptions{})
	if !result.Equal(NewInt(1)) {
		t.Fatalf("write through one wrapper = %v through the other, want 1", result)
	}
	if len(entries) != 0 {
		t.Fatalf("host entry map gained %d entries from script write, want 0", len(entries))
	}
}

// TestInboundHashDefaultMetadataTakesSlowPath pins that a host hash carrying
// Ruby-style default metadata is excluded from the fast path and keeps its
// missing-key behavior through the rebinder.
func TestInboundHashDefaultMetadataTakesSlowPath(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def read(h)
  h[:missing]
end`)

	withDefault := NewHashWithDefault(map[string]Value{}, NewString("fallback"), NewNil())
	result := callScript(t, context.Background(), script, "read",
		[]Value{withDefault}, CallOptions{})
	if !result.Equal(NewString("fallback")) {
		t.Fatalf("missing-key read = %v, want fallback", result)
	}
}

// TestInboundBuriedForwardedCapabilityStillRevoked pins that a callable buried
// deep inside an otherwise data-shaped argument graph disables the fast path
// for the whole call, so the inbound rebinder still revokes a captured
// capability grant on re-entry.
func TestInboundBuriedForwardedCapabilityStillRevoked(t *testing.T) {
	t.Parallel()

	stub := &jobQueueStub{}
	script := compileScriptDefault(t, `
def id(&b)
  b
end

def make()
  id(&jobs.enqueue)
end

def use(payload)
  payload[:rows][0][:cb].call("demo", { key: "k" })
end
`)

	exported := callScript(t, context.Background(), script, "make", nil,
		callOptionsWithCapabilities(MustNewJobQueueCapability("jobs", stub)),
	)
	if exported.Kind() != KindBlock {
		t.Fatalf("make returned %v, want a block", exported.Kind())
	}

	payload := NewHash(map[string]Value{
		"rows": NewArray([]Value{
			NewHash(map[string]Value{
				"id": NewInt(1),
				"cb": exported,
			}),
		}),
	})
	err := callScriptErr(t, context.Background(), script, "use",
		[]Value{payload}, CallOptions{})
	requireErrorContains(t, err, "capability jobs.enqueue was not granted to this call")
	if len(stub.enqueueCalls) != 0 {
		t.Fatalf("capability invoked %d times from a call that granted no capabilities", len(stub.enqueueCalls))
	}
}

// TestFastPathPayloadIsolatedThroughCapabilityBoundary pins the script-to-host
// half of the boundary: a data-only host argument (fast-copied at entry) that
// the script forwards to a contracted capability is cloned again at the
// capability boundary, so the host-received attributes never alias the
// script's copy or the host's original argument.
func TestFastPathPayloadIsolatedThroughCapabilityBoundary(t *testing.T) {
	t.Parallel()

	stub := &dbCapabilityStub{}
	script := compileScriptDefault(t, `def run(payload)
  db.update("Players", "row-1", payload)
  payload[:rows][0][:score] = 99
  payload[:rows][0][:score]
end`)

	rowEntries := map[string]Value{"score": NewInt(1)}
	payload := map[string]Value{"rows": NewArray([]Value{NewHash(rowEntries)})}

	result := callScript(t, context.Background(), script, "run",
		[]Value{NewHash(payload)},
		callOptionsWithCapabilities(MustNewDBCapability("db", stub)))
	if !result.Equal(NewInt(99)) {
		t.Fatalf("run returned %v, want 99", result)
	}

	if len(stub.updateCalls) != 1 {
		t.Fatalf("db.update called %d times, want 1", len(stub.updateCalls))
	}
	attrs := stub.updateCalls[0].Attributes
	hostRow := attrs["rows"].Array()[0]
	if got, _, _ := hostRow.HashGet(NewSymbol("score")); !got.Equal(NewInt(1)) {
		t.Fatalf("host-received score = %v (script post-call mutation leaked), want 1", got)
	}
	if !rowEntries["score"].Equal(NewInt(1)) {
		t.Fatalf("host original score = %v, want 1", rowEntries["score"])
	}
}
