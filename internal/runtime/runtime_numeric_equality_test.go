package runtime

import (
	"context"
	"testing"
)

// `1 == 1.0` was false, which contradicted the documented split between == and
// eql? (docs/stdlib_core_utilities.md introduces eql? as the kind-strict one,
// "so 1.eql?(1.0) is false even though both are numerically one"). It also
// disagreed with <=>, which already reported 1 <=> 1.0 as 0, so sorting and
// equality could reach opposite conclusions about the same pair.
func TestNumericEqualityComparesAcrossIntAndFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "int equals float", expr: "1 == 1.0", want: true},
		{name: "float equals int", expr: "1.0 == 1", want: true},
		{name: "fractional float is not an int", expr: "1 == 1.5", want: false},
		{name: "computed average", expr: "([1, 2, 3].sum / 3.0) == 2", want: true},
		{name: "integer division against float", expr: "(4 / 2) == (4.0 / 2)", want: true},

		// eql? keeps the kind gate, which is the distinction the docs draw and
		// what hash keys rely on.
		{name: "eql stays kind strict", expr: "1.eql?(1.0)", want: false},
		{name: "eql matches same kind", expr: "1.eql?(1)", want: true},

		// Consistency with the operators that already coerced.
		{name: "spaceship agrees", expr: "(1 <=> 1.0) == 0", want: true},
		{name: "case equality agrees", expr: "1 === 1.0", want: true},
		{name: "array membership agrees", expr: "[1, 2].include?(2.0)", want: true},
		{name: "range membership agrees", expr: "(1..3).include?(2.0)", want: true},

		// Composites compare element-wise through the same rule.
		{name: "arrays compare numerically", expr: "[1] == [1.0]", want: true},
		{name: "nested arrays", expr: "[[1]] == [[1.0]]", want: true},

		// Non-finite floats equal no integer.
		{name: "nan equals nothing", expr: "(0.0 / 0.0) == 1", want: false},
		{name: "infinity is not an int", expr: "(1.0 / 0.0) == 1", want: false},

		// Above 2^53 a float64 cannot represent every int64, so the comparison
		// runs exactly rather than converting the integer to float.
		{name: "large int does not equal a nearby float", expr: "9007199254740993 == 9007199254740992.0", want: false},
		{name: "large int equals its own float", expr: "9007199254740992 == 9007199254740992.0", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.Kind() != KindBool {
				t.Fatalf("%s returned %v, want a bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// Hash keys use eql?, so an int and a float key stay distinct even though they
// now compare equal under ==. That separation is the reason eql? exists.
// Numeric keys are rejected outright, so the int/float distinction `eql?` draws
// never reaches a hash. Converting with to_s is the documented migration, and
// numbers that render differently stay different keys.
func TestNumericHashKeysAreRejected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      h = {}
      h[1] = "int"
      h.keys.length
    end

    def converted()
      h = {}
      h[1.to_s] = "one"
      h[1.5.to_s] = "one and a half"
      h.keys.length
    end
    `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{},
		"hash keys must be strings or symbols")
	if got := callFunc(t, script, "converted", nil); got.Int() != 2 {
		t.Fatalf("converted key count = %d, want 2", got.Int())
	}
}
