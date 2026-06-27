package runtime

import "testing"

// TestCaseEqualityOperator verifies the `===` operator implements Ruby's case
// equality contract: the left operand is the matcher, ranges check membership,
// and every other value falls back to `==`.
func TestCaseEqualityOperator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"scalar int equal", `1 === 1`, true},
		{"scalar int unequal", `1 === 2`, false},
		{"scalar float equal", `1.5 === 1.5`, true},
		// `===` mirrors Vibescript's `==`, which treats int and float as distinct
		// kinds, so `1 === 1.0` is false even though Ruby reports true. The
		// scalar fallback intentionally tracks `==` rather than diverging.
		{"scalar int vs float not equal", `1 === 1.0`, false},
		{"scalar string equal", `"a" === "a"`, true},
		{"scalar string unequal", `"a" === "b"`, false},
		{"scalar bool equal", `true === true`, true},
		{"scalar nil equal", `nil === nil`, true},
		{"scalar symbol equal", `:a === :a`, true},
		{"array equal", `[1, 2] === [1, 2]`, true},
		{"array unequal", `[1, 2] === [1, 3]`, false},
		{"range contains int", `(1..3) === 2`, true},
		{"range contains start", `(1..3) === 1`, true},
		{"range contains end inclusive", `(1..3) === 3`, true},
		{"range excludes beyond end", `(1..3) === 4`, false},
		{"range excludes below start", `(1..3) === 0`, false},
		{"exclusive range excludes end", `(1...3) === 3`, false},
		{"exclusive range contains interior", `(1...3) === 2`, true},
		{"range contains float interior", `(1..3) === 2.5`, true},
		{"range excludes float beyond end", `(1..3) === 3.5`, false},
		{"value lhs is not a matcher", `2 === (1..3)`, false},
		{"range vs range uses equality", `(1..3) === (1..3)`, true},
		{"range vs different range", `(1..3) === (1..4)`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s = %v (kind %v), want bool", tc.expr, got, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// TestCaseEqualityMatchesWhenClause asserts that `pattern === value` returns the
// same result as the equivalent `case` clause, since both must share Ruby's case
// equality semantics.
func TestCaseEqualityMatchesWhenClause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matcher string
		value   string
	}{
		{"range hit", "1..3", "2"},
		{"range miss", "1..3", "5"},
		{"exclusive range boundary", "1...3", "3"},
		{"scalar hit", "7", "7"},
		{"scalar miss", "7", "8"},
		{"string hit", `"x"`, `"x"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			operatorResult := evalExpr(t, "("+tc.matcher+") === "+tc.value)

			caseSource := "def run()\n" +
				"  case " + tc.value + "\n" +
				"  when " + tc.matcher + " then true\n" +
				"  else false\n" +
				"  end\n" +
				"end"
			caseResult := callFunc(t, compileScript(t, caseSource), "run", nil)

			if operatorResult.Bool() != caseResult.Bool() {
				t.Fatalf("operator %q = %v, case clause = %v; want equal",
					tc.matcher+" === "+tc.value, operatorResult.Bool(), caseResult.Bool())
			}
		})
	}
}

func TestCaseWhenSplatValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def match_value(value)
  case value
  when *[1, 2], 3
    "number"
  when *["x", "y"]
    "letter"
  else
    "miss"
  end
end

def match_truthy
  case
  when *[false, nil]
    "miss"
  when *[nil, true]
    "hit"
  end
end

def bad_splat
  case 1
  when *1
    "bad"
  end
end
`)

	tests := []struct {
		name string
		arg  Value
		want Value
	}{
		{name: "splatted int", arg: NewInt(2), want: NewString("number")},
		{name: "direct trailing value", arg: NewInt(3), want: NewString("number")},
		{name: "splatted string", arg: NewString("y"), want: NewString("letter")},
		{name: "miss", arg: NewString("z"), want: NewString("miss")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, "match_value", []Value{tc.arg}); !got.Equal(tc.want) {
				t.Fatalf("match_value(%v) = %v, want %v", tc.arg, got, tc.want)
			}
		})
	}

	if got := callFunc(t, script, "match_truthy", nil); !got.Equal(NewString("hit")) {
		t.Fatalf("match_truthy = %v, want hit", got)
	}
	requireCallErrorContains(t, script, "bad_splat", nil, CallOptions{}, "case when splat value must be an array")
}
