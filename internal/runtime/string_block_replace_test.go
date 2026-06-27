package runtime

import (
	"context"
	"runtime"
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
// and String#gsub! return nil only when the pattern never matches. The decision
// is keyed off whether a match occurred, not whether the result bytes changed.
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
		{
			name:   "sub bang regex no match",
			script: `def run() "hello".sub!("z", regex: true) do |m| m.upcase end end`,
		},
		{
			name:   "gsub bang regex no match",
			script: `def run() "hello".gsub!("z", regex: true) do |m| m.upcase end end`,
		},
		{
			name:   "sub bang template no match",
			script: `def run() "hello".sub!("z", "Z") end`,
		},
		{
			name:   "gsub bang template no match",
			script: `def run() "hello".gsub!("z", "Z") end`,
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

// TestStringBangMatchUnchangedReturnsReceiver is the direct regression for the
// reviewer's finding that String#sub!/String#gsub! must return the receiver --
// not nil -- whenever a substitution is performed, even one that reproduces the
// original text. Ruby keys the bang return off whether the pattern matched, so a
// block that returns the match unchanged, or an empty-pattern replacement that
// leaves the bytes identical, still returns the rewritten string. Comparing the
// result bytes (the prior behavior) wrongly reported nil for these cases.
func TestStringBangMatchUnchangedReturnsReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "sub bang literal block returns match unchanged",
			script: `def run() "a".sub!("a") do |m| m end end`,
			want:   "a",
		},
		{
			name:   "gsub bang literal block returns match unchanged",
			script: `def run() "aa".gsub!("a") do |m| m end end`,
			want:   "aa",
		},
		{
			name:   "sub bang regex block returns match unchanged",
			script: `def run() "abc".sub!("b", regex: true) do |m| m end end`,
			want:   "abc",
		},
		{
			name:   "gsub bang regex block returns match unchanged",
			script: `def run() "abc".gsub!("[abc]", regex: true) do |m| m end end`,
			want:   "abc",
		},
		{
			name:   "sub bang template replaces with same text",
			script: `def run() "abc".sub!("b", "b") end`,
			want:   "abc",
		},
		{
			name:   "gsub bang template replaces with same text",
			script: `def run() "abc".gsub!("b", "b") end`,
			want:   "abc",
		},
		{
			name:   "gsub bang empty literal pattern leaves bytes identical",
			script: `def run() "abc".gsub!("", "") end`,
			want:   "abc",
		},
		{
			name:   "gsub bang empty pattern on empty string still substitutes",
			script: `def run() "".gsub!("", "") end`,
			want:   "",
		},
		{
			name:   "gsub bang empty-match regex leaves bytes identical",
			script: `def run() "abc".gsub!("x*", regex: true) do |m| m end end`,
			want:   "abc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindString {
				t.Fatalf("expected string, got %v (%q)", got.Kind(), got.String())
			}
			if got.String() != tc.want {
				t.Fatalf("result = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

func TestPlainStringReplacementEnforcesOutputLimit(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{
		StepQuota:        100_000,
		MemoryQuotaBytes: 8 << 20,
	}, `
def gsub_run(text, replacement)
  text.gsub("a", replacement).size
end

def sub_run(text, replacement)
  text.sub("", replacement).size
end

def no_match_gsub(text, replacement)
  text.gsub("z", replacement)
end

def no_match_gsub_bang(text, replacement)
  text.gsub!("z", replacement)
end

def literal_gsub(text, pattern)
  text.gsub(pattern, "R")
end

def literal_sub(text, pattern)
  text.sub(pattern, "R")
end
	`)

	requireCallErrorContains(t, script, "gsub_run", []Value{
		NewString(strings.Repeat("a", 2_000)),
		NewString(strings.Repeat("x", 1_000)),
	}, CallOptions{}, "string.gsub output exceeds limit")
	requireCallErrorContains(t, script, "sub_run", []Value{
		NewString("abc"),
		NewString(strings.Repeat("x", maxRegexInputBytes+1)),
	}, CallOptions{}, "string.sub replacement exceeds limit")

	huge := NewString(strings.Repeat("x", maxRegexInputBytes+1))
	got := callFunc(t, script, "no_match_gsub", []Value{NewString("abc"), huge})
	if got.Kind() != KindString || got.String() != "abc" {
		t.Fatalf("no-match gsub with huge replacement = %#v, want original string", got)
	}
	got = callFunc(t, script, "no_match_gsub_bang", []Value{NewString("abc"), huge})
	if got.Kind() != KindNil {
		t.Fatalf("no-match gsub! with huge replacement = %#v, want nil", got)
	}

	bigLiteral := strings.Repeat("p", maxRegexInputBytes+1)
	got = callFunc(t, script, "literal_gsub", []Value{NewString("abc"), NewString(bigLiteral)})
	if got.Kind() != KindString || got.String() != "abc" {
		t.Fatalf("gsub with unmatched oversized literal pattern = %#v, want original string", got)
	}
	got = callFunc(t, script, "literal_gsub", []Value{NewString(bigLiteral), NewString(bigLiteral)})
	if got.Kind() != KindString || got.String() != "R" {
		t.Fatalf("gsub collapsing oversized source = %#v, want replacement", got)
	}
	got = callFunc(t, script, "literal_sub", []Value{NewString(bigLiteral), NewString(bigLiteral)})
	if got.Kind() != KindString || got.String() != "R" {
		t.Fatalf("sub collapsing oversized source = %#v, want replacement", got)
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
	if _, _, err := rubyRegexGSubWith(re, text, "string.gsub", rubyBlockReplacer(text, yield)); err == nil {
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

// TestStringSubGsubLiteralBlockInvalidUTF8 is the direct regression for the
// reviewer's P2 on preserving literal byte matching for block replacements. A
// literal-pattern block form (regex: false) must match byte-for-byte exactly like
// the literal template form, including for patterns and subjects holding invalid
// UTF-8 -- which Vibescript supports via APIs like byteslice. Routing the literal
// block form through Go's regexp engine breaks this, because regexp patterns must
// be valid UTF-8: a raw 0xc3 byte pattern would raise "invalid regex" instead of
// matching the literal byte. Driving the script-level sub/gsub block path with a
// 0xc3 pattern produced by "Aé".byteslice(1, 1) pins that the literal byte is
// matched and yielded to the block, byte for byte with MRI Ruby on a binary
// (ASCII-8BIT) string.
func TestStringSubGsubLiteralBlockInvalidUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		script    string
		wantBytes []byte
	}{
		{
			name: "gsub replaces every invalid byte match",
			script: `def run()
  pat = "Aé".byteslice(1, 1)
  ("X" + pat + "Y" + pat + "Z").gsub(pat) do |m| "_" end
end`,
			wantBytes: []byte("X_Y_Z"),
		},
		{
			name: "sub replaces only the first invalid byte match",
			script: `def run()
  pat = "Aé".byteslice(1, 1)
  ("X" + pat + "Y" + pat + "Z").sub(pat) do |m| "_" end
end`,
			wantBytes: []byte{'X', '_', 'Y', 0xc3, 'Z'},
		},
		{
			name: "gsub yields the matched invalid byte to the block",
			script: `def run()
  pat = "Aé".byteslice(1, 1)
  ("X" + pat + "Y" + pat).gsub(pat) do |m| m end
end`,
			wantBytes: []byte{'X', 0xc3, 'Y', 0xc3},
		},
		{
			name: "gsub empty pattern over invalid byte subject advances by one byte",
			script: `def run()
  pat = "Aé".byteslice(1, 1)
  ("X" + pat + "Y").gsub("") do |m| "-" end
end`,
			wantBytes: []byte{'-', 'X', '-', 0xc3, '-', 'Y', '-'},
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
			if got.String() != string(tc.wantBytes) {
				t.Fatalf("result bytes mismatch: got %x, want %x", got.String(), tc.wantBytes)
			}
		})
	}
}

// TestLiteralBlockReplace pins the byte-for-byte semantics of the literal block
// replacer that backs String#sub and String#gsub when the pattern is literal
// (regex: false). It must mirror strings.Replace/ReplaceAll exactly -- including
// empty-pattern per-position matching and invalid-UTF-8 bytes -- and yield the
// literal run that was replaced to the block, while bounding every append by the
// shared regex output cap.
func TestLiteralBlockReplace(t *testing.T) {
	t.Parallel()

	const invalid = "\xc3" // raw lead byte, never valid UTF-8 on its own

	tests := []struct {
		name        string
		src         string
		pattern     string
		global      bool
		wantResult  string
		wantMatched bool
		wantYields  []string
	}{
		{
			name:        "gsub non-empty literal",
			src:         "ababab",
			pattern:     "ab",
			global:      true,
			wantResult:  "<ab><ab><ab>",
			wantMatched: true,
			wantYields:  []string{"ab", "ab", "ab"},
		},
		{
			name:        "sub non-empty literal first only",
			src:         "ababab",
			pattern:     "ab",
			global:      false,
			wantResult:  "<ab>abab",
			wantMatched: true,
			wantYields:  []string{"ab"},
		},
		{
			name:        "gsub empty pattern every position",
			src:         "abc",
			pattern:     "",
			global:      true,
			wantResult:  "<>a<>b<>c<>",
			wantMatched: true,
			wantYields:  []string{"", "", "", ""},
		},
		{
			name:        "sub empty pattern leading only",
			src:         "abc",
			pattern:     "",
			global:      false,
			wantResult:  "<>abc",
			wantMatched: true,
			wantYields:  []string{""},
		},
		{
			name:        "gsub empty pattern multibyte advances by rune",
			src:         "aé",
			pattern:     "",
			global:      true,
			wantResult:  "<>a<>é<>",
			wantMatched: true,
			wantYields:  []string{"", "", ""},
		},
		{
			name:        "sub empty pattern on empty source still matches",
			src:         "",
			pattern:     "",
			global:      false,
			wantResult:  "<>",
			wantMatched: true,
			wantYields:  []string{""},
		},
		{
			name:        "gsub matches invalid utf8 byte literally",
			src:         "X" + invalid + "Y" + invalid + "Z",
			pattern:     invalid,
			global:      true,
			wantResult:  "X<" + invalid + ">Y<" + invalid + ">Z",
			wantMatched: true,
			wantYields:  []string{invalid, invalid},
		},
		{
			name:        "gsub empty pattern over invalid byte subject advances by one byte",
			src:         "X" + invalid + "Y",
			pattern:     "",
			global:      true,
			wantResult:  "<>X<>" + invalid + "<>Y<>",
			wantMatched: true,
			wantYields:  []string{"", "", "", ""},
		},
		{
			name:        "sub no match leaves matched false",
			src:         "abc",
			pattern:     "xyz",
			global:      false,
			wantResult:  "abc",
			wantMatched: false,
			wantYields:  nil,
		},
		{
			name:        "gsub no match returns receiver",
			src:         "abc",
			pattern:     "xyz",
			global:      true,
			wantResult:  "abc",
			wantMatched: false,
			wantYields:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var yields []string
			yield := func(match string) (string, error) {
				yields = append(yields, match)
				return "<" + match + ">", nil
			}
			got, matched, err := literalBlockReplace(tc.src, tc.pattern, tc.global, yield)
			if err != nil {
				t.Fatalf("literalBlockReplace error: %v", err)
			}
			if got != tc.wantResult {
				t.Fatalf("result = %x, want %x", got, tc.wantResult)
			}
			if matched != tc.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tc.wantMatched)
			}
			if len(yields) != len(tc.wantYields) {
				t.Fatalf("yield count = %d (%x), want %d (%x)", len(yields), yields, len(tc.wantYields), tc.wantYields)
			}
			for i := range yields {
				if yields[i] != tc.wantYields[i] {
					t.Fatalf("yield[%d] = %x, want %x", i, yields[i], tc.wantYields[i])
				}
			}
		})
	}
}

