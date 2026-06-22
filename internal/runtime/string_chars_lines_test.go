package runtime

import (
	"errors"
	"testing"
)

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

func TestStringEachLineShortCircuitsOnBlockError(t *testing.T) {
	t.Parallel()

	// each_line streams line by line, so a block error must stop the scan
	// immediately rather than after every line has been split. Collecting the
	// lines seen before the failure proves only the lines up to the raise were
	// yielded.
	script := compileScript(t, `
def run()
  seen = []
  begin
    "a\nb\nc".each_line do |l|
      seen = seen + [l]
      if l == "b\n"
        raise "boom"
      end
    end
  rescue
    nil
  end
  seen
end
`)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{NewString("a\n"), NewString("b\n")})
}

func TestForEachLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "empty string yields nothing", text: "", want: nil},
		{name: "retains trailing newline", text: "a\nb", want: []string{"a\n", "b"}},
		{name: "trailing newline adds no empty line", text: "a\nb\n", want: []string{"a\n", "b\n"}},
		{name: "consecutive newlines yield blank lines", text: "\n\n", want: []string{"\n", "\n"}},
		{name: "crlf stays together", text: "a\r\nb", want: []string{"a\r\n", "b"}},
		{name: "bare carriage return is not a separator", text: "a\rb", want: []string{"a\rb"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			if err := forEachLine(tc.text, func(line string) error {
				got = append(got, line)
				return nil
			}); err != nil {
				t.Fatalf("forEachLine returned unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("forEachLine(%q) = %q, want %q", tc.text, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("forEachLine(%q)[%d] = %q, want %q", tc.text, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestForEachLineStopsOnError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("stop")
	var got []string
	err := forEachLine("a\nb\nc", func(line string) error {
		got = append(got, line)
		if line == "b\n" {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("forEachLine error = %v, want %v", err, sentinel)
	}
	want := []string{"a\n", "b\n"}
	if len(got) != len(want) {
		t.Fatalf("forEachLine yielded %q before stopping, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("forEachLine yielded %q before stopping, want %q", got, want)
		}
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
