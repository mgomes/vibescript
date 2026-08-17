package runtime

import (
	"context"
	"testing"
)

// eql? checked only the outermost kind before delegating to ==, so nested
// values took the widened numeric comparison and [1].eql?([1.0]) was true.
// Ruby says false: eql? is the kind-strict predicate, and its strictness has
// to hold at every level, which is what hash-key identity depends on.
func TestEqlIsKindStrictRecursively(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		// The scalar case always worked.
		{`(1).eql?(1.0).to_s`, "false"},
		// These did not.
		{`[1].eql?([1.0]).to_s`, "false"},
		{`({a: 1}).eql?({a: 1.0}).to_s`, "false"},
		{`[[1]].eql?([[1.0]]).to_s`, "false"},
		{`[1, 2].eql?([1, 2.0]).to_s`, "false"},
		// Same kinds still compare equal, nested or not.
		{`[1].eql?([1]).to_s`, "true"},
		{`({a: 1}).eql?({a: 1}).to_s`, "true"},
		{`[[1]].eql?([[1]]).to_s`, "true"},
		{`(1.0).eql?(1.0).to_s`, "true"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
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

// == stays numeric at every level, which is the distinction eql? draws
// against. Making eql? strict must not narrow ==.
func TestEqualityStaysNumericRecursively(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`(1 == 1.0).to_s`, "true"},
		{`([1] == [1.0]).to_s`, "true"},
		{`(({a: 1}) == {a: 1.0}).to_s`, "true"},
		{`([[1]] == [[1.0]]).to_s`, "true"},
		{`([1, 2] == [1, 2.0]).to_s`, "true"},
		// And a genuine difference is still a difference.
		{`([1] == [2]).to_s`, "false"},
		{`([1] == ["1"]).to_s`, "false"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
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
