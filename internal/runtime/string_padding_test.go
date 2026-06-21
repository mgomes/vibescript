package runtime

import (
	"math"
	"testing"
)

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

// TestStringPaddingRejectsOutOfRangeFloatWidths covers Float widths that cannot
// be represented as a native int. These cannot appear as script literals, so the
// widths are passed directly as Values. A naive int(float) cast wraps such
// widths into an in-range value (for example 1e20 collapsing to math.MinInt),
// which would silently slip past the projected-size guard and return the
// receiver unchanged instead of raising the intended range error.
func TestStringPaddingRejectsOutOfRangeFloatWidths(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def center_w(s, w) s.center(w) end
    def ljust_w(s, w) s.ljust(w) end
    def rjust_w(s, w) s.rjust(w) end
    `)

	receiver := NewString("hi")
	hugePositive := math.Nextafter(math.MaxInt, math.Inf(1))
	hugeNegative := math.Nextafter(math.MinInt, math.Inf(-1))

	cases := []struct {
		name  string
		fn    string
		width Value
		want  string
	}{
		{"center huge positive float", "center_w", NewFloat(1e20), "string.center width is out of range"},
		{"ljust huge positive float", "ljust_w", NewFloat(1e20), "string.ljust width is out of range"},
		{"rjust huge positive float", "rjust_w", NewFloat(1e20), "string.rjust width is out of range"},
		{"center huge negative float", "center_w", NewFloat(-1e20), "string.center width is out of range"},
		{"center float just above int max", "center_w", NewFloat(hugePositive), "string.center width is out of range"},
		{"center float just below int min", "center_w", NewFloat(hugeNegative), "string.center width is out of range"},
		{"center NaN width", "center_w", NewFloat(math.NaN()), "string.center width is out of range"},
		{"center positive infinity width", "center_w", NewFloat(math.Inf(1)), "string.center width is out of range"},
		{"center negative infinity width", "center_w", NewFloat(math.Inf(-1)), "string.center width is out of range"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, []Value{receiver, tc.width}, CallOptions{}, tc.want)
		})
	}
}

// TestStringPaddingAcceptsLargeInRangeFloatWidth confirms that a Float width
// near the truncation boundary still pads correctly rather than being rejected,
// guarding against an overly aggressive range check.
func TestStringPaddingAcceptsLargeInRangeFloatWidth(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(s, w) s.ljust(w, ".") end`)
	result := callFunc(t, script, "run", []Value{NewString("hi"), NewFloat(5.9)})
	if result.Kind() != KindString {
		t.Fatalf("expected string, got %v", result.Kind())
	}
	if got, want := result.String(), "hi..."; got != want {
		t.Fatalf("padding mismatch: got %q, want %q", got, want)
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
