package runtime

import "testing"

// The temporal and money kinds expose Ruby-style `to_s`/`string` conversions as
// callable nullary builtins (NewAutoBuiltin), matching the other scalar
// conversions. Resolving them to a bare string during member lookup would let
// the parenless form work but break the explicit nullary-call form
// (`1.hour.string()`), so these tests exercise both forms plus argument
// validation to keep the callable contract from regressing.

// TestTemporalAndMoneyStringConversionForms verifies that to_s and string
// produce the same display form whether invoked parenless or with an explicit
// nullary call.
func TestTemporalAndMoneyStringConversionForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`90.minutes.to_s`, "5400s"},
		{`90.minutes.to_s()`, "5400s"},
		{`90.minutes.string`, "5400s"},
		{`90.minutes.string()`, "5400s"},
		{`Time.utc(2024, 1, 1).to_s`, "2024-01-01T00:00:00Z"},
		{`Time.utc(2024, 1, 1).to_s()`, "2024-01-01T00:00:00Z"},
		{`Time.utc(2024, 1, 1).string`, "2024-01-01T00:00:00Z"},
		{`Time.utc(2024, 1, 1).string()`, "2024-01-01T00:00:00Z"},
		{`money("1.50 USD").to_s`, "1.50 USD"},
		{`money("1.50 USD").to_s()`, "1.50 USD"},
		{`money("1.50 USD").string`, "1.50 USD"},
		{`money("1.50 USD").string()`, "1.50 USD"},
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

// TestTemporalAndMoneyStringConversionStoredBuiltin confirms the conversions
// survive the stored-member-call path: binding the method to a variable and
// calling it later must still yield the string rather than fail as
// non-callable.
func TestTemporalAndMoneyStringConversionStoredBuiltin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`(90.minutes.string)()`, "5400s"},
		{`(Time.utc(2024, 1, 1).to_s)()`, "2024-01-01T00:00:00Z"},
		{`(money("1.50 USD").string)()`, "1.50 USD"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalScalarExpr(t, tc.expr)
			if got.Kind() != KindString || got.String() != tc.want {
				t.Fatalf("%s = %v, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

// TestTemporalAndMoneyStringConversionArgumentRejection verifies that the
// callable conversions reject positional arguments rather than silently
// dropping them.
func TestTemporalAndMoneyStringConversionArgumentRejection(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`90.minutes.to_s(1)`, `90.minutes.string(1)`,
		`Time.utc(2024, 1, 1).to_s(1)`, `Time.utc(2024, 1, 1).string(1)`,
		`money("1.50 USD").to_s(1)`, `money("1.50 USD").string(1)`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take arguments")
		})
	}
}

// TestTemporalAndMoneyStringConversionKeywordRejection verifies that a stray
// keyword argument raises rather than being silently dropped.
func TestTemporalAndMoneyStringConversionKeywordRejection(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`90.minutes.to_s(x: 1)`, `90.minutes.string(x: 1)`,
		`Time.utc(2024, 1, 1).to_s(x: 1)`, `Time.utc(2024, 1, 1).string(x: 1)`,
		`money("1.50 USD").to_s(x: 1)`, `money("1.50 USD").string(x: 1)`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take keyword arguments")
		})
	}
}

// TestTemporalAndMoneyStringConversionBlockRejection verifies that passing a
// block raises rather than being silently ignored.
func TestTemporalAndMoneyStringConversionBlockRejection(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`90.minutes.to_s { 1 }`, `90.minutes.string { 1 }`,
		`Time.utc(2024, 1, 1).to_s { 1 }`, `Time.utc(2024, 1, 1).string { 1 }`,
		`money("1.50 USD").to_s { 1 }`, `money("1.50 USD").string { 1 }`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take a block")
		})
	}
}
