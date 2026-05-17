package runtime

import (
	"testing"
)

func TestLogicalOperatorsShortCircuit(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def bad_index
      [1][4]
    end

    def explode
      raise "boom"
    end

    def false_and_bad_index
      false && bad_index
    end

    def true_or_explode
      true || explode
    end

    def adjacent_run(values, index)
      index + 1 < values.size && values[index + 1] == values[index] + 1
    end
    `)

	cases := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{name: "false_and_bad_index_short_circuits", fn: "false_and_bad_index", want: NewBool(false)},
		{name: "true_or_explode_short_circuits", fn: "true_or_explode", want: NewBool(true)},
		{name: "adjacent_run_single", fn: "adjacent_run", args: []Value{NewArray([]Value{NewInt(5)}), NewInt(0)}, want: NewBool(false)},
		{name: "adjacent_run_pair", fn: "adjacent_run", args: []Value{NewArray([]Value{NewInt(5), NewInt(6)}), NewInt(0)}, want: NewBool(true)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, tc.args)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.fn, got, tc.want)
			}
		})
	}
}

func TestIntegerDivisionAndModulo(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def gcd(a, b)
      while b != 0
        next_value = a % b
        a = b
        b = next_value
      end
      a
    end

    def hailstone(n)
      out = [n]
      while n != 1
        if n % 2 == 0
          n = n / 2
        else
          n = n * 3 + 1
        end
        out = out + [n]
      end
      out
    end

    def arithmetic
      {
        int_div: 7 / 2,
        neg_div_left: -7 / 2,
        neg_div_right: 7 / -2,
        neg_div_both: -7 / -2,
        float_div: 7.0 / 2,
        mod_chain: 10 / 2 % 3,
        neg_mod_left: -7 % 2,
        neg_mod_right: 7 % -2,
        gcd: gcd(54, 24),
        hailstone: hailstone(7)
      }
    end
    `)

	result := callFunc(t, script, "arithmetic", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["int_div"].Equal(NewInt(3)) {
		t.Fatalf("int_div mismatch: %v", got["int_div"])
	}
	if !got["neg_div_left"].Equal(NewInt(-4)) || !got["neg_div_right"].Equal(NewInt(-4)) || !got["neg_div_both"].Equal(NewInt(3)) {
		t.Fatalf("negative division mismatch: left=%v right=%v both=%v", got["neg_div_left"], got["neg_div_right"], got["neg_div_both"])
	}
	if got["float_div"].Kind() != KindFloat || got["float_div"].Float() != 3.5 {
		t.Fatalf("float_div mismatch: %v", got["float_div"])
	}
	if !got["mod_chain"].Equal(NewInt(2)) {
		t.Fatalf("mod_chain mismatch: %v", got["mod_chain"])
	}
	if !got["neg_mod_left"].Equal(NewInt(1)) || !got["neg_mod_right"].Equal(NewInt(-1)) {
		t.Fatalf("negative modulo mismatch: left=%v right=%v", got["neg_mod_left"], got["neg_mod_right"])
	}
	if !got["gcd"].Equal(NewInt(6)) {
		t.Fatalf("gcd mismatch: %v", got["gcd"])
	}
	compareArrays(t, got["hailstone"], []Value{
		NewInt(7), NewInt(22), NewInt(11), NewInt(34), NewInt(17), NewInt(52),
		NewInt(26), NewInt(13), NewInt(40), NewInt(20), NewInt(10), NewInt(5),
		NewInt(16), NewInt(8), NewInt(4), NewInt(2), NewInt(1),
	})
}

