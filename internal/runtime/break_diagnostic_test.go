package runtime

import (
	"context"
	"strings"
	"testing"
)

// break inside a block terminates the call the block was passed to, which is
// Ruby's rule: the value becomes that call's result.
func TestBreakInsideBlockEndsTheCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "each with a value", body: "[1, 2, 3, 4].each { |n| break n if n > 3 }.inspect", want: "4"},
		{name: "each with no value", body: "[1, 2, 3].each { |n| break }.inspect", want: "nil"},
		{name: "map with a value", body: "[1, 2, 3].map { |n| break \"early\" if n == 2\n    n }.inspect", want: `"early"`},
		{name: "do block", body: "[1, 2, 3, 4, 5].each do |n|\n    break n if n > 3\n  end.inspect", want: "4"},
		{name: "assigns before breaking", body: "found = nil\n  [1, 2, 3, 4].each { |n| found = n\n    break if n == 2 }\n  found.inspect", want: "2"},
		{name: "no break runs to completion", body: "[1, 2, 3].each { |n| n }.inspect", want: "[1, 2, 3]"},
		{name: "hash each", body: "({a: 1, b: 2}).each { |k, v| break v }.inspect", want: "1"},
		{name: "nested blocks break the inner call", body: "out = []\n  [[1, 2], [3]].each { |row| row.each { |n| break }\n    out = out + [row.length] }\n  out.inspect", want: "[2, 1]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got.String(), tc.want)
			}
		})
	}
}

// A break that crosses a call boundary with no block involved still reports:
// absorbing it there would silently make it that call's value.
func TestBreakStillCannotCrossAPlainCallBoundary(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def helper()\n  break\nend\ndef run()\n  for i in [1]\n    helper()\n  end\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected a break crossing a plain call boundary to be reported")
	}
	if !strings.Contains(err.Error(), "cannot cross call boundary") {
		t.Fatalf("error = %v, want the call-boundary message", err)
	}
}

// A break with no enclosing loop or block at all keeps the original message,
// which is accurate there.
func TestBreakWithNoEnclosingLoopKeepsOriginalMessage(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  break\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected a bare break to be reported")
	}
	if !strings.Contains(err.Error(), "break used outside of loop") {
		t.Fatalf("error = %v, want the outside-of-loop message", err)
	}
}

// The forms where break does work must keep working.
func TestBreakStillWorksWhereSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "for loop", body: "total = 0\n  for i in [1, 2, 3]\n    break if i > 2\n    total = total + i\n  end\n  total.to_s", want: "3"},
		{name: "while loop", body: "i = 0\n  while true\n    i = i + 1\n    break if i > 2\n  end\n  i.to_s", want: "3"},
		{name: "until loop", body: "i = 0\n  until false\n    i = i + 1\n    break if i > 1\n  end\n  i.to_s", want: "2"},
		{name: "break with a value in a loop", body: "result = nil\n  for i in [1, 2, 3]\n    break\n  end\n  \"done\"", want: "done"},
		// next still crosses a block boundary, which is half of what made the
		// break restriction surprising.
		{name: "next inside a block", body: "out = []\n  [1, 2, 3].each { |n| next if n == 2\n    out.push(n) }\n  out.inspect", want: "[1, 3]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got.String(), tc.want)
			}
		})
	}
}

// A driver abandoned mid-iteration must leave the sandbox accounting intact:
// the estimator's block-iteration regions unwind on their defers, so a break
// must not let a later allocation escape the memory quota.
func TestBreakMidIterationKeepsMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 256 * 1024}, `
    def run(rows)
      rows.each { |row| break }
      rows.map { |row| "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" * 40 }
    end
    `)
	rows := make([]Value, 400)
	for i := range rows {
		rows[i] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to hold after a break abandoned an earlier iteration")
	}
}

// The step quota likewise survives a break: work done before it still counts.
func TestBreakMidIterationKeepsStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 16 << 20}, `
    def run(rows)
      rows.each { |row| break if row > 100000 }
      rows.each { |row| break if row > 100000 }
      1
    end
    `)
	rows := make([]Value, 5000)
	for i := range rows {
		rows[i] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop iteration that breaks only at the end")
	}
}
