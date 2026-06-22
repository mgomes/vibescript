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
		{
			name:   "chars rejects keyword arguments",
			script: `def run() "abc".chars(foo: 1) end`,
			want:   "string.chars does not take arguments",
		},
		{
			name:   "lines rejects keyword arguments",
			script: `def run() "a\nb".lines(chomp: true) end`,
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

func TestStringEachChar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii yields each character",
			script: "def run() out = [] \"ab\".each_char { |c| out = out + [c] } out end",
			want:   []Value{NewString("a"), NewString("b")},
		},
		{
			name:   "multibyte yields whole runes",
			script: "def run() out = [] \"héllo🎉\".each_char { |c| out = out + [c] } out end",
			want:   []Value{NewString("h"), NewString("é"), NewString("l"), NewString("l"), NewString("o"), NewString("🎉")},
		},
		{
			name:   "empty string yields nothing",
			script: "def run() out = [] \"\".each_char { |c| out = out + [c] } out end",
			want:   []Value{},
		},
		{
			name:   "newline is yielded as a character",
			script: "def run() out = [] \"a\\nb\".each_char { |c| out = out + [c] } out end",
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

func TestStringEachLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "lines retain trailing newline",
			script: "def run() out = [] \"a\\nb\".each_line { |l| out = out + [l] } out end",
			want:   []Value{NewString("a\n"), NewString("b")},
		},
		{
			name:   "trailing newline does not add empty line",
			script: "def run() out = [] \"a\\nb\\n\".each_line { |l| out = out + [l] } out end",
			want:   []Value{NewString("a\n"), NewString("b\n")},
		},
		{
			name:   "single line without newline",
			script: "def run() out = [] \"abc\".each_line { |l| out = out + [l] } out end",
			want:   []Value{NewString("abc")},
		},
		{
			name:   "empty string yields no lines",
			script: "def run() out = [] \"\".each_line { |l| out = out + [l] } out end",
			want:   []Value{},
		},
		{
			// Vibescript double-quoted literals decode only \n, \t, \", and \\,
			// so a carriage return reaches the runtime as a literal byte here.
			name:   "crlf keeps carriage return with the newline",
			script: "def run() out = [] \"a\r\nb\".each_line { |l| out = out + [l] } out end",
			want:   []Value{NewString("a\r\n"), NewString("b")},
		},
		{
			name:   "multibyte content split into lines",
			script: "def run() out = [] \"héllo\\nwörld\".each_line { |l| out = out + [l] } out end",
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

func TestStringEachReturnsReceiver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "each_char returns the receiver",
			script: "def run() \"abc\".each_char { |c| c } end",
			want:   "abc",
		},
		{
			name:   "each_line returns the receiver",
			script: "def run() \"a\\nb\".each_line { |l| l } end",
			want:   "a\nb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != tc.want {
				t.Fatalf("expected receiver string %q, got %v %q", tc.want, result.Kind(), result.String())
			}
		})
	}
}

func TestStringEachRejectsMisuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "each_char requires a block",
			script: `def run() "ab".each_char end`,
			want:   "string.each_char requires a block",
		},
		{
			name:   "each_line requires a block",
			script: `def run() "a\nb".each_line end`,
			want:   "string.each_line requires a block",
		},
		{
			name:   "each_char rejects positional arguments",
			script: `def run() "ab".each_char("x") { |c| c } end`,
			want:   "string.each_char does not take arguments",
		},
		{
			name:   "each_line rejects positional arguments",
			script: `def run() "a\nb".each_line("\n") { |l| l } end`,
			want:   "string.each_line does not take arguments",
		},
		{
			name:   "each_char rejects keyword arguments",
			script: `def run() "ab".each_char(foo: 1) { |c| c } end`,
			want:   "string.each_char does not take arguments",
		},
		{
			name:   "each_line rejects keyword arguments",
			script: `def run() "a\nb".each_line(chomp: true) { |l| l } end`,
			want:   "string.each_line does not take arguments",
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
