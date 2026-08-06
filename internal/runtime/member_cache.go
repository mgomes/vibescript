package runtime

import (
	"fmt"
	"sync"
)

type memberTable struct {
	once  sync.Once
	names []string
	table map[string]Value
	// receiverKinds lists the value kinds this table's members may be invoked
	// against. Empty means unrestricted.
	receiverKinds []ValueKind
}

func newMemberTable(names []string) *memberTable {
	return &memberTable{
		names: names,
	}
}

// newTypedMemberTable builds a member table whose members reach into a
// receiver payload of one of the given kinds.
//
// These members take their receiver as a call argument rather than capturing
// it, so one that escapes its member expression without being auto-invoked is
// later called with whatever receiver the call site supplies — nil for a bare
// callee. Reaching into that receiver for a payload it does not have is a Go
// type-assertion panic, and Script.Call does not recover, so script source
// could terminate the embedding process: `(missing rescue /a/.source)()` made
// regex.source read a nil receiver's regex payload. Guarding the receiver
// turns every such mismatch into an ordinary script error (#25).
func newTypedMemberTable(names []string, receiverKinds ...ValueKind) *memberTable {
	return &memberTable{
		names:         names,
		receiverKinds: receiverKinds,
	}
}

func (t *memberTable) lookup(name string, build func(string) (Value, error)) (Value, bool) {
	t.once.Do(func() {
		t.buildAll(build)
	})
	member, ok := t.table[name]
	return member, ok
}

func (t *memberTable) buildAll(build func(string) (Value, error)) {
	table := make(map[string]Value, len(t.names))
	for _, name := range t.names {
		member, err := build(name)
		if err != nil {
			panic(err)
		}
		if len(t.receiverKinds) > 0 {
			guardMemberReceiver(member, t.receiverKinds)
		}
		table[name] = member
	}
	t.table = table
}

// guardMemberReceiver wraps a member's Fn so a receiver of an unexpected kind
// is rejected before the member reads its payload. The wrap mutates the
// builtin in place, keeping its pointer identity: contract binding and the
// capability scopes key on it, and a fresh builtin here would silently drop
// those associations. Table members are built once under the table's sync.Once
// before any lookup returns, so the mutation races nothing.
func guardMemberReceiver(member Value, kinds []ValueKind) {
	builtin := valueBuiltin(member)
	if builtin == nil || builtin.Fn == nil {
		return
	}
	inner := builtin.Fn
	name := builtin.Name
	builtin.Fn = func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if !receiverKindAllowed(receiver, kinds) {
			return NewNil(), fmt.Errorf("%s requires a %s receiver, got %s", name, kindListText(kinds), receiver.Kind())
		}
		return inner(exec, receiver, args, kwargs, block)
	}
}

func receiverKindAllowed(receiver Value, kinds []ValueKind) bool {
	got := receiver.Kind()
	for _, kind := range kinds {
		if got == kind {
			return true
		}
	}
	return false
}

func kindListText(kinds []ValueKind) string {
	text := ""
	for i, kind := range kinds {
		switch {
		case i == 0:
			text = kind.String()
		case i == len(kinds)-1:
			text += " or " + kind.String()
		default:
			text += ", " + kind.String()
		}
	}
	return text
}
