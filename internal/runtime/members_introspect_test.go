package runtime

import (
	"context"
	"testing"
)

// evalBoolExpr compiles and runs a one-line expression and asserts it returns a
// boolean, returning that boolean for comparison.
func evalBoolExpr(t *testing.T, expr string) bool {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindBool {
		t.Fatalf("%s kind = %v, want bool", expr, got.Kind())
	}
	return got.Bool()
}

func TestRespondToCoreValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"string has length", `"Ada".respond_to?(:length)`, true},
		{"string string arg", `"Ada".respond_to?("upcase")`, true},
		{"string missing", `"Ada".respond_to?(:nope)`, false},
		{"array has map", `[1, 2].respond_to?(:map)`, true},
		{"array missing", `[1, 2].respond_to?(:nope)`, false},
		{"int has times", `3.respond_to?(:times)`, true},
		{"int missing", `3.respond_to?(:nope)`, false},
		{"float has round", `1.5.respond_to?(:round)`, true},
		{"symbol has to_s", `:ok.respond_to?(:to_s)`, true},
		{"range has to_a", `(1..3).respond_to?(:to_a)`, true},
		{"range has each", `(1..3).respond_to?(:each)`, true},
		{"range missing", `(1..3).respond_to?(:nope)`, false},
		{"nil has inspect", `nil.respond_to?(:inspect)`, true},
		{"nil missing", `nil.respond_to?(:length)`, false},
		{"bool has inspect", `true.respond_to?(:inspect)`, true},
		{"predicate itself", `42.respond_to?(:respond_to?)`, true},
		{"is_a predicate", `42.respond_to?(:is_a?)`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := evalBoolExpr(t, tc.expr); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRespondToHashReportsMethodsNotData(t *testing.T) {
	t.Parallel()

	// Hash data keys are not methods: respond_to? must report only the hash's
	// callable members, mirroring Ruby where h.respond_to?(:some_key) is false.
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"data key not a method", `{name: "x"}.respond_to?(:name)`, false},
		{"keys method", `{name: "x"}.respond_to?(:keys)`, true},
		{"each method", `{}.respond_to?(:each)`, true},
		{"missing method", `{}.respond_to?(:nope)`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := evalBoolExpr(t, tc.expr); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRespondToNamespace(t *testing.T) {
	t.Parallel()

	// Namespace objects (such as Math) respond to their callable module
	// functions but not to their plain constants. They also expose the hash
	// builtin methods that back them, so respond_to? mirrors actual dispatch.
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"module function", `Math.respond_to?(:sqrt)`, true},
		{"constant not a method", `Math.respond_to?(:PI)`, false},
		{"backing hash method", `Math.respond_to?(:keys)`, true},
		{"missing", `Math.respond_to?(:nope)`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := evalBoolExpr(t, tc.expr); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRespondToInstancePrivacy(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class User
      def greet
        "hi"
      end

      private def secret
        "s"
      end
    end

    def public_method()
      User.new.respond_to?(:greet)
    end

    def private_hidden()
      User.new.respond_to?(:secret)
    end

    def private_include_all()
      User.new.respond_to?(:secret, true)
    end

    def missing()
      User.new.respond_to?(:nope)
    end

    def ivar_not_method()
      User.new.respond_to?(:class)
    end
    `)

	cases := map[string]bool{
		"public_method":       true,
		"private_hidden":      false,
		"private_include_all": true,
		"missing":             false,
		"ivar_not_method":     true, // `class` is a callable member
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

func TestRespondToInstanceIvarsNeverRespond(t *testing.T) {
	t.Parallel()

	// An instance variable holding any value (even a callable would not change
	// this) is data, not a method, so respond_to? reports false for its name.
	script := compileScript(t, `
    class Box
      def initialize
        @value = 7
      end
    end

    def run()
      Box.new.respond_to?(:value)
    end
    `)
	if got := callFunc(t, script, "run", nil); got.Bool() {
		t.Fatalf("respond_to?(:value) on ivar name = true, want false")
	}
}

func TestRespondToFromSelfSeesPrivate(t *testing.T) {
	t.Parallel()

	// Inside the receiver, bare respond_to? sees private methods because it uses
	// implicit receiver dispatch. An explicit self receiver follows public
	// dispatch unless include_all is requested.
	script := compileScript(t, `
    class User
      def check
        [respond_to?(:secret), self.respond_to?(:secret), self.respond_to?(:secret, true)]
      end

      private def secret
        "s"
      end
    end

    def run()
      User.new.check
    end
    `)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindArray {
		t.Fatalf("run kind = %v, want array", got.Kind())
	}
	arr := got.Array()
	if len(arr) != 3 || !arr[0].Bool() || arr[1].Bool() || !arr[2].Bool() {
		t.Fatalf("respond_to? from self = %v, want [true, false, true]", arr)
	}
}

func TestRespondToClassValue(t *testing.T) {
	t.Parallel()

	// A class value responds to new and its class methods, but not to class
	// variables. is_a? on the class value itself is false: a class is not an
	// instance.
	script := compileScript(t, `
    class Widget
      def self.build
        "built"
      end
    end

    def has_new()
      Widget.respond_to?(:new)
    end

    def has_class_method()
      Widget.respond_to?(:build)
    end

    def missing()
      Widget.respond_to?(:nope)
    end

    def class_is_not_instance()
      Widget.is_a?(Widget)
    end
    `)

	cases := map[string]bool{
		"has_new":               true,
		"has_class_method":      true,
		"missing":               false,
		"class_is_not_instance": false,
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

func TestRespondToEnumValue(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    enum Color
      Red
      Blue
    end

    def has_name()
      Color::Red.respond_to?(:name)
    end

    def has_symbol()
      Color::Red.respond_to?(:symbol)
    end

    def missing()
      Color::Red.respond_to?(:nope)
    end
    `)

	cases := map[string]bool{
		"has_name":   true,
		"has_symbol": true,
		"missing":    false,
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool || got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got, want)
		}
	}
}

