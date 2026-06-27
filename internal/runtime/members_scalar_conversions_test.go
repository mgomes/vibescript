package runtime

import "testing"

// evalScalarExpr compiles and runs an expression as the body of a no-argument
// `run` function, mirroring the helper used by the numeric predicate tests.
func evalScalarExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

func TestScalarToStringConversions(t *testing.T) {
	t.Parallel()

	// to_s and the documented `.string` idiom share one renderer, so each pair
	// must produce the same display form interpolation uses.
	tests := []struct {
		expr string
		want string
	}{
		{`42.to_s`, "42"},
		{`42.string`, "42"},
		{`(-7).to_s`, "-7"},
		{`3.14.to_s`, "3.14"},
		{`3.14.string`, "3.14"},
		{`5.0.to_s`, "5"},
		{`true.to_s`, "true"},
		{`true.string`, "true"},
		{`false.to_s`, "false"},
		{`nil.to_s`, ""},
		{`nil.string`, ""},
		{`"hi".to_s`, "hi"},
		{`"hi".string`, "hi"},
		{`:ok.to_s`, "ok"},
		{`:ok.string`, "ok"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if got.Kind() != KindString {
				t.Fatalf("%s kind = %v, want string", tc.expr, got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

func TestStringToStringReturnsReceiver(t *testing.T) {
	t.Parallel()

	// Ruby's String#to_s returns the receiver itself, so the result is the same
	// string value rather than a copy with new identity.
	for _, expr := range []string{`"vibe".to_s`, `"vibe".string`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, expr)
			if !got.Equal(NewString("vibe")) {
				t.Fatalf("%s = %v, want \"vibe\"", expr, got)
			}
		})
	}
}

func TestNilPredicateAcrossKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{`nil.nil?`, true},
		{`42.nil?`, false},
		{`0.nil?`, false},
		{`3.14.nil?`, false},
		{`true.nil?`, false},
		{`false.nil?`, false},
		{`"".nil?`, false},
		{`"x".nil?`, false},
		{`:ok.nil?`, false},
		{`[].nil?`, false},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

func TestStringNumericConversions(t *testing.T) {
	t.Parallel()

	intTests := []struct {
		expr string
		want int64
	}{
		{`"42".to_i`, 42},
		{`"-7".to_i`, -7},
		{`"  13  ".to_i`, 13},
		{`"+5".to_i`, 5},
	}
	for _, tc := range intTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}

	floatTests := []struct {
		expr string
		want float64
	}{
		{`"3.5".to_f`, 3.5},
		{`"-2.25".to_f`, -2.25},
		{`"  10  ".to_f`, 10},
		{`"1e3".to_f`, 1000},
	}
	for _, tc := range floatTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if !got.Equal(NewFloat(tc.want)) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestStringNumericConversionRejectsInvalid(t *testing.T) {
	t.Parallel()

	// Unlike Ruby's lenient String#to_i/#to_f (which ignore trailing garbage and
	// return 0 on failure), Vibescript parses strictly so a malformed value never
	// silently becomes 0 when crossing a typed boundary.
	tests := []struct {
		expr string
		want string
	}{
		{`"abc".to_i`, "string.to_i expects a base-10 integer string"},
		{`"42abc".to_i`, "string.to_i expects a base-10 integer string"},
		{`"3.5".to_i`, "string.to_i expects a base-10 integer string"},
		{`"".to_i`, "string.to_i expects a numeric string"},
		{`"   ".to_i`, "string.to_i expects a numeric string"},
		{`"abc".to_f`, "string.to_f expects a numeric string"},
		{`"1.2.3".to_f`, "string.to_f expects a numeric string"},
		{`"".to_f`, "string.to_f expects a numeric string"},
		{`"Infinity".to_f`, "string.to_f expects a finite numeric string"},
		{`"NaN".to_f`, "string.to_f expects a finite numeric string"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestNumericToNumericConversions(t *testing.T) {
	t.Parallel()

	intResults := []struct {
		expr string
		want int64
	}{
		{`42.to_i`, 42},
		{`(-7).to_i`, -7},
		{`3.9.to_i`, 3},
		{`(-3.9).to_i`, -3},
		{`5.0.to_i`, 5},
	}
	for _, tc := range intResults {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}

	floatResults := []struct {
		expr string
		want float64
	}{
		{`42.to_f`, 42},
		{`(-7).to_f`, -7},
		{`3.5.to_f`, 3.5},
	}
	for _, tc := range floatResults {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			if !got.Equal(NewFloat(tc.want)) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestFloatToIntRejectsNonFinite(t *testing.T) {
	t.Parallel()

	// Ruby raises FloatDomainError converting NaN/Infinity to an integer; the
	// out-of-range checker reports a clear error rather than truncating garbage.
	for _, expr := range []string{`(1.0 / 0.0).to_i`, `(-1.0 / 0.0).to_i`, `(0.0 / 0.0).to_i`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "float.to_i result out of int64 range")
		})
	}
}

func TestScalarConversionArgumentRejection(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`42.to_s(1)`, `42.string(1)`, `42.to_i(1)`, `42.to_f(1)`, `42.nil?(1)`,
		`3.14.to_s(1)`, `3.14.string(1)`, `3.14.to_i(1)`, `3.14.to_f(1)`, `3.14.nil?(1)`,
		`"x".to_s(1)`, `"x".string(1)`, `"42".to_i(1)`, `"3.5".to_f(1)`, `"x".nil?(1)`,
		`true.to_s(1)`, `true.string(1)`, `true.nil?(1)`,
		`nil.to_s(1)`, `nil.string(1)`, `nil.nil?(1)`,
		`:ok.to_s(1)`, `:ok.string(1)`, `:ok.nil?(1)`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take arguments")
		})
	}
}

func TestScalarConversionKeywordArgumentRejection(t *testing.T) {
	t.Parallel()

	// A stray keyword argument must raise rather than be silently dropped: for
	// example `"42".to_i(base: 16)` must not quietly parse base 10. Covers the
	// conversion/predicate builtins that dispatch through requireNullaryCall.
	exprs := []string{
		`42.to_s(x: 1)`, `42.string(x: 1)`, `42.to_i(x: 1)`, `42.to_f(x: 1)`, `42.nil?(x: 1)`,
		`3.14.to_s(x: 1)`, `3.14.string(x: 1)`, `3.14.to_i(x: 1)`, `3.14.to_f(x: 1)`, `3.14.nil?(x: 1)`,
		`"x".to_s(x: 1)`, `"x".string(x: 1)`, `"42".to_i(base: 16)`, `"3.5".to_f(x: 1)`, `"x".nil?(x: 1)`,
		`"x".to_sym(x: 1)`, `"x".intern(x: 1)`,
		`true.to_s(x: 1)`, `true.string(x: 1)`, `true.nil?(x: 1)`,
		`nil.to_s(x: 1)`, `nil.string(x: 1)`, `nil.nil?(x: 1)`,
		`:ok.to_s(x: 1)`, `:ok.string(x: 1)`, `:ok.id2name(x: 1)`, `:ok.to_sym(x: 1)`, `:ok.nil?(x: 1)`,
		`[].nil?(x: 1)`, `({}).nil?(x: 1)`, `(1..3).nil?(x: 1)`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take keyword arguments")
		})
	}
}

