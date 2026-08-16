package runtime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// propertyContractCloneSource builds a class whose single property carries a
// wide shape type and whose methods all take that property as an unannotated
// ivar parameter, so every one of them resolves to the same contract node.
func propertyContractCloneSource(fields, methods int) string {
	var b strings.Builder
	b.WriteString("class Big\n  property x: { ")
	for i := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "field_number_%06d: int", i)
	}
	b.WriteString(" }\n")
	for i := range methods {
		fmt.Fprintf(&b, "  def m%06d(@x)\n  end\n", i)
	}
	b.WriteString("end\n\ndef build\n  Big\nend\n")
	return b.String()
}

// TestHostClonedIvarParamsShareOnePropertyContract pins that host-cloning a
// class copies the property contract once for the whole class instead of once
// per parameter that names it. The contract is a single node declared by the
// generated accessor, so a per-param copy turned a type the source spells once
// into method_count copies of it during cloneValueForHost — after execution,
// where no quota can see it (#16). The one copy must still be the clone's own:
// pointing the parameters back at the compiled script would make a caller that
// edits what it was handed edit the script every later call runs.
func TestHostClonedIvarParamsShareOnePropertyContract(t *testing.T) {
	t.Parallel()

	const methods = 8
	script := compileScriptDefault(t, propertyContractCloneSource(4, methods))

	compiled, ok := script.classes["Big"]
	if !ok {
		t.Fatal("class Big is not compiled")
	}
	source := compiled.Methods["m000000"].Params[0].PropertyType
	if source == nil {
		t.Fatal("m000000's ivar param resolved no property contract; the test cannot observe sharing")
	}
	if len(source.Shape) != 4 {
		t.Fatalf("property contract shape has %d fields, want 4", len(source.Shape))
	}

	cloned := valueClass(callScript(t, context.Background(), script, "build", nil, CallOptions{}))
	if cloned == nil {
		t.Fatal("build did not return a class")
	}
	var shared *TypeExpr
	for i := range methods {
		name := fmt.Sprintf("m%06d", i)
		method, ok := cloned.Methods[name]
		if !ok {
			t.Fatalf("cloned class is missing method %s", name)
		}
		got := method.Params[0].PropertyType
		switch {
		case got == nil:
			t.Fatalf("cloned %s lost its property contract", name)
		case got == source:
			t.Fatalf("cloned %s param property contract is the compiled script's own node; the clone is not detached", name)
		case shared == nil:
			shared = got
		case got != shared:
			t.Fatalf("cloned %s param property contract = %p, want the one copy %p the class already made", name, got, shared)
		}
	}
	if len(shared.Shape) != len(source.Shape) {
		t.Fatalf("cloned contract shape has %d fields, want %d", len(shared.Shape), len(source.Shape))
	}
}

// TestClassSnapshotDetachesPropertyContracts pins the same detachment for the
// snapshot Classes() hands out. Sharing the compiled node with the snapshot
// made a caller's edit to a returned contract land on the accessor every later
// call resolves, and race with calls already running (#16).
func TestClassSnapshotDetachesPropertyContracts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, propertyContractCloneSource(4, 2))
	source := script.classes["Big"].Methods["m000000"].Params[0].PropertyType
	if source == nil {
		t.Fatal("m000000's ivar param resolved no property contract")
	}

	var snapshot *ClassDef
	for _, classDef := range script.Classes() {
		if classDef.Name == "Big" {
			snapshot = classDef
		}
	}
	if snapshot == nil {
		t.Fatal("Classes() did not return Big")
	}

	first := snapshot.Methods["m000000"].Params[0].PropertyType
	second := snapshot.Methods["m000001"].Params[0].PropertyType
	if first == nil || second == nil {
		t.Fatal("the snapshot lost its property contracts")
	}
	if first != second {
		t.Fatalf("the snapshot copied the contract per parameter: %p and %p", first, second)
	}
	if first == source {
		t.Fatal("the snapshot handed out the compiled script's own contract node")
	}

	// Editing what the caller was handed must not reach the script.
	first.Nullable = !first.Nullable
	if source.Nullable == first.Nullable {
		t.Fatal("editing the snapshot's contract changed the compiled script's")
	}
}

