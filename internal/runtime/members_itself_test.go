package runtime

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

// TestItselfReturnsReceiver covers the Ruby-style Object#itself helper, which
// returns the receiver unchanged across every value kind. Each case compares
// the result against the same expression evaluated directly, so itself must be
// a pure identity over scalars, collections, nil, and script instances.
func TestItselfReturnsReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Point
      def initialize(x)
        @x = x
      end

      def x
        @x
      end
    end

    enum Status
      ACTIVE
      DONE
    end

    def string_itself
      "vibe".itself
    end

    def int_itself
      42.itself
    end

    def float_itself
      3.5.itself
    end

    def bool_itself
      true.itself
    end

    def symbol_itself
      :tag.itself
    end

    def array_itself
      [1, 2, 3].itself
    end

    def hash_itself
      {a: 1, b: 2}.itself
    end

    def nil_itself
      nil.itself
    end

    def range_itself
      (1..3).itself
    end

    def duration_itself
      2.hours.itself
    end

    def instance_x
      Point.new(7).itself.x
    end

    def enum_value_itself
      Status::ACTIVE.itself.name
    end

    def no_paren
      99.itself
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "string", fn: "string_itself", want: NewString("vibe")},
		{name: "int", fn: "int_itself", want: NewInt(42)},
		{name: "float", fn: "float_itself", want: NewFloat(3.5)},
		{name: "bool", fn: "bool_itself", want: NewBool(true)},
		{name: "symbol", fn: "symbol_itself", want: NewSymbol("tag")},
		{name: "array", fn: "array_itself", want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
		{name: "hash", fn: "hash_itself", want: NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)})},
		{name: "nil", fn: "nil_itself", want: NewNil()},
		{name: "range", fn: "range_itself", want: NewRange(Range{Start: 1, End: 3})},
		{name: "instance member through itself", fn: "instance_x", want: NewInt(7)},
		{name: "enum value member through itself", fn: "enum_value_itself", want: NewString("ACTIVE")},
		{name: "no parentheses auto-invokes", fn: "no_paren", want: NewInt(99)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}

	t.Run("duration preserves kind", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "duration_itself", nil); got.Kind() != KindDuration {
			t.Fatalf("duration_itself() kind = %v, want duration", got.Kind())
		}
	})
}

// TestItselfPreservesReferenceIdentity confirms itself returns the very same
// underlying collection rather than a copy, so value ownership and the
// host-boundary isolation already established for the receiver are preserved.
func TestItselfPreservesReferenceIdentity(t *testing.T) {
	t.Parallel()
	hash := NewHash(map[string]Value{"k": NewInt(1)})
	member, ok := universalMember(hash, "itself")
	if !ok {
		t.Fatal("universalMember(itself) did not resolve")
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatal("itself member is not a builtin")
	}
	if !builtin.AutoInvoke {
		t.Fatal("itself member must auto-invoke so bare access returns the receiver")
	}

	got, err := builtin.Fn(&Execution{}, hash, nil, nil, NewNil())
	if err != nil {
		t.Fatalf("itself returned error: %v", err)
	}
	if reflect.ValueOf(got.Hash()).Pointer() != reflect.ValueOf(hash.Hash()).Pointer() {
		t.Fatal("itself returned a copied hash, want the same underlying map")
	}
}

// TestItselfTakesPrecedenceOverHashKey documents that itself resolves as the
// universal method even when the receiver is a hash carrying an "itself" key,
// matching Ruby where {itself: 1}.itself returns the hash, not the value.
func TestItselfTakesPrecedenceOverHashKey(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run
      data = {itself: 1, other: 2}
      data.itself[:other]
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(2)) {
		t.Fatalf("run() = %#v, want 2 (itself must return the hash, not the key value)", got)
	}
}

