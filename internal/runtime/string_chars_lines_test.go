package runtime

import "testing"

func TestStringChars(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii",
			script: `def run() "abc".chars end`,
			want:   []Value{NewString("a"), NewString("b"), NewString("c")},
		},
		{
			name:   "multibyte runes",
			script: `def run() "héllo".chars end`,
			want:   []Value{NewString("h"), NewString("é"), NewString("l"), NewString("l"), NewString("o")},
		},
		{
			name:   "empty string",
			script: `def run() "".chars end`,
			want:   []Value{},
		},
		{
			name:   "newline is a character",
			script: "def run() \"a\\nb\".chars end",
			want:   []Value{NewString("a"), NewString("\n"), NewString("b")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

func TestStringLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "lines retain trailing newline",
			script: "def run() \"a\\nb\".lines end",
			want:   []Value{NewString("a\n"), NewString("b")},
		},
		{
			name:   "trailing newline does not add empty line",
			script: "def run() \"a\\nb\\n\".lines end",
			want:   []Value{NewString("a\n"), NewString("b\n")},
		},
		{
			name:   "single line without newline",
			script: `def run() "abc".lines end`,
			want:   []Value{NewString("abc")},
		},
		{
			name:   "empty string yields no lines",
			script: `def run() "".lines end`,
			want:   []Value{},
		},
		{
			name:   "consecutive newlines yield blank lines",
			script: "def run() \"\\n\\n\".lines end",
			want:   []Value{NewString("\n"), NewString("\n")},
		},
		{
			// Vibescript double-quoted literals decode only \n, \t, \", and \\,
			// so a carriage return reaches the runtime as a literal byte here.
			name:   "crlf keeps carriage return with the newline",
			script: "def run() \"a\r\nb\".lines end",
			want:   []Value{NewString("a\r\n"), NewString("b")},
		},
		{
			name:   "bare carriage return is not a separator",
			script: "def run() \"a\rb\".lines end",
			want:   []Value{NewString("a\rb")},
		},
		{
			name:   "multibyte content split into lines",
			script: "def run() \"héllo\\nwörld\".lines end",
			want:   []Value{NewString("héllo\n"), NewString("wörld")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

func TestStringCharsLinesRejectArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "chars rejects arguments",
			script: `def run() "abc".chars("x") end`,
			want:   "string.chars does not take arguments",
		},
		{
			name:   "lines rejects arguments",
			script: `def run() "a\nb".lines("\n") end`,
			want:   "string.lines does not take arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}
