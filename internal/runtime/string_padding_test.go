package runtime

import "testing"

func TestStringPadding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "center default space padding",
			script: `def run() "hi".center(6) end`,
			want:   "  hi  ",
		},
		{
			name:   "center odd padding favors the right",
			script: `def run() "hi".center(5) end`,
			want:   " hi  ",
		},
		{
			name:   "center custom single-character pad",
			script: `def run() "hi".center(6, "-") end`,
			want:   "--hi--",
		},
		{
			name:   "center multi-character pad truncated to width",
			script: `def run() "hi".center(10, "12345") end`,
			want:   "1234hi1234",
		},
		{
			name:   "center multi-character pad uneven split",
			script: `def run() "hi".center(7, "ab") end`,
			want:   "abhiaba",
		},
		{
			name:   "ljust default space padding",
			script: `def run() "hi".ljust(5) end`,
			want:   "hi   ",
		},
		{
			name:   "ljust custom pad",
			script: `def run() "hi".ljust(5, ".") end`,
			want:   "hi...",
		},
		{
			name:   "ljust multi-character pad truncated",
			script: `def run() "hi".ljust(7, "ab") end`,
			want:   "hiababa",
		},
		{
			name:   "rjust default space padding",
			script: `def run() "hi".rjust(5) end`,
			want:   "   hi",
		},
		{
			name:   "rjust custom pad",
			script: `def run() "hi".rjust(5, ".") end`,
			want:   "...hi",
		},
		{
			name:   "rjust multi-character pad truncated",
			script: `def run() "hi".rjust(7, "ab") end`,
			want:   "ababahi",
		},
		{
			name:   "width equal to length returns receiver",
			script: `def run() "hello".center(5) end`,
			want:   "hello",
		},
		{
			name:   "width shorter than length returns receiver",
			script: `def run() "abc".center(2) end`,
			want:   "abc",
		},
		{
			name:   "zero width returns receiver",
			script: `def run() "hi".center(0) end`,
			want:   "hi",
		},
		{
			name:   "negative width returns receiver",
			script: `def run() "hi".center(-3) end`,
			want:   "hi",
		},
		{
			name:   "float width truncates toward zero",
			script: `def run() "hi".center(7.9) end`,
			want:   "  hi   ",
		},
		{
			name:   "rjust float width truncates toward zero",
			script: `def run() "hi".rjust(7.2, ".") end`,
			want:   ".....hi",
		},
		{
			name:   "empty receiver pads to width",
			script: `def run() "".rjust(3, "x") end`,
			want:   "xxx",
		},
		{
			name:   "center counts unicode runes not bytes",
			script: `def run() "é".center(5, "-") end`,
			want:   "--é--",
		},
		{
			name:   "rjust pads with multibyte runes",
			script: `def run() "héllo".rjust(8, "·") end`,
			want:   "···héllo",
		},
		{
			name:   "center distributes multibyte pad runes",
			script: `def run() "ok".center(6, "·") end`,
			want:   "··ok··",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("padding mismatch: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStringPaddingRejectsBadArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "center requires a width",
			script: `def run() "hi".center() end`,
			want:   "string.center expects width and optional pad string",
		},
		{
			name:   "center rejects extra arguments",
			script: `def run() "hi".center(5, "-", "x") end`,
			want:   "string.center expects width and optional pad string",
		},
		{
			name:   "center rejects keyword arguments",
			script: `def run() "hi".center(5, foo: 1) end`,
			want:   "string.center does not accept keyword arguments",
		},
		{
			name:   "center rejects non-numeric width",
			script: `def run() "hi".center("5") end`,
			want:   "string.center width must be integer",
		},
		{
			name:   "center rejects non-string pad",
			script: `def run() "hi".center(5, 1) end`,
			want:   "string.center pad must be string",
		},
		{
			name:   "center rejects empty pad",
			script: `def run() "hi".center(6, "") end`,
			want:   "string.center pad must not be empty",
		},
		{
			name:   "ljust rejects empty pad",
			script: `def run() "hi".ljust(6, "") end`,
			want:   "string.ljust pad must not be empty",
		},
		{
			name:   "rjust rejects non-numeric width",
			script: `def run() "hi".rjust(true) end`,
			want:   "string.rjust width must be integer",
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

func TestStringPaddingEnforcesMemoryQuota(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
	}{
		{
			name:   "ljust oversized width",
			script: `def run() "x".ljust(1000000) end`,
		},
		{
			name:   "center oversized width",
			script: `def run() "x".center(1000000) end`,
		},
		{
			name:   "rjust oversized width with multibyte pad",
			script: `def run() "x".rjust(1000000, "·") end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 4096}, tc.script)
			requireRunMemoryQuotaError(t, script, nil, CallOptions{})
		})
	}
}
