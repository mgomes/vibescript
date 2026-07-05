package runtime

import (
	"fmt"
	"testing"
)

func TestEnvInlineBindingsPromoteToMap(t *testing.T) {
	t.Parallel()

	env := newEnv(nil)
	for i := range inlineEnvBindingCapacity {
		env.Define(fmt.Sprintf("v%d", i), NewInt(int64(i)))
	}
	if env.values != nil {
		t.Fatalf("values map initialized before inline capacity was exceeded")
	}
	if got := env.dynamicLen(); got != inlineEnvBindingCapacity {
		t.Fatalf("dynamicLen() = %d, want %d", got, inlineEnvBindingCapacity)
	}

	env.Define("overflow", NewInt(99))
	if env.values == nil {
		t.Fatalf("values map was not initialized after inline capacity was exceeded")
	}
	if env.inlineLen != 0 {
		t.Fatalf("inlineLen after promotion = %d, want 0", env.inlineLen)
	}
	for i := range inlineEnvBindingCapacity {
		name := fmt.Sprintf("v%d", i)
		val, ok := env.Get(name)
		if !ok || !val.Equal(NewInt(int64(i))) {
			t.Fatalf("Get(%q) = (%#v, %t), want %d", name, val, ok, i)
		}
	}
	val, ok := env.Get("overflow")
	if !ok || !val.Equal(NewInt(99)) {
		t.Fatalf("Get(overflow) = (%#v, %t), want 99", val, ok)
	}
}

func TestEnvInlineBindingsSupportAssignmentAndStaticTransitions(t *testing.T) {
	t.Parallel()

	parent := newEnv(nil)
	parent.Define("shared", NewInt(1))
	child := newEnv(parent)
	child.Assign("shared", NewInt(2))
	val, ok := parent.Get("shared")
	if !ok || !val.Equal(NewInt(2)) {
		t.Fatalf("parent.Get(shared) = (%#v, %t), want reassigned value", val, ok)
	}

	child.Assign("local", NewInt(3))
	val, ok = child.Get("local")
	if !ok || !val.Equal(NewInt(3)) {
		t.Fatalf("child.Get(local) = (%#v, %t), want local value", val, ok)
	}
	if child.values != nil {
		t.Fatalf("child assignment initialized values map inside inline capacity")
	}

	env := newEnv(nil)
	env.Define("name", NewInt(1))
	env.DefineStatic("name", NewInt(2))
	if env.inlineLen != 0 {
		t.Fatalf("inlineLen after DefineStatic shadow = %d, want 0", env.inlineLen)
	}
	val, ok = env.Get("name")
	if !ok || !val.Equal(NewInt(2)) {
		t.Fatalf("Get(name) after DefineStatic = (%#v, %t), want static value", val, ok)
	}
	env.Define("name", NewInt(3))
	if env.staticBytes != 0 {
		t.Fatalf("staticBytes after dynamic Define = %d, want 0", env.staticBytes)
	}
	val, ok = env.Get("name")
	if !ok || !val.Equal(NewInt(3)) {
		t.Fatalf("Get(name) after Define = (%#v, %t), want dynamic value", val, ok)
	}
}

func TestEnvGetSkippingDoesNotCloneFrozenBindingIntoSkippedScope(t *testing.T) {
	t.Parallel()

	frozen := newEnv(nil)
	frozen.frozen = true
	frozen.DefineStatic("JSON", NewObject(map[string]Value{"name": NewString("json")}))
	env := newEnv(frozen)

	val, ok := env.getSkipping("JSON", map[*Env]struct{}{env: {}})
	if !ok {
		t.Fatalf("getSkipping(JSON) missing frozen binding")
	}
	if val.Kind() != KindObject {
		t.Fatalf("getSkipping(JSON) kind = %s, want object", val.Kind())
	}
	if env.hasOwnBinding("JSON") {
		t.Fatalf("getSkipping(JSON) materialized binding into skipped scope")
	}
}

