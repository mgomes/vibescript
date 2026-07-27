package runtime

import (
	"context"
	"testing"
)

// JSON object keys parse as strings while hash literals write symbol keys, so
// JSON.parse(JSON.stringify(h)) is never equal to h and a symbol lookup reads
// nil rather than reporting anything. The behaviour is defensible -- JSON keys
// really are strings -- but it is silent, so the first symptom is usually a
// downstream nil error far from the parse.
//
// These pin both the behaviour and the documented conversion, so the recipe in
// docs/builtins.md cannot quietly stop working.
func TestJSONParseKeysAreStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "string lookup finds the value", expr: `JSON.parse(JSON.stringify({name: "Ada"}))["name"].inspect`, want: `"Ada"`},
		{name: "symbol lookup reads nil", expr: `JSON.parse(JSON.stringify({name: "Ada"}))[:name].inspect`, want: "nil"},
		{name: "keys are strings", expr: `JSON.parse(JSON.stringify({name: "Ada"})).keys.inspect`, want: `["name"]`},
		{name: "a round trip is not identity", expr: `(JSON.parse(JSON.stringify({name: "Ada"})) == {name: "Ada"}).to_s`, want: "false"},
		// The documented conversion restores the symbol-keyed form.
		{name: "transform_keys restores equality", expr: `(JSON.parse(JSON.stringify({name: "Ada"})).transform_keys { |k| k.to_sym } == {name: "Ada"}).to_s`, want: "true"},
		{name: "transform_keys on several keys", expr: `(JSON.parse(JSON.stringify({name: "Ada", age: 36})).transform_keys { |k| k.to_sym } == {name: "Ada", age: 36}).to_s`, want: "true"},
		{name: "transform_keys preserves values", expr: `JSON.parse(JSON.stringify({name: "Ada"})).transform_keys { |k| k.to_sym }[:name].inspect`, want: `"Ada"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}
