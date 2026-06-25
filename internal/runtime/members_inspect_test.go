package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// evalInspect compiles and runs a one-line expression and returns its result.
func evalInspect(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

func TestInspectAcrossKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"nil", "nil.inspect", "nil"},
		{"bool_true", "true.inspect", "true"},
		{"bool_false", "false.inspect", "false"},
		{"int", "42.inspect", "42"},
		{"negative_int", "(-7).inspect", "-7"},
		{"float", "(2.5).inspect", "2.5"},
		{"symbol", ":ok.inspect", ":ok"},
		{"symbol_predicate_name", ":even?.inspect", `:"even?"`},
		{"empty_string", `"".inspect`, `""`},
		{"plain_string", `"hello".inspect`, `"hello"`},
		{"string_with_newline", `"a\nb".inspect`, `"a\nb"`},
		{"string_with_tab", `"a\tb".inspect`, `"a\tb"`},
		{"string_with_quote", `"say \"hi\"".inspect`, `"say \"hi\""`},
		{"string_with_backslash", `"a\\b".inspect`, `"a\\b"`},
		{"array_mixed", `[1, "x", nil].inspect`, `[1, "x", nil]`},
		{"array_of_symbols", `[:a, :b].inspect`, `[:a, :b]`},
		{"nested_array", `[1, ["two"], :ok].inspect`, `[1, ["two"], :ok]`},
		{"empty_array", `[].inspect`, `[]`},
		{"single_entry_hash", `{a: 1}.inspect`, `{a: 1}`},
		{"hash_string_value", `{a: "x"}.inspect`, `{a: "x"}`},
		{"hash_nested", `{items: [1, "x"]}.inspect`, `{items: [1, "x"]}`},
		{"empty_hash", `{}.inspect`, `{}`},
		{"quoted_key_hash", `{"a b": 1}.inspect`, `{"a b": 1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalInspect(t, tc.expr)
			if got.Kind() != KindString {
				t.Fatalf("%s kind = %v, want string", tc.expr, got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// TestInspectDistinctFromToS confirms inspect adds quoting that the to_s/output
// rendering omits: a string interpolated directly renders as its raw text, while
// inspect double-quotes and escapes it.
func TestInspectDistinctFromToS(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  s = "a\nb"
  [s, s.inspect]
end`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindArray {
		t.Fatalf("kind = %v, want array", got.Kind())
	}
	arr := got.Array()
	if len(arr) != 2 {
		t.Fatalf("len = %d, want 2", len(arr))
	}
	if raw := arr[0].String(); raw != "a\nb" {
		t.Fatalf("raw string = %q, want %q", raw, "a\nb")
	}
	if inspected := arr[1].String(); inspected != `"a\nb"` {
		t.Fatalf("inspected = %q, want %q", inspected, `"a\nb"`)
	}
}

func TestInspectRejectsArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"positional_arg", `"x".inspect(1)`, "string.inspect does not take arguments"},
		{"keyword_arg", `"x".inspect(pretty: true)`, "string.inspect does not take keyword arguments"},
		{"block", `[1].inspect { |x| x }`, "array.inspect does not take a block"},
		{"int_arg", `1.inspect(2)`, "int.inspect does not take arguments"},
		{"symbol_arg", `:ok.inspect(1)`, "symbol.inspect does not take arguments"},
		{"nil_arg", `nil.inspect(1)`, "nil.inspect does not take arguments"},
		{"bool_arg", `true.inspect(1)`, "bool.inspect does not take arguments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestInspectUnknownScalarMemberSuggests confirms an unknown member on the new
// scalar kinds yields a helpful "did you mean" error rather than the generic
// "unsupported member access" message they returned before inspect was added.
func TestInspectUnknownScalarMemberSuggests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"symbol", ":ok.inspct", `unknown symbol method inspct (did you mean "inspect"?)`},
		{"nil", "nil.inspct", `unknown nil method inspct (did you mean "inspect"?)`},
		{"bool", "true.inspct", `unknown bool method inspct (did you mean "inspect"?)`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestInspectLargeCompositeTripsMemoryQuota confirms inspect projects its output
// size and rejects a composite whose rendering would exceed the memory quota,
// instead of allocating the oversized string first and only then failing. The
// builtin is invoked directly on an Execution so the projected-size check is
// exercised in isolation from the call-binding memory checks.
func TestInspectLargeCompositeTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	builtin := valueBuiltin(newInspectBuiltin("array"))
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: 4096,
	}

	// The rendered array (each element a 64-byte string with quotes and the ", "
	// separators) far exceeds the 4 KiB quota, so the projected-size check rejects
	// the call before the output string is materialized.
	elems := make([]Value, 1000)
	for i := range elems {
		elems[i] = NewString(strings.Repeat("x", 64))
	}
	_, err := builtin.Fn(exec, NewArray(elems), nil, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestInspectChargesReceiverFootprint confirms the projection counts the
// receiver's own footprint, not just the inspected string. The receiver stays
// live while inspect materializes its rendering, so an ephemeral receiver whose
// structural footprint dwarfs its small rendering (here many empty strings, each
// rendering as two bytes but costing a Value slot and string header) could slip
// past a payload-only check while base+receiver+result actually exceeds the
// quota. The quota is pinned to the exact payload-only projection: the old
// behavior would have admitted it, the receiver-aware projection rejects it.
func TestInspectChargesReceiverFootprint(t *testing.T) {
	t.Parallel()

	builtin := valueBuiltin(newInspectBuiltin("array"))

	elems := make([]Value, 200)
	for i := range elems {
		elems[i] = NewString("")
	}
	receiver := NewArray(elems)

	// Use a quota-disabled exec only to measure the projection terms with the
	// same estimator the check uses; the assertion exec gets the pinned quota.
	measure := &Execution{ctx: context.Background()}
	base := measure.estimateMemoryUsage()
	receiverFootprint := measure.estimateMemoryUsage(receiver) - base
	if receiverFootprint <= 0 {
		t.Fatalf("receiver footprint = %d, want > 0", receiverFootprint)
	}

	payload, err := receiver.InspectByteLenBounded(func() error { return nil })
	if err != nil {
		t.Fatalf("InspectByteLenBounded() error = %v", err)
	}

	// payloadOnly is what the old projection charged: base plus the result
	// string's header and bytes, ignoring the still-live receiver.
	payloadOnly := base + estimatedValueBytes + estimatedStringHeaderBytes + payload

	// At quota == payloadOnly the payload-only check passes (used is not strictly
	// greater) while the receiver-aware check exceeds it by the receiver's whole
	// footprint, so inspect must now reject.
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: payloadOnly,
	}
	if _, err := builtin.Fn(exec, receiver, nil, nil, NewNil()); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("inspect at payload-only quota error = %v, want %v", err, errMemoryQuotaExceeded)
	}

	// Raising the quota to cover the receiver footprint too lets the same call
	// succeed, proving the rejection above was the receiver charge and not an
	// unrelated over-count.
	exec.memoryQuota = payloadOnly + receiverFootprint
	if _, err := builtin.Fn(exec, receiver, nil, nil, NewNil()); err != nil {
		t.Fatalf("inspect at receiver-aware quota error = %v, want nil", err)
	}
}

// TestInspectStepBudgetAbortsProjection confirms the projection walk charges the
// step quota, so a composite whose rendering is bounded but whose shared-acyclic
// graph forces an exponential re-walk trips the step quota instead of burning
// unbounded CPU.
func TestInspectStepBudgetAbortsProjection(t *testing.T) {
	t.Parallel()

	builtin := valueBuiltin(newInspectBuiltin("array"))
	exec := &Execution{
		ctx:         context.Background(),
		quota:       64,
		memoryQuota: 1 << 30,
	}

	// Each level holds two references to the level below, so the projection walk
	// is exponential in depth even though the value is acyclic and small.
	v := NewArray([]Value{NewInt(0)})
	for range 40 {
		v = NewArray([]Value{v, v})
	}
	_, err := builtin.Fn(exec, v, nil, nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

// TestInspectReservedAsHashMethod confirms a stored key named "inspect" does not
// shadow the method on dot access (the method wins, matching other reserved hash
// method names), while index access still reaches the stored value.
func TestInspectReservedAsHashMethod(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  h = { inspect: "stored" }
  [h.inspect, h[:inspect]]
end`)
	got := callFunc(t, script, "run", nil)
	arr := got.Array()
	if len(arr) != 2 {
		t.Fatalf("len = %d, want 2", len(arr))
	}
	if method := arr[0].String(); method != `{inspect: "stored"}` {
		t.Fatalf("h.inspect = %q, want %q", method, `{inspect: "stored"}`)
	}
	if stored := arr[1].String(); stored != "stored" {
		t.Fatalf("h[:inspect] = %q, want %q", stored, "stored")
	}
}