func TestEnvResetForBlockCallClearsPerCallState(t *testing.T) {
	t.Parallel()

	oldParent := newEnv(nil)
	oldParent.Define("old_parent", NewInt(1))
	parent := newEnv(nil)
	parent.Define("parent", NewInt(2))

	env := newEnv(oldParent)
	env.inline[0] = envBinding{name: "inline", value: NewInt(3)}
	env.inlineLen = 1
	env.values = map[string]Value{"mapped": NewInt(4)}
	env.statics = map[string]Value{"static": NewInt(5)}
	env.staticBytes = 99
	env.arrayAppendBuffers = map[string][]Value{"items": {NewInt(6)}}
	env.assignBoundary = true
	env.rebindOuter = true
	env.frozen = true

	env.resetForBlockCall(parent)

	if env.parent != parent {
		t.Fatalf("parent after reset = %p, want %p", env.parent, parent)
	}
	if env.inlineLen != 0 {
		t.Fatalf("inlineLen after reset = %d, want 0", env.inlineLen)
	}
	if _, ok := env.Get("inline"); ok {
		t.Fatalf("inline binding survived reset")
	}
	if len(env.values) != 0 {
		t.Fatalf("values after reset = %v, want empty", env.values)
	}
	if env.statics != nil {
		t.Fatalf("statics after reset = %v, want nil", env.statics)
	}
	if env.staticBytes != 0 {
		t.Fatalf("staticBytes after reset = %d, want 0", env.staticBytes)
	}
	if env.arrayAppendBuffers != nil {
		t.Fatalf("arrayAppendBuffers after reset = %v, want nil", env.arrayAppendBuffers)
	}
	if env.assignBoundary {
		t.Fatalf("assignBoundary after reset = true, want false")
	}
	if env.rebindOuter {
		t.Fatalf("rebindOuter after reset = true, want false")
	}
	if env.frozen {
		t.Fatalf("frozen after reset = true, want false")
	}
	if _, ok := env.Get("old_parent"); ok {
		t.Fatalf("old parent binding survived reset")
	}
	if val, ok := env.Get("parent"); !ok || !val.Equal(NewInt(2)) {
		t.Fatalf("new parent binding after reset = (%#v, %t), want 2", val, ok)
	}
}

// TestEnvClearArrayAppendBufferSettlesBinding pins the settle contract of the
// concat accumulator: clearing on a read only unregisters the hidden buffer.
// The binding keeps the exact wrapper the reader received (one Ruby object, so
// later in-place mutations stay visible through both handles), and the
// wrapper's elements stay clamped to length so nothing can ever grow the
// escaped backing in place.
func TestEnvClearArrayAppendBufferSettlesBinding(t *testing.T) {
	t.Parallel()

	env := newEnv(nil)
	buffer := make([]Value, 2, 8)
	buffer[0] = NewInt(1)
	buffer[1] = NewInt(2)
	val := arrayValueFromAppendBuffer(buffer)
	env.assignArrayAppendBuffer("items", val, buffer)

	env.clearArrayAppendBuffer("items")

	if _, ok := env.arrayAppendBuffer("items"); ok {
		t.Fatalf("arrayAppendBuffer(items) survived clear")
	}
	got, ok := env.Get("items")
	if !ok {
		t.Fatalf("Get(items) missing after clear")
	}
	if arrayIdentity(got) != arrayIdentity(val) {
		t.Fatalf("clear rebound items to a different wrapper; the binding and the reader must stay one object")
	}
	items := got.Array()
	if len(items) != 2 || cap(items) != 2 {
		t.Fatalf("settled items len/cap = %d/%d, want 2/2 (clamped to length)", len(items), cap(items))
	}
}

// TestEnvSettleArrayAppendResultUnregistersBuffer pins the escape-time settle:
// the escaping value keeps its wrapper identity while the matching buffer
// registration is dropped, so no later fast-path concat can append into the
// escaped backing.
func TestEnvSettleArrayAppendResultUnregistersBuffer(t *testing.T) {
	t.Parallel()

	env := newEnv(nil)
	buffer := make([]Value, 2, 8)
	buffer[0] = NewInt(1)
	buffer[1] = NewInt(2)
	val := arrayValueFromAppendBuffer(buffer)
	env.assignArrayAppendBuffer("items", val, buffer)

	settled := env.settleArrayAppendResult(val)

	if arrayIdentity(settled) != arrayIdentity(val) {
		t.Fatalf("settle returned a different wrapper; escaping results must keep identity")
	}
	if _, ok := env.arrayAppendBuffer("items"); ok {
		t.Fatalf("arrayAppendBuffer(items) survived settle of its wrapper")
	}

	other := NewArray([]Value{NewInt(1), NewInt(2)})
	env.assignArrayAppendBuffer("items", val, buffer)
	if got := env.settleArrayAppendResult(other); arrayIdentity(got) != arrayIdentity(other) {
		t.Fatalf("settle of an unrelated array changed its wrapper")
	}
	if _, ok := env.arrayAppendBuffer("items"); !ok {
		t.Fatalf("settle of an unrelated array dropped the registered buffer")
	}
}
