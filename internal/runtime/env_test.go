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

func TestEnvClearArrayAppendBufferDetachesBinding(t *testing.T) {
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
	items := got.Array()
	if len(items) != 2 || cap(items) != 2 {
		t.Fatalf("detached items len/cap = %d/%d, want 2/2", len(items), cap(items))
	}
	if len(buffer) != 2 || cap(buffer) != 8 {
		t.Fatalf("source buffer len/cap = %d/%d, want 2/8", len(buffer), cap(buffer))
	}
	items[0] = NewInt(99)
	if buffer[0].Equal(NewInt(99)) {
		t.Fatalf("detached binding still aliases the append buffer")
	}
}
