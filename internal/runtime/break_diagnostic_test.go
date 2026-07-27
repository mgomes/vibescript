package runtime

import (
	"context"
	"strings"
	"testing"
)

// A break inside a block reported "break used outside of loop", which
// describes a situation the author can see is untrue -- the break is plainly
// inside an each -- so the message sent them looking for a missing end. The
// restriction is genuinely hard to predict, because next and return both cross
// a block boundary and break does not.
func TestBreakInsideBlockNamesTheBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "each with do block", body: "[1, 2, 3].each do |n|\n    break\n  end"},
		{name: "each with brace block", body: "[1, 2, 3].each { |n| break }"},
		{name: "break with a modifier", body: "[1, 2, 3].each { |n| break if n > 1 }"},
		{name: "map", body: "[1, 2, 3].map { |n| break }"},
		{name: "nested blocks", body: "[[1]].each { |row| row.each { |n| break } }"},
		{name: "hash each", body: "({a: 1}).each { |k, v| break }"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s: expected break inside a block to be reported", tc.name)
			}
			if !strings.Contains(err.Error(), "cannot cross a block boundary") {
				t.Fatalf("%s: error = %v, want it to name the block boundary", tc.name, err)
			}
			if strings.Contains(err.Error(), "outside of loop") {
				t.Fatalf("%s: error still claims the break is outside a loop: %v", tc.name, err)
			}
		})
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
