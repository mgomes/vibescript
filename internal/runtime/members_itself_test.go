package runtime

import (
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
	member, ok := universalMember("itself")
	if !ok {
		t.Fatal("universalMember(itself) did not resolve")
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatal("itself member is not a builtin")
	}

	hash := NewHash(map[string]Value{"k": NewInt(1)})
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
		name   string
		source string
	}{
		{
			name: "positional argument",
			source: `
        def run
          5.itself(1)
        end
      `,
		},
		{
			name: "keyword argument",
			source: `
        def run
          5.itself(x: 1)
        end
      `,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "itself does not take arguments")
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
