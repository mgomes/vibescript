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

// mixinPropertyCloneSource builds one module holding a wide property type and
// many classes that include it. A mixed-in method is a shallow copy, so every
// including class reaches the module's own annotation nodes: one type the
// source spells once, reachable from as many classes as the source cares to
// declare.
func mixinPropertyCloneSource(classes, fields int, returnClasses bool) string {
	var b strings.Builder
	b.WriteString("module Wide\n  property x: { ")
	for i := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "field_number_%06d: int", i)
	}
	b.WriteString(" }\n  def helper(@x)\n  end\nend\n")
	for i := range classes {
		fmt.Fprintf(&b, "class Holder%04d\n  include Wide\nend\n", i)
	}
	b.WriteString("\ndef build\n")
	if !returnClasses {
		b.WriteString("  1\n")
	} else {
		b.WriteString("  [")
		for i := range classes {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "Holder%04d", i)
		}
		b.WriteString("]\n")
	}
	b.WriteString("end\n")
	return b.String()
}

// TestHostClonedMixinClassesShareOnePropertyContract pins that the per-clone
// memo spans the whole returned graph rather than each class in it. Including a
// module copies its methods by shallow copy, so N including classes reach one
// annotation node; a memo scoped per class would copy a wide module property
// once per include and put the blowup back, spelled with `include` instead of
// with methods. 200 classes including one 400-field property retained 34MB of
// host clone from a 19KB script and retain 0.3MB now (#16).
func TestHostClonedMixinClassesShareOnePropertyContract(t *testing.T) {
	t.Parallel()

	const classes = 8
	script := compileScriptDefault(t, mixinPropertyCloneSource(classes, 4, true))
	compiledContract := script.classes["Holder0000"].Methods["helper"].Params[0].PropertyType
	if compiledContract == nil {
		t.Fatal("the mixed-in helper resolved no property contract")
	}

	returned := callScript(t, context.Background(), script, "build", nil, CallOptions{}).Array()
	if len(returned) != classes {
		t.Fatalf("build returned %d classes, want %d", len(returned), classes)
	}

	var contract, accessorType *TypeExpr
	for i, val := range returned {
		classDef := valueClass(val)
		if classDef == nil {
			t.Fatalf("returned[%d] is not a class", i)
		}
		got := classDef.Methods["helper"].Params[0].PropertyType
		setter := classDef.Methods["x="].Params[0].Type
		if got == nil || setter == nil {
			t.Fatalf("returned[%d] lost its property types", i)
		}
		if got == compiledContract {
			t.Fatalf("returned[%d] carries the compiled script's own contract node", i)
		}
		if contract == nil {
			contract, accessorType = got, setter
			continue
		}
		if got != contract {
			t.Fatalf("returned[%d] has its own copy %p of the contract; the clone already made %p", i, got, contract)
		}
		if setter != accessorType {
			t.Fatalf("returned[%d] has its own copy %p of the accessor annotation; the clone already made %p", i, setter, accessorType)
		}
	}
}

// TestClassSnapshotSharesMixinContractsAcrossClasses pins the same for the
// Classes() snapshot, whose memo has to span the whole call and not one class.
func TestClassSnapshotSharesMixinContractsAcrossClasses(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, mixinPropertyCloneSource(4, 4, false))
	compiledContract := script.classes["Holder0000"].Methods["helper"].Params[0].PropertyType

	var contract *TypeExpr
	seen := 0
	for _, classDef := range script.Classes() {
		if !strings.HasPrefix(classDef.Name, "Holder") {
			continue
		}
		seen++
		got := classDef.Methods["helper"].Params[0].PropertyType
		if got == nil {
			t.Fatalf("%s lost its property contract", classDef.Name)
		}
		if got == compiledContract {
			t.Fatalf("%s carries the compiled script's own contract node", classDef.Name)
		}
		if contract == nil {
			contract = got
		} else if got != contract {
			t.Fatalf("%s has its own copy %p of the contract; the snapshot already made %p", classDef.Name, got, contract)
		}
	}
	if seen != 4 {
		t.Fatalf("the snapshot returned %d including classes, want 4", seen)
	}
}

// TestHostClonedMixinContractDoesNotScaleWithClassCount pins the memory the
// sharing above buys: returning many classes that include one wide-property
// module must not retain a copy of that property per class.
func TestHostClonedMixinContractDoesNotScaleWithClassCount(t *testing.T) {
	hostCloneBytes := func(classes int) uint64 {
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited},
			mixinPropertyCloneSource(classes, 400, true))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result, err := script.Call(context.Background(), "build", nil, CallOptions{})
		if err != nil {
			t.Fatalf("call with %d classes failed: %v", classes, err)
		}
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(result)
		return after.TotalAlloc - before.TotalAlloc
	}

	few := hostCloneBytes(2)
	many := hostCloneBytes(128)
	// 64 times the classes cost 64 more class clones, which is real but small
	// and fixed per class; what must not scale is the property type they all
	// name. Unmemoized this measured ~43x the two-class clone against the ~4x
	// the per-class structure alone accounts for.
	if limit := 8 * few; many > limit {
		t.Fatalf("host clone allocated %d bytes for 128 including classes, want at most %d (%d bytes for two)", many, limit, few)
	}
	t.Logf("host clone allocated %d bytes for two including classes and %d for 128", few, many)
}
