package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestStringSubGsubBlockForm exercises the Ruby block forms of String#sub,
// String#sub!, String#gsub, and String#gsub!: the block receives the matched
// substring (Ruby's group 0) and its result replaces the match. Expected values
// match MRI Ruby with the equivalent Regexp-literal patterns.
func TestStringSubGsubBlockForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "gsub upcases each regex match",
			script: `def run() "abc".gsub("[bc]", regex: true) do |m| m.upcase end end`,
			want:   "aBC",
		},
		{
			name:   "sub upcases first regex match only",
			script: `def run() "abc".sub("[bc]", regex: true) do |m| m.upcase end end`,
			want:   "aBc",
		},
		{
			name:   "gsub block over literal pattern",
			script: `def run() "hello".gsub("l") do |m| m.upcase end end`,
			want:   "heLLo",
		},
		{
			name:   "sub block over literal pattern replaces first only",
			script: `def run() "hello".sub("l") do |m| m.upcase end end`,
			want:   "heLlo",
		},
		{
			name:   "gsub block wraps each whole match",
			script: `def run() "cat dog".gsub("\\w+", regex: true) do |m| "[" + m + "]" end end`,
			want:   "[cat] [dog]",
		},
		{
			name:   "gsub block non-string result coerced to string",
			script: `def run() "a-b".gsub("[a-z]", regex: true) do |m| 7 end end`,
			want:   "7-7",
		},
		{
			name:   "sub block receives whole match with capture groups",
			script: `def run() "John Smith".sub("(\\w+) (\\w+)", regex: true) do |m| m.upcase end end`,
			want:   "JOHN SMITH",
		},
		{
			name:   "gsub block no match returns receiver unchanged",
			script: `def run() "abc".gsub("z", regex: true) do |m| m.upcase end end`,
			want:   "abc",
		},
		{
			name:   "sub block no match returns receiver unchanged",
			script: `def run() "abc".sub("z", regex: true) do |m| m.upcase end end`,
			want:   "abc",
		},
		{
			name:   "gsub block empty pattern inserts at every position",
			script: `def run() "abc".gsub("") do |m| "-" end end`,
			want:   "-a-b-c-",
		},
		{
			name:   "gsub bang block upcases each match",
			script: `def run() "hello".gsub!("l") do |m| m.upcase end end`,
			want:   "heLLo",
		},
		{
			name:   "sub bang block upcases first match",
			script: `def run() "hello".sub!("l") do |m| m.upcase end end`,
			want:   "heLlo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindString {
				t.Fatalf("expected string, got %v", got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("result mismatch: got %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestStringBangBlockNoMatchReturnsNil pins the Ruby behavior that String#sub!
// and String#gsub! return nil (not the receiver) when the block form makes no
// replacement, matching the value-replacement bang forms.
func TestStringBangBlockNoMatchReturnsNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
	}{
		{
			name:   "sub bang no match",
			script: `def run() "hello".sub!("z") do |m| m.upcase end end`,
		},
		{
			name:   "gsub bang no match",
			script: `def run() "hello".gsub!("z") do |m| m.upcase end end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindNil {
				t.Fatalf("expected nil, got %v (%q)", got.Kind(), got.String())
			}
		})
	}
}

// TestStringSubGsubMixedReplacementAndBlock verifies that supplying both a
// replacement argument and a block is rejected for sub/gsub and their bang
// variants, rather than silently honoring one over the other.
func TestStringSubGsubMixedReplacementAndBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "sub replacement plus block",
			script: `def run() "abc".sub("b", "X") do |m| m.upcase end end`,
			want:   "string.sub cannot take both a replacement argument and a block",
		},
		{
			name:   "gsub replacement plus block",
			script: `def run() "abc".gsub("b", "X") do |m| m.upcase end end`,
			want:   "string.gsub cannot take both a replacement argument and a block",
		},
		{
			name:   "sub bang replacement plus block",
			script: `def run() "abc".sub!("b", "X") do |m| m.upcase end end`,
			want:   "string.sub! cannot take both a replacement argument and a block",
		},
		{
			name:   "gsub bang replacement plus block",
			script: `def run() "abc".gsub!("b", "X") do |m| m.upcase end end`,
			want:   "string.gsub! cannot take both a replacement argument and a block",
		},
		{
			name:   "sub without replacement or block",
			script: `def run() "abc".sub("b") end`,
			want:   "string.sub expects pattern and replacement",
		},
		{
			name:   "gsub without replacement or block",
			script: `def run() "abc".gsub("b") end`,
			want:   "string.gsub expects pattern and replacement",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestStringScanBlockForm verifies String#scan's block form: each match element
// is yielded with the same shape as the non-block result (full match string with
// no captures, an array of captures otherwise), and the call returns the
// receiver string regardless of the block's own result.
func TestStringScanBlockForm(t *testing.T) {
	t.Parallel()

	t.Run("yields plain matches and returns receiver", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run()
  out = []
  ret = "a1 b2".scan("[a-z][0-9]") do |m| out = out.push(m) end
  { ret: ret, out: out }
end`)
		got := callFunc(t, script, "run", nil)
		hash := got.Hash()
		if ret := hash["ret"]; ret.Kind() != KindString || ret.String() != "a1 b2" {
			t.Fatalf("scan block should return receiver, got %v %q", ret.Kind(), ret.String())
		}
		compareArrays(t, hash["out"], []Value{NewString("a1"), NewString("b2")})
	})

	t.Run("yields capture arrays", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run()
  out = []
  "a1 b2".scan("([a-z])([0-9])") do |m| out = out.push(m) end
  out
end`)
		got := callFunc(t, script, "run", nil)
		compareArrays(t, got, []Value{
			NewArray([]Value{NewString("a"), NewString("1")}),
			NewArray([]Value{NewString("b"), NewString("2")}),
		})
	})

	t.Run("no match never yields and returns receiver", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run()
  count = 0
  ret = "abc".scan("z") do |m| count = count + 1 end
  { ret: ret, count: count }
end`)
		got := callFunc(t, script, "run", nil)
		hash := got.Hash()
		if ret := hash["ret"]; ret.Kind() != KindString || ret.String() != "abc" {
			t.Fatalf("scan block should return receiver on no match, got %v %q", ret.Kind(), ret.String())
		}
		if count := hash["count"]; count.Kind() != KindInt || count.Int() != 0 {
			t.Fatalf("scan block should not yield on no match, got %v", count.Int())
		}
	})
}

