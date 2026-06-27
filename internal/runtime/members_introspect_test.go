package runtime

import "testing"

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
		{"range missing each", `(1..3).respond_to?(:each)`, false},
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

	// Inside the receiver, respond_to? sees private methods even without the
	// include_all flag, matching dispatch visibility. The bare and explicit
	// self forms must agree.
	script := compileScript(t, `
    class User
      def check
        [respond_to?(:secret), self.respond_to?(:secret)]
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
	if len(arr) != 2 || !arr[0].Bool() || !arr[1].Bool() {
		t.Fatalf("respond_to? from self = %v, want [true, true]", arr)
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
