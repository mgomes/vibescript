package runtime

import "testing"

func TestStringPartition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "splits on first separator occurrence",
			script: `def run() "abc=def=ghi".partition("=") end`,
			want:   []Value{NewString("abc"), NewString("="), NewString("def=ghi")},
		},
		{
			name:   "multi-character separator",
			script: `def run() "a::b::c".partition("::") end`,
			want:   []Value{NewString("a"), NewString("::"), NewString("b::c")},
		},
		{
			name:   "missing separator keeps whole string as head",
			script: `def run() "no-sep".partition("=") end`,
			want:   []Value{NewString("no-sep"), NewString(""), NewString("")},
		},
		{
			name:   "empty separator matches at start",
			script: `def run() "abc".partition("") end`,
			want:   []Value{NewString(""), NewString(""), NewString("abc")},
		},
		{
			name:   "separator at start yields empty head",
			script: `def run() "=abc".partition("=") end`,
			want:   []Value{NewString(""), NewString("="), NewString("abc")},
		},
		{
			name:   "separator at end yields empty tail",
			script: `def run() "abc=".partition("=") end`,
			want:   []Value{NewString("abc"), NewString("="), NewString("")},
		},
		{
			name:   "multibyte content around separator",
			script: `def run() "héllo=wörld".partition("=") end`,
			want:   []Value{NewString("héllo"), NewString("="), NewString("wörld")},
		},
		{
			name:   "multibyte separator",
			script: `def run() "a÷b÷c".partition("÷") end`,
			want:   []Value{NewString("a"), NewString("÷"), NewString("b÷c")},
		},
		{
			name:   "empty receiver",
			script: `def run() "".partition("=") end`,
			want:   []Value{NewString(""), NewString(""), NewString("")},
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

func TestStringRPartition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "splits on last separator occurrence",
			script: `def run() "abc=def=ghi".rpartition("=") end`,
			want:   []Value{NewString("abc=def"), NewString("="), NewString("ghi")},
		},
		{
			name:   "multi-character separator",
			script: `def run() "a::b::c".rpartition("::") end`,
			want:   []Value{NewString("a::b"), NewString("::"), NewString("c")},
		},
		{
			name:   "missing separator keeps whole string as tail",
			script: `def run() "no-sep".rpartition("=") end`,
			want:   []Value{NewString(""), NewString(""), NewString("no-sep")},
		},
		{
			name:   "empty separator matches at end",
			script: `def run() "abc".rpartition("") end`,
			want:   []Value{NewString("abc"), NewString(""), NewString("")},
		},
		{
			name:   "separator at start yields empty head",
			script: `def run() "=abc".rpartition("=") end`,
			want:   []Value{NewString(""), NewString("="), NewString("abc")},
		},
		{
			name:   "separator at end yields empty tail",
			script: `def run() "abc=".rpartition("=") end`,
			want:   []Value{NewString("abc"), NewString("="), NewString("")},
		},
		{
			name:   "multibyte content around separator",
			script: `def run() "héllo=wörld".rpartition("=") end`,
			want:   []Value{NewString("héllo"), NewString("="), NewString("wörld")},
		},
		{
			name:   "multibyte separator",
			script: `def run() "a÷b÷c".rpartition("÷") end`,
			want:   []Value{NewString("a÷b"), NewString("÷"), NewString("c")},
		},
		{
			name:   "empty receiver",
			script: `def run() "".rpartition("=") end`,
			want:   []Value{NewString(""), NewString(""), NewString("")},
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

func TestStringPartitionRejectsBadArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "partition requires a separator",
			script: `def run() "abc".partition() end`,
			want:   "string.partition expects exactly one separator",
		},
		{
			name:   "partition rejects extra arguments",
			script: `def run() "abc".partition("=", "x") end`,
			want:   "string.partition expects exactly one separator",
		},
		{
			name:   "partition rejects keyword arguments",
			script: `def run() "abc".partition("=", foo: 1) end`,
			want:   "string.partition expects exactly one separator",
		},
		{
			name:   "partition rejects non-string separator",
			script: `def run() "abc".partition(1) end`,
			want:   "string.partition separator must be string",
		},
		{
			name:   "rpartition requires a separator",
			script: `def run() "abc".rpartition() end`,
			want:   "string.rpartition expects exactly one separator",
		},
		{
			name:   "rpartition rejects extra arguments",
			script: `def run() "abc".rpartition("=", "x") end`,
			want:   "string.rpartition expects exactly one separator",
		},
		{
			name:   "rpartition rejects keyword arguments",
			script: `def run() "abc".rpartition("=", foo: 1) end`,
			want:   "string.rpartition expects exactly one separator",
		},
		{
			name:   "rpartition rejects non-string separator",
			script: `def run() "abc".rpartition(1) end`,
			want:   "string.rpartition separator must be string",
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