// TestLiteralBlockReplaceOutputLimit confirms the literal block replacer enforces
// the shared regex output cap: a block returning an over-cap replacement for a
// flood of literal matches fails with the output-limit error rather than
// allocating past the guard, matching the regexp block path.
func TestLiteralBlockReplaceOutputLimit(t *testing.T) {
	t.Parallel()

	src := strings.Repeat("a", maxRegexInputBytes)
	yield := func(string) (string, error) { return "xx", nil }
	if _, _, err := literalBlockReplace(src, "a", true, yield); err == nil {
		t.Fatal("expected output limit error, got nil")
	} else if !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLiteralBlockReplaceTinyOutputForHugeSource is the regression for the
// reviewer's finding that the literal block replacer must not preallocate an
// output buffer proportional to len(src). regex: false bypasses the regex
// input-size cap, so src can be many times maxRegexInputBytes; the transient
// allocation must track the bounded output the call accumulates, not the source
// length.
//
// Two distinct properties are pinned, both with a source far over the 1 MiB cap:
//
//   - non-empty pattern (literalBlockReplace): replacing a huge literal token with
//     a tiny block result must allocate only a few bytes. A reintroduced
//     make([]byte, 0, len(src)) would allocate at least the multi-MiB source and
//     trip the byte bound.
//   - empty pattern (literalBlockReplaceEmpty): copying an over-cap source verbatim
//     must stop at the shared output cap with the output-limit error while
//     allocating only cap-bounded memory, never the full uncapped source.
//
// The allocation assertion is deterministic: the call runs in isolation (the test
// is not parallel and Go pauses parallel tests while it runs), so the
// process-global TotalAlloc delta reflects only this call plus negligible runtime
// noise, well under the generous per-case bounds.
func TestLiteralBlockReplaceTinyOutputForHugeSource(t *testing.T) {
	// Not parallel: the allocation measurement reads the process-global TotalAlloc
	// counter, so concurrent tests in this package would pollute the delta.

	// Far larger than the 1 MiB regex cap so a len(src)-sized preallocation would
	// dwarf the bounded output every case produces.
	const hugeLen = 8 * maxRegexInputBytes
	hugeRun := strings.Repeat("p", hugeLen)

	tests := []struct {
		name      string
		src       string
		pattern   string
		global    bool
		yield     func(string) (string, error)
		want      string // expected output when wantOutputLimit is false
		wantMatch bool
		// wantOutputLimit expects the shared output-cap error instead of a result,
		// for the empty-pattern case whose verbatim copy of an over-cap source must
		// stop at the cap rather than materialize the whole source.
		wantOutputLimit bool
		// maxTransientBytes bounds the bytes the single call may allocate. It is a
		// small constant relative to hugeLen, so a reintroduced make([]byte, 0,
		// len(src)) (>= hugeLen) trips the check while bounded growth passes.
		maxTransientBytes uint64
	}{
		{
			name:              "non-empty pattern sub tiny output",
			src:               hugeRun,
			pattern:           hugeRun,
			global:            false,
			yield:             func(string) (string, error) { return "R", nil },
			want:              "R",
			wantMatch:         true,
			maxTransientBytes: maxRegexInputBytes,
		},
		{
			name:              "non-empty pattern gsub tiny output",
			src:               "X" + hugeRun,
			pattern:           hugeRun,
			global:            true,
			yield:             func(string) (string, error) { return "R", nil },
			want:              "XR",
			wantMatch:         true,
			maxTransientBytes: maxRegexInputBytes,
		},
		{
			name:            "empty pattern gsub over-cap source stops at cap",
			src:             hugeRun,
			pattern:         "",
			global:          true,
			yield:           func(string) (string, error) { return "", nil },
			wantOutputLimit: true,
			// The verbatim copy is bounded at the 1 MiB cap; allow several times that
			// for amortized geometric growth while still rejecting an alloc that scales
			// with the multi-MiB source.
			maxTransientBytes: 8 * maxRegexInputBytes,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			var matched bool
			var err error
			alloc := allocBytes(func() {
				got, matched, err = literalBlockReplace(tc.src, tc.pattern, tc.global, tc.yield)
			})
			if tc.wantOutputLimit {
				if err == nil {
					t.Fatalf("literalBlockReplace error = nil, want output-limit error")
				}
				if !strings.Contains(err.Error(), "output exceeds limit") {
					t.Fatalf("literalBlockReplace error = %v, want output-limit error", err)
				}
			} else {
				if err != nil {
					t.Fatalf("literalBlockReplace error = %v, want nil", err)
				}
				if matched != tc.wantMatch {
					t.Fatalf("literalBlockReplace matched = %v, want %v", matched, tc.wantMatch)
				}
				if got != tc.want {
					if len(got) > 64 || len(tc.want) > 64 {
						t.Fatalf("literalBlockReplace output length = %d, want %d", len(got), len(tc.want))
					}
					t.Fatalf("literalBlockReplace = %q, want %q", got, tc.want)
				}
			}
			if alloc > tc.maxTransientBytes {
				t.Fatalf("literalBlockReplace allocated %d bytes for a %d-byte source, want <= %d (no len(src) preallocation)", alloc, len(tc.src), tc.maxTransientBytes)
			}
		})
	}
}

