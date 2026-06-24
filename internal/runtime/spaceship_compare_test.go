package runtime

import "testing"

// evalExpr compiles a single expression inside a run function and returns its
// value, failing the test on any compile or call error.
func evalExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

// TestSpaceshipIncomparableReturnsNil verifies that `<=>` yields nil for
// operand pairs that cannot be ordered, matching Ruby's spaceship contract.
func TestSpaceshipIncomparableReturnsNil(t *testing.T) {
	t.Parallel()

	exprs := []struct {
		name string
		expr string
	}{
		{"int vs string", `1 <=> "a"`},
		{"string vs int", `"a" <=> 1`},
		{"float vs string", `1.0 <=> "a"`},
		{"array vs int", `[1] <=> 1`},
		{"int vs array", `1 <=> [1]`},
		{"int vs nil", `1 <=> nil`},
		{"int vs bool", `1 <=> true`},
		{"string vs symbol", `"a" <=> :a`},
		{"hash vs hash", `{a: 1} <=> {a: 1}`},
		{"time vs int", `Time.utc(2024, 1, 1) <=> 1`},
		{"int vs time", `1 <=> Time.utc(2024, 1, 1)`},
		{"duration vs int", `3.seconds <=> 3`},
		{"money diff currency", `money("10.00 USD") <=> money("10.00 EUR")`},
		{"money vs int", `money("10.00 USD") <=> 5`},
		{"time member call wrong type", `Time.utc(2024, 1, 1).<=>(1)`},
	}

	for _, tc := range exprs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalExpr(t, tc.expr)
			if got.Kind() != KindNil {
				t.Fatalf("%s = %v (kind %v), want nil", tc.expr, got, got.Kind())
			}
		})
	}
}

// TestSpaceshipComparableReturnsOrder verifies that comparable operands keep
// producing -1/0/1, including mixed int/float pairs that Ruby orders.
func TestSpaceshipComparableReturnsOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"int less", `1 <=> 2`, -1},
		{"int equal", `2 <=> 2`, 0},
		{"int greater", `3 <=> 2`, 1},
		{"float vs int equal", `1.0 <=> 1`, 0},
		{"int vs float less", `1 <=> 1.5`, -1},
		{"float vs int greater", `2.5 <=> 1`, 1},
		{"string less", `"a" <=> "b"`, -1},
		{"string equal", `"a" <=> "a"`, 0},
		{"money same currency less", `money("10.00 USD") <=> money("20.00 USD")`, -1},
		{"money same currency equal", `money("10.00 USD") <=> money("10.00 USD")`, 0},
		{"duration less", `3.seconds <=> 5.seconds`, -1},
		{"duration greater", `5.minutes <=> 5.seconds`, 1},
		{"time less", `Time.utc(2024, 1, 1) <=> Time.utc(2024, 1, 2)`, -1},
		{"time equal", `Time.utc(2024, 1, 1) <=> Time.utc(2024, 1, 1)`, 0},
		{"time member call", `Time.utc(2024, 1, 2).<=>(Time.utc(2024, 1, 1))`, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalExpr(t, tc.expr)
			if got.Kind() != KindInt {
				t.Fatalf("%s = %v (kind %v), want int %d", tc.expr, got, got.Kind(), tc.want)
			}
			if got.Int() != tc.want {
				t.Fatalf("%s = %d, want %d", tc.expr, got.Int(), tc.want)
			}
		})
	}
}

// TestRelationalIncomparableRaises verifies that the relational operators keep
// raising on incomparable operands, matching Ruby's ArgumentError instead of
// the spaceship operator's nil result.
func TestRelationalIncomparableRaises(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"int lt string", `1 < "a"`, "unsupported comparison operands"},
		{"string gt int", `"a" > 1`, "unsupported comparison operands"},
		{"time lte int", `Time.utc(2024, 1, 1) <= 1`, "unsupported comparison operands"},
		{"int gte time", `1 >= Time.utc(2024, 1, 1)`, "unsupported comparison operands"},
		{"money diff currency lt", `money("10.00 USD") < money("10.00 EUR")`, "money currency mismatch for comparison"},
		{"money diff currency gte", `money("10.00 USD") >= money("10.00 EUR")`, "money currency mismatch for comparison"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestTimeCompareMemberContract covers Time#<=> argument validation alongside
// its nil-on-incomparable result so the member call stays aligned with the
// operator path.
func TestTimeCompareMemberContract(t *testing.T) {
	t.Parallel()

	t.Run("nil for non-time argument", func(t *testing.T) {
		t.Parallel()
		got := evalExpr(t, `Time.utc(2024, 1, 1).<=>("nope")`)
		if got.Kind() != KindNil {
			t.Fatalf("Time#<=> with string = %v (kind %v), want nil", got, got.Kind())
		}
	})

	t.Run("wrong argument count raises", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, "def run()\n  Time.utc(2024, 1, 1).<=>(Time.utc(2024,1,1), Time.utc(2024,1,2))\nend")
		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "time.<=> expects 1 argument")
	})
}

// TestTimeEqlWrongTypeReturnsFalse verifies Time#eql? matches Ruby's predicate
// behavior of returning false for a non-Time argument rather than raising.
func TestTimeEqlWrongTypeReturnsFalse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"int argument", `Time.utc(2024, 1, 1).eql?(1)`, false},
		{"string argument", `Time.utc(2024, 1, 1).eql?("x")`, false},
		{"equal time", `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 1))`, true},
		{"unequal time", `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 2))`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s = %v (kind %v), want bool %t", tc.expr, got, got.Kind(), tc.want)
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %t, want %t", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// TestSpaceshipIncomparableDirectOrder exercises compareValueOrder so the
// incomparable sentinel and money mismatch keep flowing through isIncomparable.
func TestSpaceshipIncomparableDirectOrder(t *testing.T) {
	t.Parallel()

	usd := mustMoneyValue(t, "10.00 USD")
	eur := mustMoneyValue(t, "10.00 EUR")

	tests := []struct {
		name  string
		left  Value
		right Value
	}{
		{"int vs string", NewInt(1), NewString("a")},
		{"money currency mismatch", usd, eur},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := compareValueOrder(tc.left, tc.right)
			if err == nil {
				t.Fatalf("compareValueOrder(%v, %v) error = nil, want incomparable", tc.left, tc.right)
			}
			if !isIncomparable(err) {
				t.Fatalf("isIncomparable(%v) = false, want true", err)
			}
		})
	}
}
