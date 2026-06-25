package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestStringScanCaptureShape verifies that String#scan mirrors Ruby's
// capture-aware result shape: no groups yields the full match strings, one or
// more groups yields an array per match holding each captured substring, and
// optional groups that did not participate become nil.
func TestStringScanCaptureShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "no captures returns full matches",
			source: `def run() "a1 b2".scan("[a-z][0-9]") end`,
			want:   []Value{NewString("a1"), NewString("b2")},
		},
		{
			name:   "single capture returns nested single-element arrays",
			source: `def run() "foobar".scan("(o)") end`,
			want: []Value{
				NewArray([]Value{NewString("o")}),
				NewArray([]Value{NewString("o")}),
			},
		},
		{
			name:   "multiple captures return nested arrays",
			source: `def run() "a1 b2".scan("([a-z])([0-9])") end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("1")}),
				NewArray([]Value{NewString("b"), NewString("2")}),
			},
		},
		{
			name:   "optional unmatched capture becomes nil",
			source: `def run() "a-b-c".scan("(\\w)(-)?") end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewString("-")}),
				NewArray([]Value{NewString("b"), NewString("-")}),
				NewArray([]Value{NewString("c"), NewNil()}),
			},
		},
		{
			name:   "empty capture preserved distinct from nil",
			source: `def run() "x".scan("(x)(y)?(z*)") end`,
			want: []Value{
				NewArray([]Value{NewString("x"), NewNil(), NewString("")}),
			},
		},
		{
			name:   "no match returns empty array",
			source: `def run() "abc".scan("z") end`,
			want:   []Value{},
		},
		{
			name:   "no match with captures returns empty array",
			source: `def run() "abc".scan("(z)(z)") end`,
			want:   []Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			compareArrays(t, got, tt.want)
		})
	}
}

// TestStringScanArgumentRejection covers the misuse cases String#scan must
// reject before attempting to match.
func TestStringScanArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "missing pattern",
			source: `def run() "abc".scan() end`,
			want:   "string.scan expects exactly one pattern",
		},
		{
			name:   "extra positional argument",
			source: `def run() "abc".scan("a", "b") end`,
			want:   "string.scan expects exactly one pattern",
		},
		{
			name:   "non-string pattern",
			source: `def run() "abc".scan(1) end`,
			want:   "string.scan pattern must be string",
		},
		{
			name:   "invalid regex",
			source: `def run() "abc".scan("(") end`,
			want:   "string.scan invalid regex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

// TestStringScanCaptureMemoryQuota verifies that a capture-aware scan whose
// accumulated nested-array result would exceed the memory quota fails with the
// limit error instead of materializing an unbounded array. Each match builds a
// fresh nested array, so a subject that matches at every position produces one
// nested array per character; under a tight quota the running accumulator must
// trip before the whole result is built.
func TestStringScanCaptureMemoryQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, `def run(text)
  text.scan("(a)")
end`)

	subject := NewString(strings.Repeat("a", 50_000))
	requireRunMemoryQuotaError(t, script, []Value{subject}, CallOptions{})
}

// TestStringScanCaptureMemoryQuotaUnderAmpleMemory confirms the same large scan
// completes when the memory quota is generous, proving the incremental bound is
// not rejecting results the post-call check would accept.
func TestStringScanCaptureMemoryQuotaUnderAmpleMemory(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 << 20}, `def run(text)
  text.scan("(a)").size
end`)

	const count = 50_000
	subject := NewString(strings.Repeat("a", count))
	got, err := script.Call(context.Background(), "run", []Value{subject}, CallOptions{})
	if err != nil {
		t.Fatalf("scan under ample memory = %v, want success", err)
	}
	if got.Kind() != KindInt || got.Int() != count {
		t.Fatalf("scan size = %v, want int %d", got, count)
	}
}

// TestStringScanStepQuota verifies that scan charges a step per match attempt, so
// a subject yielding far more matches than the step quota allows trips the step
// limit even when the memory quota is ample.
func TestStringScanStepQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, `def run(text)
  text.scan("a")
end`)

	subject := NewString(strings.Repeat("a", 100_000))
	requireCallRuntimeErrorType(t, script, "run", []Value{subject}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestStringScanContextCancellation confirms a canceled context aborts the scan:
// step() polls cancellation on its first invocation, so even a tiny subject is
// enough to observe it.
func TestStringScanContextCancellation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  "aaa".scan("a")
end`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan under canceled context = %v, want context.Canceled", err)
	}
}