// TestHostClonedPropertyContractDoesNotScaleWithMethodCount pins the memory
// consequence of the sharing above: the host clone of a class must cost the
// same whether one method or many reference the same wide property type.
// Before the fix a 38KB script with a 1000-field property and 500 ivar-param
// methods retained 80MB of host clone; sharing holds it at 0.5MB.
func TestHostClonedPropertyContractDoesNotScaleWithMethodCount(t *testing.T) {
	hostCloneBytes := func(methods int) uint64 {
		script := compileScriptWithConfig(t, Config{}, propertyContractCloneSource(400, methods))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result, err := script.Call(context.Background(), "build", nil, CallOptions{})
		if err != nil {
			t.Fatalf("call with %d methods failed: %v", methods, err)
		}
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(result)
		return after.TotalAlloc - before.TotalAlloc
	}

	one := hostCloneBytes(1)
	many := hostCloneBytes(128)
	// Cloning 128 method bodies costs more than cloning one, but only by the
	// source those methods actually spell. A per-param copy of the property
	// type instead makes the clone scale with methods * type size, which for
	// these dimensions measured ~35x the single-method clone.
	if limit := 4 * one; many > limit {
		t.Fatalf("host clone allocated %d bytes for 128 ivar-param methods, want at most %d (%d bytes for one method)", many, limit, one)
	}
	t.Logf("host clone allocated %d bytes for one ivar-param method and %d for 128", one, many)
}