func TestScalarConversionBlockRejection(t *testing.T) {
	t.Parallel()

	// Passing a block to a nullary conversion/predicate must raise rather than
	// silently ignore the block.
	exprs := []string{
		`42.to_s { 1 }`, `42.string { 1 }`, `42.to_i { 1 }`, `42.to_f { 1 }`, `42.nil? { 1 }`,
		`3.14.to_s { 1 }`, `3.14.to_i { 1 }`, `3.14.to_f { 1 }`, `3.14.nil? { 1 }`,
		`"42".to_i { 1 }`, `"3.5".to_f { 1 }`, `"x".to_s { 1 }`, `"x".to_sym { 1 }`, `"x".nil? { 1 }`,
		`true.to_s { 1 }`, `true.nil? { 1 }`,
		`nil.to_s { 1 }`, `nil.nil? { 1 }`,
		`:ok.to_s { 1 }`, `:ok.id2name { 1 }`, `:ok.to_sym { 1 }`, `:ok.nil? { 1 }`,
		`[].nil? { 1 }`, `({}).nil? { 1 }`, `(1..3).nil? { 1 }`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take a block")
		})
	}
}

func TestAggregateKindsRejectToStringConversion(t *testing.T) {
	t.Parallel()

	// Arrays, hashes, and ranges deliberately do not expose to_s/string because
	// their rendering can be unbounded; only inspect (which charges the memory
	// quota) and nil? are available. This guards the docs/changelog claim that the
	// scalar string conversions are not universal.
	tests := []struct {
		expr string
		want string
	}{
		{`[1, 2].to_s`, "unknown array method to_s"},
		{`[1, 2].string`, "unknown array method string"},
		{`({a: 1}).to_s`, "unknown hash"},
		{`({a: 1}).string`, "unknown hash"},
		{`(1..3).to_s`, "unknown range method to_s"},
		{`(1..3).string`, "unknown range method string"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestAggregateKindsAnswerNilPredicate(t *testing.T) {
	t.Parallel()

	// nil? is the one universal method arrays, hashes, and ranges gained; each is
	// a non-nil value so the predicate is always false.
	for _, expr := range []string{`[1, 2].nil?`, `({a: 1}).nil?`, `(1..3).nil?`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", expr, got.Kind())
			}
			if got.Bool() {
				t.Fatalf("%s = true, want false", expr)
			}
		})
	}
}

func TestTypedBoundaryStringConversion(t *testing.T) {
	t.Parallel()

	// The docs/typing.md migration example converts a union argument to a string
	// at a typed boundary with `.string`; exercise both branches of the union.
	script := compileScript(t, `
    def normalize_id(id: int | string | nil) -> string
      if id == nil
        "unknown"
      else
        id.string
      end
    end
    `)
	cases := []struct {
		arg  Value
		want string
	}{
		{NewInt(42), "42"},
		{NewString("abc"), "abc"},
		{NewNil(), "unknown"},
	}
	for _, tc := range cases {
		got := callFunc(t, script, "normalize_id", []Value{tc.arg})
		if got.Kind() != KindString || got.String() != tc.want {
			t.Fatalf("normalize_id(%v) = %v, want %q", tc.arg, got, tc.want)
		}
	}
}
