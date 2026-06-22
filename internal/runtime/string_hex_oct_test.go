package runtime

import "testing"

func TestStringHex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   int64
	}{
		{name: "plain hex", script: `def run() "ff".hex end`, want: 255},
		{name: "0x prefix", script: `def run() "0xff".hex end`, want: 255},
		{name: "0X prefix", script: `def run() "0Xff".hex end`, want: 255},
		{name: "uppercase digits", script: `def run() "FF".hex end`, want: 255},
		{name: "leading whitespace and prefix", script: `def run() "  0x1A".hex end`, want: 26},
		{name: "negative", script: `def run() "-ff".hex end`, want: -255},
		{name: "positive sign", script: `def run() "+ff".hex end`, want: 255},
		{name: "underscore separator", script: `def run() "1_f".hex end`, want: 31},
		{name: "underscore after prefix", script: `def run() "0x1_f".hex end`, want: 31},
		{name: "stops at first invalid digit", script: `def run() "12g34".hex end`, want: 18},
		{name: "trailing non-hex text", script: `def run() "ffextra".hex end`, want: 4094},
		{name: "trailing space", script: `def run() "ff ".hex end`, want: 255},
		{name: "empty string is zero", script: `def run() "".hex end`, want: 0},
		{name: "bare prefix is zero", script: `def run() "0x".hex end`, want: 0},
		{name: "no hex digits is zero", script: `def run() "garbage".hex end`, want: 0},
		{name: "single zero", script: `def run() "0".hex end`, want: 0},
		{name: "max int64", script: `def run() "7fffffffffffffff".hex end`, want: 9223372036854775807},
		{name: "min int64", script: `def run() "-8000000000000000".hex end`, want: -9223372036854775808},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindInt {
				t.Fatalf("expected int, got %v", result.Kind())
			}
			if result.Int() != tc.want {
				t.Fatalf("hex = %d, want %d", result.Int(), tc.want)
			}
		})
	}
}

func TestStringOct(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   int64
	}{
		{name: "plain octal", script: `def run() "10".oct end`, want: 8},
		{name: "octal digits", script: `def run() "777".oct end`, want: 511},
		{name: "binary prefix", script: `def run() "0b101".oct end`, want: 5},
		{name: "octal prefix", script: `def run() "0o17".oct end`, want: 15},
		{name: "hex prefix", script: `def run() "0xff".oct end`, want: 255},
		{name: "decimal prefix", script: `def run() "0d99".oct end`, want: 99},
		{name: "negative", script: `def run() "-17".oct end`, want: -15},
		{name: "leading whitespace", script: `def run() "  17".oct end`, want: 15},
		{name: "underscore separator", script: `def run() "1_7".oct end`, want: 15},
		{name: "leading underscore is zero", script: `def run() "_1_7".oct end`, want: 0},
		{name: "stops at first invalid digit", script: `def run() "08".oct end`, want: 0},
		{name: "empty string is zero", script: `def run() "".oct end`, want: 0},
		{name: "no octal digits is zero", script: `def run() "garbage".oct end`, want: 0},
		{name: "max int64 via hex prefix", script: `def run() "0x7fffffffffffffff".oct end`, want: 9223372036854775807},
		{name: "min int64 via hex prefix", script: `def run() "-0x8000000000000000".oct end`, want: -9223372036854775808},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindInt {
				t.Fatalf("expected int, got %v", result.Kind())
			}
			if result.Int() != tc.want {
				t.Fatalf("oct = %d, want %d", result.Int(), tc.want)
			}
		})
	}
}

func TestStringHexOctOverflow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "hex above max int64",
			script: `def run() "8000000000000000".hex end`,
			want:   "string.hex integer out of range",
		},
		{
			name:   "hex below min int64",
			script: `def run() "-8000000000000001".hex end`,
			want:   "string.hex integer out of range",
		},
		{
			name:   "oct above max int64 via hex prefix",
			script: `def run() "0x10000000000000000".oct end`,
			want:   "string.oct integer out of range",
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

func TestStringHexOctArityErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "hex with argument",
			script: `def run() "ff".hex(1) end`,
			want:   "string.hex does not take arguments",
		},
		{
			name:   "oct with argument",
			script: `def run() "17".oct(1) end`,
			want:   "string.oct does not take arguments",
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