// TestOrdinaryClassCrossesTheHostBoundaryIntact pins that sharing the contract
// leaves a normal class alone: a default-profile call still returns methods
// whose ivar params carry the contract, and the contract still shapes the
// values behind them, keeping a bare zero-arity callable un-invoked.
func TestOrdinaryClassCrossesTheHostBoundaryIntact(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def five
  5
end

class Holder
  property cb: function
  property label: string

  def initialize(@cb = five, @label = "held")
  end

  def invoke
    @cb.call
  end
end

def summary
  holder = Holder.new
  [holder.invoke, holder.label]
end

def build
  Holder.new
end
`)

	got := callScript(t, context.Background(), script, "summary", nil, CallOptions{}).Array()
	want := []Value{NewInt(5), NewString("held")}
	if len(got) != len(want) {
		t.Fatalf("summary returned %d values, want %d", len(got), len(want))
	}
	for i, w := range want {
		if !got[i].Equal(w) {
			t.Fatalf("summary[%d] = %v, want %v", i, got[i], w)
		}
	}

	inst := valueInstance(callScript(t, context.Background(), script, "build", nil, CallOptions{}))
	if inst == nil {
		t.Fatal("build did not return an instance")
	}
	if label, ok := inst.Ivars["label"]; !ok || !label.Equal(NewString("held")) {
		t.Fatalf("cloned instance @label = %v, want \"held\"", label)
	}
	contract := inst.Class.Methods["initialize"].Params[0].PropertyType
	if contract == nil || contract.Kind != TypeFunction {
		t.Fatalf("cloned initialize's @cb param property contract = %v, want the function type", contract)
	}
}

// widePropertyCloneSource builds one class holding a wide property type and
// many methods whose `@x` parameters resolve to it. A property contract is
// resolved once and shared by every parameter that names it, so the whole
// declaration reaches one annotation node however many methods name it.
func widePropertyCloneSource(methods, fields int, returnClass bool) string {
	var b strings.Builder
	b.WriteString("class Holder\n  property x: { ")
	for i := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "field_number_%06d: int", i)
	}
	b.WriteString(" }\n")
	for i := range methods {
		fmt.Fprintf(&b, "  def helper_%04d(@x)\n  end\n", i)
	}
	b.WriteString("end\n\ndef build\n")
	if returnClass {
		b.WriteString("  Holder\n")
	} else {
		b.WriteString("  1\n")
	}
	b.WriteString("end\n")
	return b.String()
}

// TestHostClonedClassSharesOnePropertyContract pins that the per-clone memo
// spans the whole returned graph rather than each function in it. A property
// contract is resolved once, so N methods naming it reach one annotation node;
// a memo scoped per function would copy a wide property once per method and
// put the blowup back. 200 parameters naming one 400-field property retained
// 34MB of host clone from a 19KB script and retain 0.3MB now (#16).
func TestHostClonedClassSharesOnePropertyContract(t *testing.T) {
	t.Parallel()

	const methods = 8
	script := compileScriptDefault(t, widePropertyCloneSource(methods, 4, true))
	compiledContract := script.classes["Holder"].Methods["helper_0000"].Params[0].PropertyType
	if compiledContract == nil {
		t.Fatal("the helper resolved no property contract")
	}

	returned := callScript(t, context.Background(), script, "build", nil, CallOptions{})
	classDef := valueClass(returned)
	if classDef == nil {
		t.Fatal("build did not return a class")
	}

	var contract *TypeExpr
	accessorType := classDef.Methods["x="].Params[0].Type
	if accessorType == nil {
		t.Fatal("the cloned class lost its accessor annotation")
	}
	for i := range methods {
		name := fmt.Sprintf("helper_%04d", i)
		got := classDef.Methods[name].Params[0].PropertyType
		if got == nil {
			t.Fatalf("%s lost its property type", name)
		}
		if got == compiledContract {
			t.Fatalf("%s carries the compiled script's own contract node", name)
		}
		if contract == nil {
			contract = got
			continue
		}
		if got != contract {
			t.Fatalf("%s has its own copy %p of the contract; the clone already made %p", name, got, contract)
		}
	}
	if accessorType != contract {
		t.Fatalf("the accessor annotation %p is a separate copy of the contract %p", accessorType, contract)
	}
}

// TestClassSnapshotSharesPropertyContractsAcrossMethods pins the same for the
// Classes() snapshot, whose memo has to span the whole call and not one
// function.
func TestClassSnapshotSharesPropertyContractsAcrossMethods(t *testing.T) {
	t.Parallel()

	const methods = 4
	script := compileScriptDefault(t, widePropertyCloneSource(methods, 4, false))
	compiledContract := script.classes["Holder"].Methods["helper_0000"].Params[0].PropertyType

	var contract *TypeExpr
	seen := 0
	for _, classDef := range script.Classes() {
		if classDef.Name != "Holder" {
			continue
		}
		for i := range methods {
			name := fmt.Sprintf("helper_%04d", i)
			seen++
			got := classDef.Methods[name].Params[0].PropertyType
			if got == nil {
				t.Fatalf("%s lost its property contract", name)
			}
			if got == compiledContract {
				t.Fatalf("%s carries the compiled script's own contract node", name)
			}
			if contract == nil {
				contract = got
			} else if got != contract {
				t.Fatalf("%s has its own copy %p of the contract; the snapshot already made %p", name, got, contract)
			}
		}
	}
	if seen != methods {
		t.Fatalf("the snapshot returned %d annotated methods, want %d", seen, methods)
	}
}

// TestHostClonedContractDoesNotScaleWithMethodCount pins the memory the
// sharing above buys: returning a class whose methods all name one wide
// property must not retain a copy of that property per method.
func TestHostClonedContractDoesNotScaleWithMethodCount(t *testing.T) {
	hostCloneBytes := func(methods int) uint64 {
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited},
			widePropertyCloneSource(methods, 400, true))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result, err := script.Call(context.Background(), "build", nil, CallOptions{})
		if err != nil {
			t.Fatalf("call with %d methods failed: %v", methods, err)
		}
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(result)
		return after.TotalAlloc - before.TotalAlloc
	}

	few := hostCloneBytes(2)
	many := hostCloneBytes(128)
	// 64 times the methods cost 64 more function clones, which is real but
	// small and fixed per method; what must not scale is the property type
	// they all name. Unmemoized this measured ~43x the two-method clone
	// against the ~4x the per-method structure alone accounts for.
	if limit := 8 * few; many > limit {
		t.Fatalf("host clone allocated %d bytes for 128 annotated methods, want at most %d (%d bytes for two)", many, limit, few)
	}
	t.Logf("host clone allocated %d bytes for two annotated methods and %d for 128", few, many)
}
