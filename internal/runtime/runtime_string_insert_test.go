package runtime

import "testing"

func TestStringPrepend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "single argument",
			script: `def go() "abc".prepend("z") end`,
			want:   "zabc",
		},
		{
			name:   "multiple arguments preserve order",
			script: `def go() "abc".prepend("y", "z") end`,
			want:   "yzabc",
		},
		{
			name:   "no arguments returns copy",
			script: `def go() "abc".prepend() end`,
			want:   "abc",
		},
		{
			name:   "empty argument is a no-op",
			script: `def go() "abc".prepend("") end`,
			want:   "abc",
		},
		{
			name:   "onto empty receiver",
			script: `def go() "".prepend("hi") end`,
			want:   "hi",
		},
		{
			name:   "unicode arguments and receiver",
			script: `def go() "fé".prepend("café_") end`,
			want:   "café_fé",
		},
		{
			name:   "receiver is not mutated",
			script: `def go() s = "abc"; s.prepend("z"); s end`,
			want:   "abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			got := callFunc(t, script, "go", nil)
			if got.Kind() != KindString {
				t.Fatalf("expected string, got %v", got.Kind())
			}
			if got.String() != tt.want {
				t.Fatalf("prepend mismatch: want %q, got %q", tt.want, got.String())
			}
		})
	}
}

func TestStringPrependRejectsNonString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "integer argument",
			script: `def run() "abc".prepend(1) end`,
			want:   "string.prepend expects string arguments",
		},
		{
			name:   "nil argument",
			script: `def run() "abc".prepend(nil) end`,
			want:   "string.prepend expects string arguments",
		},
		{
			name:   "non-string after valid string",
			script: `def run() "abc".prepend("ok", 2) end`,
			want:   "string.prepend expects string arguments",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

func TestStringInsert(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "insert at start",
			script: `def go() "abc".insert(0, "X") end`,
			want:   "Xabc",
		},
		{
			name:   "insert in middle",
			script: `def go() "abc".insert(1, "X") end`,
			want:   "aXbc",
		},
		{
			name:   "insert at length appends",
			script: `def go() "abc".insert(3, "X") end`,
			want:   "abcX",
		},
		{
			name:   "negative one appends",
			script: `def go() "abc".insert(-1, "X") end`,
			want:   "abcX",
		},
		{
			name:   "negative two inserts before last",
			script: `def go() "abc".insert(-2, "X") end`,
			want:   "abXc",
		},
		{
			name:   "most negative valid index inserts at start",
			script: `def go() "abc".insert(-4, "X") end`,
			want:   "Xabc",
		},
		{
			name:   "empty insertion is a no-op",
			script: `def go() "abc".insert(1, "") end`,
			want:   "abc",
		},
		{
			name:   "insert into empty receiver at zero",
			script: `def go() "".insert(0, "hi") end`,
			want:   "hi",
		},
		{
			name:   "insert into empty receiver at minus one",
			script: `def go() "".insert(-1, "hi") end`,
			want:   "hi",
		},
		{
			name:   "unicode index counts characters",
			script: `def go() "café".insert(2, "Z") end`,
			want:   "caZfé",
		},
		{
			name:   "unicode negative index counts characters",
			script: `def go() "café".insert(-1, "Z") end`,
			want:   "caféZ",
		},
		{
			name:   "float index truncates toward zero",
			script: `def go() "abc".insert(1.9, "X") end`,
			want:   "aXbc",
		},
		{
			name:   "receiver is not mutated",
			script: `def go() s = "abc"; s.insert(1, "X"); s end`,
			want:   "abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			got := callFunc(t, script, "go", nil)
			if got.Kind() != KindString {
				t.Fatalf("expected string, got %v", got.Kind())
			}
			if got.String() != tt.want {
				t.Fatalf("insert mismatch: want %q, got %q", tt.want, got.String())
			}
		})
	}
}

func TestStringInsertErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "positive index past end",
			script: `def run() "abc".insert(4, "X") end`,
			want:   "string.insert index 4 out of string",
		},
		{
			name:   "negative index past start",
			script: `def run() "abc".insert(-5, "X") end`,
			want:   "string.insert index -5 out of string",
		},
		{
			name:   "unicode index past end",
			script: `def run() "café".insert(5, "X") end`,
			want:   "string.insert index 5 out of string",
		},
		{
			name:   "missing arguments",
			script: `def run() "abc".insert(1) end`,
			want:   "string.insert expects an index and a string",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".insert(1, "X", "Y") end`,
			want:   "string.insert expects an index and a string",
		},
		{
			name:   "non-integer index",
			script: `def run() "abc".insert("1", "X") end`,
			want:   "string.insert index must be integer",
		},
		{
			name:   "non-string value",
			script: `def run() "abc".insert(1, 2) end`,
			want:   "string.insert value must be string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}