// allocBytes reports the total heap bytes allocated while running f. It reads the
// cumulative TotalAlloc counter before and after, which counts every allocation f
// makes regardless of whether it is later freed, so a transient len(src)-sized
// buffer is visible even though it is collectable afterward.
func allocBytes(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestStringSubGsubLiteralBlockIgnoresPatternCap is the direct regression for
// the reviewer's finding that the literal block form must not inherit the
// regex-only pattern-size cap (validateRegexTextPattern's 16 KiB pattern and
// 1 MiB input checks). The literal template form (strings.Replace/ReplaceAll)
// imposes no such validation caps, so its block counterpart must accept the same
// oversized literal token -- the finding's "replace a 20 KiB literal token with a
// block" case -- rather than rejecting valid input the non-block path replaces.
// Only the regex form (regex: true) applies the validation guards. The shared
// output-size cap that bounds each append is a separate, deliberate guard and is
// kept; this test keeps the result well under that cap to isolate the validation
// caps the fix removed.
func TestStringSubGsubLiteralBlockIgnoresPatternCap(t *testing.T) {
	t.Parallel()

	// A literal pattern far larger than the regex pattern cap (16 KiB). The old
	// code rejected this at validation before reaching literalBlockReplace.
	bigPattern := strings.Repeat("p", maxRegexPatternSize+1)
	text := "A" + bigPattern + "B" + bigPattern + "C"

	tests := []struct {
		name   string
		global bool
		want   string
	}{
		{name: "sub oversized literal pattern", global: false, want: "ARB" + bigPattern + "C"},
		{name: "gsub oversized literal pattern", global: true, want: "ARBRC"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			yield := func(string) (string, error) { return "R", nil }
			got, matched, err := func() (string, bool, error) {
				if tc.global {
					return stringGSubBlock("string.gsub", text, bigPattern, false, yield)
				}
				return stringSubBlock("string.sub", text, bigPattern, false, yield)
			}()
			if err != nil {
				t.Fatalf("literal block over pattern cap = %v, want success", err)
			}
			if !matched {
				t.Fatal("expected matched true for literal block over pattern cap")
			}
			if got != tc.want {
				t.Fatalf("result = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStringLiteralBlockMatchesTemplateOverCaps drives the full script-level
// sub/gsub block path with a literal pattern past the regex pattern cap and
// confirms it produces exactly what the literal template form produces, proving
// the block form shares the literal path's validation-cap-free behavior end to
// end.
func TestStringLiteralBlockMatchesTemplateOverCaps(t *testing.T) {
	t.Parallel()

	pattern := strings.Repeat("p", maxRegexPatternSize+1)
	text := pattern + "tail"
	cfg := Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}

	script := compileScriptWithConfig(t, cfg, `def run(text, pattern)
  block_result = text.gsub(pattern) do |m| "R" end
  template_result = text.gsub(pattern, "R")
  { block: block_result, template: template_result }
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString(text), NewString(pattern)}, CallOptions{})
	if err != nil {
		t.Fatalf("literal block over caps = %v, want success", err)
	}
	hash := got.Hash()
	block := hash["block"]
	template := hash["template"]
	if block.Kind() != KindString || block.String() != "Rtail" {
		t.Fatalf("block result = %v %q, want %q", block.Kind(), block.String(), "Rtail")
	}
	if template.Kind() != KindString || template.String() != block.String() {
		t.Fatalf("template result = %v %q, want it to equal block result %q", template.Kind(), template.String(), block.String())
	}
}