// TestItselfRejectsArguments verifies itself refuses positional and keyword
// arguments, mirroring Ruby's zero-arity Object#itself.
func TestItselfRejectsArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "positional argument",
			source: `
        def run
          5.itself(1)
        end
      `,
			wantErr: "int.itself expects 0 arguments, got 1",
		},
		{
			name: "keyword argument",
			source: `
        def run
          5.itself(x: 1)
        end
      `,
			wantErr: "int.itself does not accept keyword arguments",
		},
		{
			name: "block argument",
			source: `
        def run
          5.itself do |x|
            x
          end
        end
      `,
			wantErr: "int.itself does not accept a block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

// TestItselfUserDefinedOverridesBuiltin guards member-resolution consistency:
// a user-defined itself method must win over the universal builtin in both the
// parenthesized form (obj.itself()) and the no-paren form (probe = obj.itself).
// Resolving the universal member ahead of per-type dispatch silently shadowed
// the user method in the no-paren form while the paren form ran it, so the two
// call paths disagreed. The universal member now resolves only as a fallback,
// after type-specific members and user-defined methods, in both paths.
func TestItselfUserDefinedOverridesBuiltin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Box
      def initialize(value)
        @value = value
      end

      def itself
        @value
      end
    end

    class Counter
      @@total = 41

      def self.itself
        @@total
      end
    end

    def instance_paren
      Box.new(7).itself
    end

    def instance_no_paren
      box = Box.new(9)
      probe = box.itself
      probe
    end

    def class_paren
      Counter.itself
    end

    def class_no_paren
      probe = Counter.itself
      probe
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "instance with parentheses", fn: "instance_paren", want: NewInt(7)},
		{name: "instance without parentheses", fn: "instance_no_paren", want: NewInt(9)},
		{name: "class method with parentheses", fn: "class_paren", want: NewInt(41)},
		{name: "class method without parentheses", fn: "class_no_paren", want: NewInt(41)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestItselfPrivateMethodNotMaskedByBuiltin guards that a private itself method
// reports the same private-method error in both call forms. The universal
// builtin must not be used as a fallback for a method that exists but is denied
// for privacy, or obj.itself would silently bypass the error that obj.itself()
// raises.
func TestItselfPrivateMethodNotMaskedByBuiltin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Vault
      private def itself
        :secret
      end
    end

    def via_paren
      Vault.new.itself()
    end

    def via_no_paren
      vault = Vault.new
      probe = vault.itself
      probe
    end
    `)

	for _, fn := range []string{"via_paren", "via_no_paren"} {
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, fn, nil, CallOptions{}, "private method itself")
		})
	}
}

// TestItselfFallsBackForUndefinedMethod confirms the universal builtin still
// resolves on script instances and classes that do not define their own itself,
// returning the receiver unchanged in both call forms.
func TestItselfFallsBackForUndefinedMethod(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Widget
      def initialize(label)
        @label = label
      end

      def label
        @label
      end
    end

    def instance_paren
      Widget.new("a").itself.label
    end

    def instance_no_paren
      widget = Widget.new("b")
      probe = widget.itself
      probe.label
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "instance with parentheses", fn: "instance_paren", want: NewString("a")},
		{name: "instance without parentheses", fn: "instance_no_paren", want: NewString("b")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestItselfInMemberCompletion confirms itself is surfaced to editor tooling as
// a universal member for every receiver type.
func TestItselfInMemberCompletion(t *testing.T) {
	t.Parallel()
	for receiver, names := range MemberCompletionNames() {
		if !slices.Contains(names, "itself") {
			t.Errorf("MemberCompletionNames[%q] = %v, missing itself", receiver, names)
		}
	}
}

// TestItselfObjectDataFieldNotShadowing confirms a KindObject that is an ordinary
// data object — the shape hosts and capabilities return — does not let a stored
// non-callable "itself" field shadow the universal builtin. Member dispatch must
// resolve the universal itself builtin (returning the object), while the stored
// field stays readable as data through index access. A callable stored under the
// same name (a module/capability method export) must still shadow, so namespace
// exports remain reachable. This mirrors the eql?/equal? data-field behavior.
func TestItselfObjectDataFieldNotShadowing(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  nil\nend")
	exec := newExecutionForCall(script, context.Background(), newEnv(nil), CallOptions{})

	dataField := NewInt(7)
	obj := NewObject(map[string]Value{"itself": dataField})

	member, err := exec.resolveMember(obj, "itself", Position{}, false)
	if err != nil {
		t.Fatalf("resolveMember(itself) on data object: %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("itself on a data object resolved to %v, want the universal builtin; a data field must not shadow it", member.Kind())
	}
	got, err := builtin.Fn(exec, obj, nil, nil, NewNil())
	if err != nil {
		t.Fatalf("invoking resolved itself against the object: %v", err)
	}
	if !got.Identical(obj) {
		t.Fatalf("obj.itself returned %v, want the receiver; the universal builtin must answer for a data object", got.Kind())
	}
	// The field is still readable as data through index access.
	if stored, ok := obj.Hash()["itself"]; !ok || !stored.Equal(dataField) {
		t.Fatalf("data field itself no longer readable as data: got %v ok=%v", stored, ok)
	}

	// A callable stored under the same name is a method export and must shadow.
	callable := NewAutoBuiltin("export.itself", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewString("export itself"), nil
	})
	namespace := NewObject(map[string]Value{"itself": callable})
	exported, err := exec.resolveMember(namespace, "itself", Position{}, false)
	if err != nil {
		t.Fatalf("resolveMember(itself) on namespace object: %v", err)
	}
	if !exported.Identical(callable) {
		t.Fatalf("a callable itself export was not resolved as the stored member; module exports must remain reachable")
	}
}

// TestItselfObjectDataFieldEndToEnd is the script-level counterpart to
// TestItselfObjectDataFieldNotShadowing: a host hands a data object carrying an
// itself field to a script, which must observe the receiver through member
// dispatch while still reading the field as data through index access.
func TestItselfObjectDataFieldEndToEnd(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run(obj)
  [obj.itself["itself"], obj["itself"]]
end`)

	obj := NewObject(map[string]Value{"itself": NewInt(5)})
	result, err := script.Call(context.Background(), "run", []Value{obj}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	compareArrays(t, result, []Value{NewInt(5), NewInt(5)})
}

// TestItselfHashKeyDoesNotShadow confirms a stored hash entry keyed "itself" is
// data, not a member: itself returns the hash itself, and the entry stays
// readable through index access. This complements TestItselfTakesPrecedenceOver-
// HashKey by checking the data path explicitly through resolveMember.
func TestItselfHashKeyDoesNotShadow(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run(h)
  [h.itself["itself"], h["itself"]]
end`)

	h := NewHash(map[string]Value{"itself": NewInt(3)})
	result, err := script.Call(context.Background(), "run", []Value{h}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	compareArrays(t, result, []Value{NewInt(3), NewInt(3)})
}