// TestStringMatchBlockForm verifies String#match's block form: a match yields the
// match-data array ([full, capture1, ...]) and the call returns the block's
// result, while a non-match returns nil without invoking the block.
func TestStringMatchBlockForm(t *testing.T) {
	t.Parallel()

	t.Run("yields match data and returns block result", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run() "a1".match("([a-z])([0-9])") do |m| m[1] end end`)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindString || got.String() != "a" {
			t.Fatalf("match block should return block result, got %v %q", got.Kind(), got.String())
		}
	})

	t.Run("block sees whole match at index zero", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run() "a1".match("([a-z])([0-9])") do |m| m[0] end end`)
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindString || got.String() != "a1" {
			t.Fatalf("match block group 0 mismatch, got %v %q", got.Kind(), got.String())
		}
	})

	t.Run("no match returns nil without calling block", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run()
  called = false
  ret = "xyz".match("([a-z])([0-9])") do |m| called = true; m[1] end
  { ret: ret, called: called }
end`)
		got := callFunc(t, script, "run", nil)
		hash := got.Hash()
		if ret := hash["ret"]; ret.Kind() != KindNil {
			t.Fatalf("match block no match should return nil, got %v", ret.Kind())
		}
		if called := hash["called"]; called.Kind() != KindBool || called.Bool() {
			t.Fatalf("match block should not be called on no match")
		}
	})
}

// TestStringBlockFormPropagatesError confirms an error raised inside a sub/gsub/
// scan/match block aborts the call rather than being swallowed.
func TestStringBlockFormPropagatesError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
	}{
		{
			name:   "gsub block raises",
			script: `def run() "abc".gsub("b") do |m| raise "boom" end end`,
		},
		{
			name:   "sub block raises",
			script: `def run() "abc".sub("b") do |m| raise "boom" end end`,
		},
		{
			name:   "scan block raises",
			script: `def run() "abc".scan("b") do |m| raise "boom" end end`,
		},
		{
			name:   "match block raises",
			script: `def run() "abc".match("b") do |m| raise "boom" end end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "boom")
		})
	}
}

