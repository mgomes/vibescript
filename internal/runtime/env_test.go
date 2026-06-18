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
