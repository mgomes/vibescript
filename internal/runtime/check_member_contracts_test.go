package runtime

import (
	"fmt"
	"testing"
)

// Scalar member contracts resolve from inferred receiver facts, not only
// literal receivers, and universal predicates carry fixed boolean results
// when no receiver arm can override universal dispatch.

func TestCheckFixedScalarConversionInventory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		params     string
		expression string
		result     string
		mismatch   string
		autoInvoke bool
	}{
		{name: "nil to_s", expression: "nil.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "nil string", expression: "nil.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "bool to_s", expression: "true.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "bool string", expression: "true.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol id2name", expression: ":ok.id2name", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol to_s", expression: ":ok.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol string", expression: ":ok.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol to_sym", expression: ":ok.to_sym", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "string to_i", params: "value: string", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "string to_f", params: "value: string", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "string to_s", params: "value: string", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "string string", params: "value: string", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "string to_sym", params: "value: string", expression: "value.to_sym", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "string intern", params: "value: string", expression: "value.intern", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "int to_i", params: "value: int", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "int to_f", params: "value: int", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "int to_s", params: "value: int", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "int string", params: "value: int", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "float to_i", params: "value: float", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "float to_f", params: "value: float", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "float to_s", params: "value: float", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "float string", params: "value: float", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "money to_s", params: "value: money", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "money string", params: "value: money", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "duration to_i", params: "value: duration", expression: "value.to_i", result: "int", mismatch: "string"},
		{name: "duration to_s", params: "value: duration", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "duration string", params: "value: duration", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time to_i", params: "value: time", expression: "value.to_i", result: "int", mismatch: "string"},
		{name: "time tv_sec", params: "value: time", expression: "value.tv_sec", result: "int", mismatch: "string"},
		{name: "time to_f", params: "value: time", expression: "value.to_f", result: "float", mismatch: "string"},
		{name: "time to_r", params: "value: time", expression: "value.to_r", result: "float", mismatch: "string"},
		{name: "time to_s", params: "value: time", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time string", params: "value: time", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time to_a", params: "value: time", expression: "value.to_a", result: "array", mismatch: "string"},
		{name: "range to_a", params: "value: range", expression: "value.to_a", result: "array<int>", mismatch: "string", autoInvoke: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expressions := []string{tc.expression}
			if tc.autoInvoke {
				expressions = append(expressions, tc.expression+"()")
			}
			for _, expression := range expressions {
				matching := fmt.Sprintf(`
def accept(value: %s)
  value
end

def run(%s)
  accept(%s)
end
`, tc.result, tc.params, expression)
				requireNoCheckWarnings(t, compileScriptDefault(t, matching))

				contradiction := fmt.Sprintf(`
def reject(value: %s)
  value
end

def run(%s)
  reject(%s)
end
`, tc.mismatch, tc.params, expression)
				requireCheckWarningContains(
					t,
					compileScriptDefault(t, contradiction),
					fmt.Sprintf("call to reject argument value expected %s, got %s", tc.mismatch, tc.result),
				)
			}
		})
	}
}

func TestCheckNullableShapeNilDispatchMatchesRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		expression string
	}{
		{name: "bare", expression: "value.to_s"},
		{name: "call", expression: "value.to_s()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, fmt.Sprintf(`
def takes_int(value: int)
  value
end

def checked(value: { name: string }?)
  takes_int(%s)
end

def render(value: { name: string }?)
  %s
end
`, tc.expression, tc.expression))
			requireCheckWarningContains(
				t,
				script,
				"call to takes_int argument value expected int, got string",
			)

			got := callFunc(t, script, "render", []Value{NewNil()})
			if got.Kind() != KindString || got.String() != "" {
				t.Errorf("render(nil) = %#v, want empty string", got)
			}

			shape := NewHashWithCapacity(1)
			if err := shape.HashSet(NewSymbol("name"), NewString("Ada")); err != nil {
				t.Fatalf(`HashSet(:name, "Ada") error = %v`, err)
			}
			requireCallErrorContains(
				t,
				script,
				"render",
				[]Value{shape},
				CallOptions{},
				"unknown hash method to_s",
			)
		})
	}
}