// TestStringGsubBlockOutputLimit confirms a block returning oversized
// replacements still trips the shared regex output-size guard rather than
// allocating an unbounded result.
func TestStringGsubBlockOutputLimit(t *testing.T) {
	t.Parallel()

	re, err := compileCachedRegex("a")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	text := strings.Repeat("a", maxRegexInputBytes)
	yield := func(string) (string, error) { return "xx", nil }
	if _, err := rubyRegexGSubWith(re, text, "string.gsub", rubyBlockReplacer(text, yield)); err == nil {
		t.Fatalf("expected output limit error, got nil")
	} else if !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBoundedReplacementStringCapsCompositeRendering is the direct regression for
// the reviewer's P1 on bound block-result rendering. A sub/gsub block's result is
// turned into its replacement string by boundedReplacementString, which renders
// with StringBounded(maxRegexInputBytes) rather than Value.String(). String()'s
// composite rendering is intentionally unbounded, so a block returning a large
// array/hash would materialize the whole multi-MiB rendering before the regex
// output guard -- which only inspects an already-built string -- could see it.
//
// Driving boundedReplacementString directly makes the cap the only variable: a
// composite whose rendering would exceed the cap must return the output-limit error
// (proving the rendering stopped at the cap rather than fully materializing), while
// a composite that fits returns exactly the unbounded String() rendering (proving
// no over-rejection). A purely error/no-error script-level assertion cannot pin
// this, because the downstream appendBounded guard rejects an oversized result
// either way -- only the rendering having STOPPED at the cap distinguishes the fix.
func TestBoundedReplacementStringCapsCompositeRendering(t *testing.T) {
	t.Parallel()

	// An array whose rendering far exceeds the 1 MiB cap. Each element renders as a
	// distinct multi-digit integer plus ", ", so the rendered form is well over
	// maxRegexInputBytes while the value itself is cheap to build.
	const overCapElements = 300_000
	big := make([]Value, overCapElements)
	for i := range big {
		big[i] = NewInt(int64(i))
	}
	overCap := NewArray(big)
	if overCap.StringByteLen() <= maxRegexInputBytes {
		t.Fatalf("fixture renders %d bytes, want > cap %d", overCap.StringByteLen(), maxRegexInputBytes)
	}

	if _, err := boundedReplacementString(overCap); err == nil {
		t.Fatal("boundedReplacementString over cap = nil error, want output-limit error")
	} else if !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("boundedReplacementString over cap error = %v, want output-limit error", err)
	}

	// A small composite renders the same bytes the unbounded String() would, so the
	// cap never rejects a result that fits. This pins that StringBounded matches the
	// unbounded form byte for byte under the cap.
	small := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	got, err := boundedReplacementString(small)
	if err != nil {
		t.Fatalf("boundedReplacementString under cap = %v, want success", err)
	}
	if want := small.String(); got != want {
		t.Fatalf("boundedReplacementString under cap = %q, want %q (must match unbounded String)", got, want)
	}
}

// TestStringSubGsubBlockCompositeResultRejected exercises the full sub/gsub block
// path with a block returning a composite whose rendering exceeds the regex output
// cap. The call must fail with the output-limit error rather than splice a
// multi-MiB replacement, confirming boundedReplacementString is wired into both the
// sub and gsub block forms. The memory quota is generous so building the array
// itself is allowed, isolating the output-cap behavior from the array build.
func TestStringSubGsubBlockCompositeResultRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
	}{
		{
			name:   "gsub block returning a large array",
			script: `def run() "a".gsub("a") do |m| (1..300000).to_a end end`,
		},
		{
			name:   "sub block returning a large array",
			script: `def run() "a".sub("a") do |m| (1..300000).to_a end end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "output exceeds limit")
		})
	}
}

// TestStringGsubBlockCompositeResultUnderCap confirms the bounded block-result
// rendering does not over-reject: a block returning a small composite whose
// rendering fits well under the regex output cap is spliced normally, exactly as
// Value.String() would have rendered it.
func TestStringGsubBlockCompositeResultUnderCap(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}, `def run() "x".gsub("x") do |m| [1, 2, 3] end end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("small composite block result = %v, want success", err)
	}
	if got.Kind() != KindString || got.String() != "[1, 2, 3]" {
		t.Fatalf("gsub small composite result = %v %q, want %q", got.Kind(), got.String(), "[1, 2, 3]")
	}
}