func TestRespondToErrors(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def no_args()
      "x".respond_to?
    end

    def too_many()
      "x".respond_to?(:a, true, 1)
    end

    def bad_name()
      "x".respond_to?(42)
    end

    def bad_flag()
      "x".respond_to?(:a, "yes")
    end
    `)

	requireCallErrorContains(t, script, "no_args", nil, CallOptions{}, "1 or 2 arguments")
	requireCallErrorContains(t, script, "too_many", nil, CallOptions{}, "1 or 2 arguments")
	requireCallErrorContains(t, script, "bad_name", nil, CallOptions{}, "symbol or string")
	requireCallErrorContains(t, script, "bad_flag", nil, CallOptions{}, "boolean second argument")
}

func TestClassPredicatesInstances(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class A
    end

    class B
    end

    def is_a_same()
      A.new.is_a?(A)
    end

    def kind_of_same()
      A.new.kind_of?(A)
    end

    def instance_of_same()
      A.new.instance_of?(A)
    end

    def is_a_other()
      A.new.is_a?(B)
    end

    def instance_of_other()
      A.new.instance_of?(B)
    end
    `)

	cases := map[string]bool{
		"is_a_same":         true,
		"kind_of_same":      true,
		"instance_of_same":  true,
		"is_a_other":        false,
		"instance_of_other": false,
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

func TestClassPredicatesFromSelf(t *testing.T) {
	t.Parallel()

	// is_a?/instance_of? called bare inside a method bind self as the receiver,
	// so they report the instance's own class.
	script := compileScript(t, `
    class A
      def checks
        [is_a?(A), instance_of?(A), kind_of?(A)]
      end
    end

    def run()
      A.new.checks
    end
    `)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindArray {
		t.Fatalf("run kind = %v, want array", got.Kind())
	}
	for i, v := range got.Array() {
		if v.Kind() != KindBool || !v.Bool() {
			t.Fatalf("self class predicate[%d] = %v, want true", i, v)
		}
	}
}

func TestClassPredicatesCoreValuesAreFalse(t *testing.T) {
	t.Parallel()

	// Core values are not instances of any script class.
	script := compileScript(t, `
    class A
    end

    def int_is_a()
      42.is_a?(A)
    end

    def string_instance_of()
      "x".instance_of?(A)
    end

    def array_kind_of()
      [1].kind_of?(A)
    end
    `)
	for _, fn := range []string{"int_is_a", "string_instance_of", "array_kind_of"} {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("%s = %v, want false", fn, got)
		}
	}
}

func TestClassPredicatesRejectNonClassArg(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class A
    end

    def not_a_class()
      A.new.is_a?(42)
    end

    def missing_arg()
      A.new.is_a?
    end

    def too_many()
      A.new.kind_of?(A, A)
    end
    `)

	requireCallErrorContains(t, script, "not_a_class", nil, CallOptions{}, "class argument")
	requireCallErrorContains(t, script, "missing_arg", nil, CallOptions{}, "exactly one argument")
	requireCallErrorContains(t, script, "too_many", nil, CallOptions{}, "exactly one argument")
}

func TestUniversalPredicateOverrideByClassMethod(t *testing.T) {
	t.Parallel()

	// A script class may define its own respond_to?; the user definition wins
	// over the universal fallback.
	script := compileScript(t, `
    class Custom
      def respond_to?(name)
        "overridden"
      end
    end

    def run()
      Custom.new.respond_to?(:anything)
    end
    `)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindString || got.String() != "overridden" {
		t.Fatalf("overridden respond_to? = %v, want \"overridden\"", got)
	}
}

func TestRespondToRejectsBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def run()
      "x".respond_to?(:length) { |a| a }
    end
    `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take a block")
}

