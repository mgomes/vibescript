package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestMemberBuiltinRejectsWrongReceiverInsteadOfPanicking pins the reported
// vector: a member expression in a call-target position evaluates without
// auto-invoking, so the member builtin escapes unbound and is then invoked
// with a nil receiver. Reading a payload the receiver does not have is a Go
// type-assertion panic, and Script.Call does not recover, so this crashed the
// embedding process from script source (#25).
func TestMemberBuiltinRejectsWrongReceiverInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  (missing rescue /a/.source)()
end`)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("an unbound member builtin must not be callable against a nil receiver")
	}
	if !strings.Contains(err.Error(), "requires a regex receiver") {
		t.Fatalf("err = %v, want a receiver-kind error", err)
	}
}

// TestMemberBuiltinsGuardEveryTypedReceiver sweeps the guarded member tables:
// every kind whose members read a typed payload must answer a mismatched
// receiver with a script error. A kind missing its guard shows up here as a
// panic that tears down the test binary, which is what it would do to a host.
func TestMemberBuiltinsGuardEveryTypedReceiver(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"regex":  `/a/.source`,
		"string": `"ab".upcase`,
		"array":  `[1].first`,
		"hash":   `{ a: 1 }.keys`,
		"int":    `1.abs`,
		"float":  `1.5.round`,
		"symbol": `:sym.to_s`,
		"bool":   `true.to_s`,
	}
	for name, member := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `def run()
  (missing rescue `+member+`)()
end`)
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				return // The member auto-invoked or the result is callable; no escape.
			}
			if strings.Contains(err.Error(), "panic") {
				t.Fatalf("%s member panicked instead of erroring: %v", name, err)
			}
		})
	}
}

// TestGuardedMembersStayCallableWithCorrectReceivers pins that the guard does
// not disturb ordinary dispatch: every guarded kind still answers its members
// normally, including a big integer reaching the int table and an object
// reaching the hash table.
func TestGuardedMembersStayCallableWithCorrectReceivers(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  [
    /ab/.source,
    "ab".upcase,
    [3, 1].first,
    { a: 1 }.keys.length,
    -2.abs,
    1.5.round,
    :sym.to_s,
    true.to_s,
  ]
end`)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("guarded members must stay callable: %v", err)
	}
	got := result.Array()
	want := []string{"ab", "AB", "3", "1", "2", "2", "sym", "true"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, expected := range want {
		if got[i].String() != expected {
			t.Fatalf("result %d = %q, want %q", i, got[i].String(), expected)
		}
	}
}

// TestObjectMembersReachHashTable pins that the hash table's guard admits
// objects: capability and module namespaces are KindObject and resolve their
// members through the same table.
func TestObjectMembersReachHashTable(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(bag)
  bag.keys.length
end`)
	bag := NewObject(map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	result, err := script.Call(context.Background(), "run", []Value{bag}, CallOptions{})
	if err != nil {
		t.Fatalf("an object must reach the hash member table: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 2 {
		t.Fatalf("result = %#v, want 2", result)
	}
}
