package runtime

import (
	"context"
	"testing"
)

// JSON object keys parse as strings while hash literals write symbol keys, so
// JSON.parse(JSON.stringify(h)) is never equal to h and a symbol lookup reads
// nil rather than reporting anything. The behavior is defensible -- JSON keys
// really are strings -- but it is silent, so the first symptom is usually a
// downstream nil error far from the parse.
//
// These pin both the behavior and the documented conversion, so the recipe in
// docs/builtins.md cannot quietly stop working.
func TestJSONParseKeysAreStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "string lookup finds the value", expr: `JSON.parse(JSON.stringify({name: "Ada"}))["name"].inspect`, want: `"Ada"`},
		{name: "symbol lookup finds the same value", expr: `JSON.parse(JSON.stringify({name: "Ada"}))[:name].inspect`, want: `"Ada"`},
		{name: "keys are strings", expr: `JSON.parse(JSON.stringify({name: "Ada"})).keys.inspect`, want: `["name"]`},
		// One keyspace makes the round trip an identity: this is what ADR-006
		// item 3 buys, and it needs no conversion step.
		{name: "a round trip is identity", expr: `(JSON.parse(JSON.stringify({name: "Ada"})) == {name: "Ada"}).to_s`, want: "true"},
		{name: "a round trip is identity for several keys", expr: `(JSON.parse(JSON.stringify({name: "Ada", age: 36})) == {name: "Ada", age: 36}).to_s`, want: "true"},
		{name: "a nested round trip is identity", expr: `(JSON.parse(JSON.stringify({a: {b: 1}})) == {a: {b: 1}}).to_s`, want: "true"},
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