// TestPredicateDataSlotsDoNotShadow guards the contract that every value
// responds to the universal introspection predicates: a stored data slot keyed
// with a predicate name (a hash key, instance variable, or class variable) is
// data, not a method, so it must never preempt the universal predicate. Without
// this guarantee a record with a colliding key would resolve the stored value
// and attempt to invoke that data instead of the predicate, making introspection
// unusable for such records.
func TestPredicateDataSlotsDoNotShadow(t *testing.T) {
	t.Parallel()

	t.Run("hash key", func(t *testing.T) {
		t.Parallel()
		// {"respond_to?": 1}.respond_to?(:keys) must call the predicate, not the
		// stored 1, and report that the hash responds to its keys builtin.
		if got := evalBoolExpr(t, `{"respond_to?": 1}.respond_to?(:keys)`); !got {
			t.Fatalf("hash with colliding respond_to? key = false, want true")
		}
		// is_a? must also reach the universal predicate (not the stored data), so
		// it reports false against a class for a hash receiver.
		script := compileScript(t, `
      class A
      end

      def run()
        {"is_a?": 1}.is_a?(A)
      end
      `)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("hash with colliding is_a? key = %v, want false", got)
		}
	})

	t.Run("instance variable", func(t *testing.T) {
		t.Parallel()
		// An instance carrying an ivar named like a predicate must still answer the
		// universal predicate rather than the stored ivar value.
		script := compileScript(t, `
      class Record
        def initialize
          @respond_to? = 1
        end

        def name
          "r"
        end
      end

      def run()
        Record.new.respond_to?(:name)
      end
      `)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindBool || !got.Bool() {
			t.Fatalf("instance with colliding respond_to? ivar = %v, want true", got)
		}
	})

	t.Run("class variable", func(t *testing.T) {
		t.Parallel()
		// A class carrying a class var named like a predicate must still answer the
		// universal predicate on the class value.
		script := compileScript(t, `
      class Widget
        @@respond_to? = 1

        def self.build
          "built"
        end
      end

      def run()
        Widget.respond_to?(:build)
      end
      `)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindBool || !got.Bool() {
			t.Fatalf("class with colliding respond_to? class var = %v, want true", got)
		}
	})
}

// TestRespondToPrivateUniversalOverride guards that a private override of a
// universal member (eql?/respond_to?/...) is reported per privacy, matching the
// dispatch that would otherwise raise. Without this, respond_to? would report a
// privately overridden universal member as callable to an external caller even
// though invoking it publicly raises a private-method error.
func TestRespondToPrivateUniversalOverride(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class User
      private def eql?(other)
        true
      end
    end

    def probe_public()
      User.new.respond_to?(:eql?)
    end

    def probe_include_all()
      User.new.respond_to?(:eql?, true)
    end

    def probe_other_universal()
      User.new.respond_to?(:itself)
    end
    `)

	cases := map[string]bool{
		// A private override of a universal member is hidden from an external probe
		// (a public dispatch of eql? would raise), but visible with include_all.
		"probe_public":      false,
		"probe_include_all": true,
		// An un-overridden universal member still responds via the fallback.
		"probe_other_universal": true,
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

// TestRespondToClassPrivateUniversalOverride mirrors the instance case for a
// class value: a private class-method override of a universal member is hidden
// from an external probe but visible with include_all.
func TestRespondToClassPrivateUniversalOverride(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Widget
      private def self.eql?(other)
        true
      end
    end

    def probe_public()
      Widget.respond_to?(:eql?)
    end

    def probe_include_all()
      Widget.respond_to?(:eql?, true)
    end
    `)

	cases := map[string]bool{
		"probe_public":      false,
		"probe_include_all": true,
	}
	for fn, want := range cases {
		got := callFunc(t, script, fn, nil)
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

// TestRespondToObjectFieldShadowsHashBuiltin guards that for object values a
// stored data field shadows the hash builtin of the same name in both dispatch
// and respond_to?: a non-callable field keyed "keys"/"size" means record.keys
// returns the field, so respond_to?(:keys) must report false rather than
// pointing at a hash builtin dispatch would never reach. A field holding a
// callable export still responds, and a name with no field falls back to the
// hash builtin.
func TestRespondToObjectFieldShadowsHashBuiltin(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def keys_field()
      record.keys
    end

    def responds_keys()
      record.respond_to?(:keys)
    end

    def responds_size_builtin()
      record.respond_to?(:size)
    end

    def responds_callable_field()
      record.respond_to?(:work)
    end

    def responds_universal()
      record.respond_to?(:itself)
    end
    `)

	record := NewObject(map[string]Value{
		// A non-callable field shadowing the hash builtin: record.keys returns 7,
		// so the receiver does not respond to :keys as a callable.
		"keys": NewInt(7),
		// A callable export keyed "work": dispatch returns it, so it responds.
		"work": NewBuiltin("work", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			return NewNil(), nil
		}),
		"name": NewString("x"),
	})
	globals := map[string]Value{"record": record}

	intCases := map[string]int64{
		"keys_field": 7, // the field shadows the hash builtin
	}
	for fn, want := range intCases {
		got, err := script.Call(context.Background(), fn, nil, CallOptions{Globals: globals})
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got.Kind() != KindInt || got.Int() != want {
			t.Fatalf("%s = %v, want %d", fn, got, want)
		}
	}

	boolCases := map[string]bool{
		"responds_keys":           false, // shadowed by the non-callable field
		"responds_size_builtin":   true,  // no field, hash builtin is reachable
		"responds_callable_field": true,  // callable export responds
		"responds_universal":      true,  // universal member always responds
	}
	for fn, want := range boolCases {
		got, err := script.Call(context.Background(), fn, nil, CallOptions{Globals: globals})
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got.Kind() != KindBool {
			t.Fatalf("%s kind = %v, want bool", fn, got.Kind())
		}
		if got.Bool() != want {
			t.Fatalf("%s = %v, want %v", fn, got.Bool(), want)
		}
	}
}

// TestPredicateMethodStillOverrides confirms the data-slot guard does not also
// suppress a genuine user-defined method of the same name: a class method named
// like a predicate continues to override the universal fallback.
func TestPredicateMethodStillOverrides(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Custom
      def is_a?(other)
        "custom is_a"
      end
    end

    def run()
      Custom.new.is_a?(Custom)
    end
    `)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindString || got.String() != "custom is_a" {
		t.Fatalf("user-defined is_a? = %v, want \"custom is_a\"", got)
	}
}