func TestNumericConversionBuiltins(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def conversions()
      {
        int_from_int: to_int(5),
        int_from_float: to_int(5.0),
        int_from_string: to_int("42"),
        float_from_int: to_float(5),
        float_from_float: to_float(1.25),
        float_from_string: to_float("2.5")
      }
    end

    def bad_int_fraction()
      to_int(1.5)
    end

    def bad_int_string()
      to_int("abc")
    end

	    def bad_float_string()
	      to_float("abc")
	    end

	    def bad_float_nan()
	      to_float("NaN")
	    end

	    def bad_float_inf()
	      to_float("Inf")
	    end
	    `)

	result := callFunc(t, script, "conversions", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["int_from_int"].Equal(NewInt(5)) || !got["int_from_float"].Equal(NewInt(5)) || !got["int_from_string"].Equal(NewInt(42)) {
		t.Fatalf("to_int conversions mismatch: %#v", got)
	}
	if got["float_from_int"].Kind() != KindFloat || got["float_from_int"].Float() != 5 {
		t.Fatalf("float_from_int mismatch: %v", got["float_from_int"])
	}
	if got["float_from_float"].Kind() != KindFloat || got["float_from_float"].Float() != 1.25 {
		t.Fatalf("float_from_float mismatch: %v", got["float_from_float"])
	}
	if got["float_from_string"].Kind() != KindFloat || got["float_from_string"].Float() != 2.5 {
		t.Fatalf("float_from_string mismatch: %v", got["float_from_string"])
	}

	badCases := []struct {
		name string
		fn   string
		want string
	}{
		{name: "to_int_fraction", fn: "bad_int_fraction", want: "to_int cannot convert non-integer float"},
		{name: "to_int_string", fn: "bad_int_string", want: "to_int expects a base-10 integer string"},
		{name: "to_float_string", fn: "bad_float_string", want: "to_float expects a numeric string"},
		{name: "to_float_nan", fn: "bad_float_nan", want: "to_float expects a finite numeric string"},
		{name: "to_float_inf", fn: "bad_float_inf", want: "to_float expects a finite numeric string"},
	}
	for _, tc := range badCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestNumericHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def int_helpers()
      {
        abs_neg: (-7).abs,
        abs_pos: 7.abs,
        clamp_mid: 7.clamp(1, 10),
        clamp_low: (-2).clamp(0, 5),
        clamp_high: 12.clamp(0, 5),
        even_true: 4.even?,
        even_false: 5.even?,
        odd_true: 5.odd?,
        odd_false: 4.odd?
      }
    end

    def float_helpers()
      {
        abs_neg: (-1.25).abs,
        clamp_mid: 1.5.clamp(1.0, 2.0),
        clamp_low: (-1.0).clamp(0.5, 2.0),
        clamp_high: 3.5.clamp(0.5, 2.0),
        round: 1.6.round,
        floor: 1.6.floor,
        ceil: 1.2.ceil
      }
    end
    `)

	intResult := callFunc(t, script, "int_helpers", nil)
	if intResult.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", intResult.Kind())
	}
	ints := intResult.Hash()
	if !ints["abs_neg"].Equal(NewInt(7)) {
		t.Fatalf("abs_neg mismatch: %v", ints["abs_neg"])
	}
	if !ints["abs_pos"].Equal(NewInt(7)) {
		t.Fatalf("abs_pos mismatch: %v", ints["abs_pos"])
	}
	if !ints["clamp_mid"].Equal(NewInt(7)) {
		t.Fatalf("clamp_mid mismatch: %v", ints["clamp_mid"])
	}
	if !ints["clamp_low"].Equal(NewInt(0)) {
		t.Fatalf("clamp_low mismatch: %v", ints["clamp_low"])
	}
	if !ints["clamp_high"].Equal(NewInt(5)) {
		t.Fatalf("clamp_high mismatch: %v", ints["clamp_high"])
	}
	if !ints["even_true"].Bool() || ints["even_false"].Bool() {
		t.Fatalf("even? mismatch: true=%v false=%v", ints["even_true"], ints["even_false"])
	}
	if !ints["odd_true"].Bool() || ints["odd_false"].Bool() {
		t.Fatalf("odd? mismatch: true=%v false=%v", ints["odd_true"], ints["odd_false"])
	}

	floatResult := callFunc(t, script, "float_helpers", nil)
	if floatResult.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", floatResult.Kind())
	}
	floats := floatResult.Hash()
	if floats["abs_neg"].Kind() != KindFloat || floats["abs_neg"].Float() != 1.25 {
		t.Fatalf("float abs mismatch: %v", floats["abs_neg"])
	}
	if floats["clamp_mid"].Kind() != KindFloat || floats["clamp_mid"].Float() != 1.5 {
		t.Fatalf("float clamp mid mismatch: %v", floats["clamp_mid"])
	}
	if floats["clamp_low"].Kind() != KindFloat || floats["clamp_low"].Float() != 0.5 {
		t.Fatalf("float clamp low mismatch: %v", floats["clamp_low"])
	}
	if floats["clamp_high"].Kind() != KindFloat || floats["clamp_high"].Float() != 2.0 {
		t.Fatalf("float clamp high mismatch: %v", floats["clamp_high"])
	}
	if !floats["round"].Equal(NewInt(2)) {
		t.Fatalf("float round mismatch: %v", floats["round"])
	}
	if !floats["floor"].Equal(NewInt(1)) {
		t.Fatalf("float floor mismatch: %v", floats["floor"])
	}
	if !floats["ceil"].Equal(NewInt(2)) {
		t.Fatalf("float ceil mismatch: %v", floats["ceil"])
	}
}
