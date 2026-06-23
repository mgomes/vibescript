package runtime

import "testing"

// TestTimeEqlPredicate exercises Time#eql? as a Ruby-aligned predicate: equal
// Times compare true, unequal Times compare false, and wrong-type operands
// compare false instead of raising. Wrong arity remains an argument error.
func TestTimeEqlPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "same", expr: `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 1))`, want: NewBool(true)},
		{name: "different", expr: `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 2))`, want: NewBool(false)},
		{name: "int operand", expr: `Time.utc(2024, 1, 1).eql?(1)`, want: NewBool(false)},
		{name: "string operand", expr: `Time.utc(2024, 1, 1).eql?("2024")`, want: NewBool(false)},
		{name: "nil operand", expr: `Time.utc(2024, 1, 1).eql?(nil)`, want: NewBool(false)},
		{name: "duration operand", expr: `Time.utc(2024, 1, 1).eql?(1.hour)`, want: NewBool(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("Time#eql? = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTimeEqlStoredBuiltinPredicate confirms the stored-member-call path (where
// the bound builtin is invoked separately from the receiver) follows the same
// predicate contract as the direct call path.
func TestTimeEqlStoredBuiltinPredicate(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      probe = Time.utc(2024, 1, 1).eql?
      [probe(Time.utc(2024, 1, 1)), probe(Time.utc(2024, 1, 2)), probe(1), probe("2024")]
    end
    `)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{
		NewBool(true),
		NewBool(false),
		NewBool(false),
		NewBool(false),
	})
}

// TestTimeEqlArityError verifies that supplying the wrong number of arguments to
// Time#eql? is still an argument-count error rather than a predicate result.
func TestTimeEqlArityError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
	}{
		{name: "no args", expr: `Time.utc(2024, 1, 1).eql?()`},
		{name: "two args", expr: `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 1), Time.utc(2024, 1, 1))`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "time.eql? expects 1 argument")
		})
	}
}

// TestDurationEqlPredicate exercises Duration#eql? as a predicate: equal
// Durations compare true, unequal Durations compare false, and wrong-type
// operands compare false instead of raising.
func TestDurationEqlPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "same", expr: `1.hour.eql?(1.hour)`, want: NewBool(true)},
		{name: "different", expr: `1.hour.eql?(2.hours)`, want: NewBool(false)},
		{name: "int operand", expr: `1.hour.eql?(1)`, want: NewBool(false)},
		{name: "string operand", expr: `1.hour.eql?("1h")`, want: NewBool(false)},
		{name: "nil operand", expr: `1.hour.eql?(nil)`, want: NewBool(false)},
		{name: "time operand", expr: `1.hour.eql?(Time.utc(2024, 1, 1))`, want: NewBool(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("Duration#eql? = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDurationEqlStoredBuiltinPredicate confirms the stored-member-call path for
// Duration#eql? follows the same predicate contract as the direct call path.
func TestDurationEqlStoredBuiltinPredicate(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      probe = 1.hour.eql?
      [probe(1.hour), probe(2.hours), probe(1), probe("1h")]
    end
    `)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{
		NewBool(true),
		NewBool(false),
		NewBool(false),
		NewBool(false),
	})
}

// TestDurationEqlArityError verifies that supplying the wrong number of
// arguments to Duration#eql? remains an argument-count error.
func TestDurationEqlArityError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
	}{
		{name: "no args", expr: `1.hour.eql?()`},
		{name: "two args", expr: `1.hour.eql?(1.hour, 1.hour)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "duration.eql? expects 1 argument")
		})
	}
}