// TestRespondToReportsMergedUniversalHelpers guards that respond_to? reports the
// full set of Object-level helpers — the value-only helpers itself/nil?/eql?/
// equal? and the block helpers tap/yield_self that share the universal-member
// fallback with the introspection predicates — so the merged universal mechanism
// answers respond_to? consistently with actual dispatch.
func TestRespondToReportsMergedUniversalHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"int itself", `42.respond_to?(:itself)`, true},
		{"int nil?", `42.respond_to?(:nil?)`, true},
		{"int eql?", `42.respond_to?(:eql?)`, true},
		{"int equal?", `42.respond_to?(:equal?)`, true},
		{"int tap", `42.respond_to?(:tap)`, true},
		{"int yield_self", `42.respond_to?(:yield_self)`, true},
		{"string tap", `"x".respond_to?(:tap)`, true},
		{"array yield_self", `[1].respond_to?(:yield_self)`, true},
		{"hash nil?", `{}.respond_to?(:nil?)`, true},
		{"hash tap (no shadow)", `{}.respond_to?(:tap)`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := evalBoolExpr(t, tc.expr); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestRespondToBlockHelpersShadowedByData guards the data-eligible block helpers
// tap/yield_self: unlike the data-safe helpers and the introspection predicates,
// a non-callable data slot keyed tap/yield_self shadows the helper (dispatch
// returns the data), so respond_to? must report false — mirroring resolveMember's
// per-kind decision. A data slot keyed by a data-safe helper or an introspection
// predicate never shadows, so those still respond.
func TestRespondToBlockHelpersShadowedByData(t *testing.T) {
	t.Parallel()

	t.Run("hash tap key shadows", func(t *testing.T) {
		t.Parallel()
		if got := evalBoolExpr(t, `{"tap": 1}.respond_to?(:tap)`); got {
			t.Fatalf("hash with non-callable tap key responds to tap, want false")
		}
		// A data-safe helper and an introspection predicate are never shadowed.
		if got := evalBoolExpr(t, `{"tap": 1}.respond_to?(:nil?)`); !got {
			t.Fatalf("hash with tap key does not respond to nil?, want true")
		}
		if got := evalBoolExpr(t, `{"tap": 1}.respond_to?(:respond_to?)`); !got {
			t.Fatalf("hash with tap key does not respond to respond_to?, want true")
		}
	})

	t.Run("instance tap ivar shadows", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
      class Record
        def initialize
          @tap = 1
        end
      end

      def run()
        Record.new.respond_to?(:tap)
      end
      `)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("instance with @tap ivar responds to tap = %v, want false", got)
		}
	})

	t.Run("class tap class var shadows", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
      class Widget
        @@tap = 1
      end

      def run()
        Widget.respond_to?(:tap)
      end
      `)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("class with @@tap class var responds to tap = %v, want false", got)
		}
	})
}
