package runtime

import (
	"context"
	"errors"
	"testing"
)

// largeStringHash builds a hash with n string-keyed integer entries for
// sandbox-limit tests that need an iteration long enough to trip a quota.
func largeStringHash(n int) Value {
	entries := make(map[string]Value, n)
	for i := range n {
		entries["k"+itoaTest(i)] = NewInt(int64(i))
	}
	return NewHash(entries)
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestHashSelectRejectBlockArity verifies that Hash#select and Hash#reject yield
// entries to blocks using Ruby's hash block semantics: a single positional
// parameter receives the entry as a [key, value] pair, two parameters receive
// the key and value separately, and extra parameters bind to nil.
func TestHashSelectRejectBlockArity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   map[string]Value
	}{
		{
			name: "select single pair parameter indexes value",
			source: `def run()
  { a: 1, b: 2 }.select do |pair|
    pair[1] == 1
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "select single pair parameter indexes key",
			source: `def run()
  { a: 1, b: 2 }.select do |pair|
    pair[0] == :a
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "reject single pair parameter indexes key",
			source: `def run()
  { a: 1, b: 2 }.reject do |pair|
    pair[0] == :a
  end
end`,
			want: map[string]Value{"b": NewInt(2)},
		},
		{
			name: "reject single pair parameter indexes value",
			source: `def run()
  { a: 1, b: 2 }.reject do |pair|
    pair[1] == 1
  end
end`,
			want: map[string]Value{"b": NewInt(2)},
		},
		{
			name: "select two parameters bind key and value",
			source: `def run()
  { a: 1, b: 2 }.select do |key, value|
    value == 1
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "reject two parameters bind key and value",
			source: `def run()
  { a: 1, b: 2 }.reject do |key, value|
    value % 2 == 0
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "select extra parameter binds nil",
			source: `def run()
  { a: 1, b: 2 }.select do |key, value, extra|
    extra == nil && value == 1
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "reject extra parameter binds nil",
			source: `def run()
  { a: 1, b: 2 }.reject do |key, value, extra|
    extra != nil || value == 2
  end
end`,
			want: map[string]Value{"a": NewInt(1)},
		},
		{
			name: "select empty hash yields nothing",
			source: `def run()
  {}.select do |pair|
    pair[1] == 1
  end
end`,
			want: map[string]Value{},
		},
		{
			name: "reject empty hash yields nothing",
			source: `def run()
  {}.reject do |pair|
    pair[0] == :a
  end
end`,
			want: map[string]Value{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindHash {
				t.Fatalf("expected hash, got %v", result.Kind())
			}
			compareHash(t, result.Hash(), tc.want)
		})
	}
}

// TestHashSelectRejectParticipateInStepQuota proves the filtering helpers account
// for each block invocation against the step quota so long hash iterations cannot
// escape the sandbox budget.
func TestHashSelectRejectParticipateInStepQuota(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "select pair parameter",
			source: `def run(h); h.select do |pair| pair[1] >= 0 end; end`,
		},
		{
			name:   "reject pair parameter",
			source: `def run(h); h.reject do |pair| pair[1] < 0 end; end`,
		},
		{
			name:   "select key value parameters",
			source: `def run(h); h.select do |key, value| value >= 0 end; end`,
		},
		{
			name:   "reject key value parameters",
			source: `def run(h); h.reject do |key, value| value < 0 end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 40}, tc.source)
			requireCallRuntimeErrorType(t, script, "run", []Value{largeStringHash(1000)}, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

// TestHashSelectRejectHonorCancellation proves the filtering helpers observe a
// canceled context before yielding entries to their block.
func TestHashSelectRejectHonorCancellation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "select pair parameter",
			source: `def run(); { a: 1, b: 2, c: 3 }.select do |pair| pair[1] >= 0 end; end`,
		},
		{
			name:   "reject key value parameters",
			source: `def run(); { a: 1, b: 2, c: 3 }.reject do |key, value| value < 0 end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := script.Call(ctx, "run", nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s under canceled context = %v, want context.Canceled", tc.name, err)
			}
		})
	}
}
