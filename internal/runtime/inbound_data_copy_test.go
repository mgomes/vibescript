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

// TestInboundDataFastPathIsolatesHostGlobals pins the same containment for a
// data-only global, which now binds lazily and deep-copies on first read.
func TestInboundDataFastPathIsolatesHostGlobals(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate()
  settings[:flags] << "added"
  size = settings[:flags].size
  settings.merge!({ touched: true })
  size
end`)

	flags := []Value{NewString("existing")}
	settings := map[string]Value{"flags": NewArray(flags)}

	result := callScript(t, context.Background(), script, "mutate", nil,
		CallOptions{Globals: map[string]Value{"settings": NewHash(settings)}})
	if !result.Equal(NewInt(2)) {
		t.Fatalf("mutate returned %v, want 2", result)
	}

	if got := len(settings); got != 1 {
		t.Fatalf("host settings map has %d entries after script merge!, want 1", got)
	}
	arr, ok := settings["flags"]
	if !ok || len(arr.Array()) != 1 {
		t.Fatalf("host flags = %v after script push, want the original single entry", arr)
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

// TestInboundGlobalAliasingArgumentSharesClone pins the cross-set alias case:
// a lazily bound global whose source is the same composite as an argument
// must materialize to the argument's clone, exactly as the eager rebinder
// deduplicated it. This exercises the registering fast copy plus the deferred
// global scan.
func TestInboundGlobalAliasingArgumentSharesClone(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate(items)
  items << "from_arg"
  [shared.equal?(items), shared.size]
end`)

	shared := NewArray([]Value{NewString("seed")})
	result := callScript(t, context.Background(), script, "mutate",
		[]Value{shared},
		CallOptions{Globals: map[string]Value{"shared": shared}})
	got := result.Array()
	if len(got) != 2 || !got[0].Equal(NewBool(true)) || !got[1].Equal(NewInt(2)) {
		t.Fatalf("mutate returned %v, want [true, 2]", result)
	}
	if len(shared.Array()) != 1 {
		t.Fatalf("host array length = %d after script push, want 1", len(shared.Array()))
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

// TestLazyGlobalUnusedIsNeverMaterialized pins the lazy-global contract with
// the memory quota: a global too large for the quota no longer fails a call
// that never reads it, and still fails (identically to the eager behavior)
// once the script does read it.
func TestLazyGlobalUnusedIsNeverMaterialized(t *testing.T) {
	t.Parallel()

	rows := make([]Value, 512)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{
			"id":   NewInt(int64(i)),
			"name": NewString("payload-payload-payload-payload"),
		})
	}
	globals := map[string]Value{"big": NewArray(rows)}
	cfg := Config{MemoryQuotaBytes: 48 << 10}

	unused := compileScriptWithConfig(t, cfg, `def run()
  1
end`)
	result := callScript(t, context.Background(), unused, "run", nil, CallOptions{Globals: globals})
	if !result.Equal(NewInt(1)) {
		t.Fatalf("run with unused over-quota global = %v, want 1", result)
	}

	used := compileScriptWithConfig(t, cfg, `def run()
  big.size
end`)
	err := callScriptErr(t, context.Background(), used, "run", nil, CallOptions{Globals: globals})
	requireErrorContains(t, err, "memory quota")
}

// TestLazyGlobalReadsStayCoherentWithinCall pins that materialization happens
// once per call: mutations through the first read remain visible to later
// reads, matching the eager-bind behavior exactly.
func TestLazyGlobalReadsStayCoherentWithinCall(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def mutate()
  counters[:hits] = counters[:hits] + 1
  counters[:hits]
end`)

	counters := map[string]Value{"hits": NewInt(0)}
	opts := CallOptions{Globals: map[string]Value{"counters": NewHash(counters)}}

	first := callScript(t, context.Background(), script, "mutate", nil, opts)
	second := callScript(t, context.Background(), script, "mutate", nil, opts)
	if !first.Equal(NewInt(1)) || !second.Equal(NewInt(1)) {
		t.Fatalf("mutate calls returned %v then %v, want 1 and 1 (fresh clone per call)", first, second)
	}
	if !counters["hits"].Equal(NewInt(0)) {
		t.Fatalf("host counters mutated to %v, want 0", counters["hits"])
	}
}

// TestLazyGlobalStrictEffectsValidatesAtBind pins that StrictEffects
// validation still runs eagerly at Call entry: a callable buried in a global
// is rejected before the script runs, even though the global would never have
// been read.
func TestLazyGlobalStrictEffectsValidatesAtBind(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  1
end`)

	poison := NewHash(map[string]Value{
		"cb": NewBuiltin("host.cb", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewNil(), nil
		}),
	})
	err := callScriptErr(t, context.Background(), script, "run", nil,
		CallOptions{Globals: map[string]Value{"hooks": poison}})
	requireErrorContains(t, err, "strict effects: global hooks must be data-only")
}

// TestLazyGlobalStrictEffectsRejectsShapeValues pins that runtime shape
// values count as non-data at the strict-effects global boundary just like
// callables: their payload is an opaque type expression, not plain data.
func TestLazyGlobalStrictEffectsRejectsShapeValues(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  1
end`)

	poison := NewHash(map[string]Value{
		"schema": NewShape(&TypeExpr{Kind: TypeShape}),
	})
	err := callScriptErr(t, context.Background(), script, "run", nil,
		CallOptions{Globals: map[string]Value{"hooks": poison}})
	requireErrorContains(t, err, "strict effects: global hooks must be data-only")
}

// TestLazyGlobalInheritedByTasks pins the task boundary: a global the parent
// never reads is still inherited by tasks with the correct value, and a
// global the parent mutates before spawning is inherited in its mutated
// state. Host originals stay untouched in both cases.
func TestLazyGlobalInheritedByTasks(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def read_tag(item)
  meta[:tag]
end

def untouched(items)
  Tasks.map(items, max: 2, with: :read_tag)
end

def mutated_first(items)
  meta[:tag] = "parent"
  Tasks.map(items, max: 2, with: :read_tag)
end`)

	meta := map[string]Value{"tag": NewString("host")}
	items := NewArray([]Value{NewInt(1), NewInt(2)})

	result := callScript(t, context.Background(), script, "untouched",
		[]Value{items}, CallOptions{Globals: map[string]Value{"meta": NewHash(meta)}})
	for _, tag := range result.Array() {
		if !tag.Equal(NewString("host")) {
			t.Fatalf("task read unread parent global = %v, want host", tag)
		}
	}

	result = callScript(t, context.Background(), script, "mutated_first",
		[]Value{items}, CallOptions{Globals: map[string]Value{"meta": NewHash(meta)}})
	for _, tag := range result.Array() {
		if !tag.Equal(NewString("parent")) {
			t.Fatalf("task read parent-mutated global = %v, want parent", tag)
		}
	}
	if !meta["tag"].Equal(NewString("host")) {
		t.Fatalf("host global mutated to %v, want host", meta["tag"])
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
