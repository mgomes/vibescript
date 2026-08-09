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
// class does not copy the property contract once per parameter that references
// it. The contract is one node declared by the generated accessor; every
// unannotated ivar param naming the property points at it, so a per-param copy
// turned a type the source spells once into method_count copies of it during
// cloneValueForHost — after execution, where no quota can see it (#16).
func TestHostClonedIvarParamsShareOnePropertyContract(t *testing.T) {
	t.Parallel()

	const methods = 8
	script := compileScriptDefault(t, propertyContractCloneSource(4, methods))

	compiled, ok := script.classes["Big"]
	if !ok {
		t.Fatal("class Big is not compiled")
	}
	contract := compiled.Methods["m000000"].Params[0].PropertyType
	if contract == nil {
		t.Fatal("m000000's ivar param resolved no property contract; the test cannot observe sharing")
	}
	if len(contract.Shape) != 4 {
		t.Fatalf("property contract shape has %d fields, want 4", len(contract.Shape))
	}

	cloned := valueClass(callScript(t, context.Background(), script, "build", nil, CallOptions{}))
	if cloned == nil {
		t.Fatal("build did not return a class")
	}
	for i := range methods {
		name := fmt.Sprintf("m%06d", i)
		method, ok := cloned.Methods[name]
		if !ok {
			t.Fatalf("cloned class is missing method %s", name)
		}
		if got := method.Params[0].PropertyType; got != contract {
			t.Fatalf("cloned %s param property contract = %p, want the shared contract %p", name, got, contract)
		}
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
