package runtime

import (
	"context"
	"testing"
)

// The key-preserving typed hash drivers now populate their result after the
// block loop rather than during it. Insertion order is observable in Vibescript
// (a typed hash records it), so the deferred build must reproduce exactly the
// order the in-loop build produced: receiver order for transform_values, and
// receiver order restricted to the kept entries for select and reject. select
// and reject compact kept entries in place, which is where an off-by-one would
// reorder or drop entries.
func TestTypedHashBuildAfterLoopPreservesOrder(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "transform_values keeps receiver order",
			source: `def run()
  h = {}
  i = 0
  while i < 12
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.transform_values do |v|
    v * 10
  end.keys.join(",")
end`,
			want: "k0,k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11",
		},
		{
			name: "transform_values maps every value",
			source: `def run()
  h = {}
  i = 0
  while i < 12
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.transform_values do |v|
    v * 10
  end.values.join(",")
end`,
			want: "0,10,20,30,40,50,60,70,80,90,100,110",
		},
		{
			name: "select keeps receiver order among kept entries",
			source: `def run()
  h = {}
  i = 0
  while i < 12
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.select do |k, v|
    v % 3 == 0
  end.keys.join(",")
end`,
			want: "k0,k3,k6,k9",
		},
		{
			name: "select pairs each kept key with its own value",
			source: `def run()
  h = {}
  i = 0
  while i < 12
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.select do |k, v|
    v % 3 == 0
  end.values.join(",")
end`,
			want: "0,3,6,9",
		},
		{
			name: "reject keeps receiver order among kept entries",
			source: `def run()
  h = {}
  i = 0
  while i < 12
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.reject do |k, v|
    v % 3 == 0
  end.keys.join(",")
end`,
			want: "k1,k2,k4,k5,k7,k8,k10,k11",
		},
		{
			name: "select keeping nothing yields an empty hash",
			source: `def run()
  h = {a: 1, b: 2}
  h.select do |k, v|
    false
  end.size.to_s
end`,
			want: "0",
		},
		{
			name: "select keeping everything preserves the whole receiver",
			source: `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.select do |k, v|
    true
  end.keys.join(",")
end`,
			want: "k0,k1,k2,k3,k4,k5",
		},
		{
			name: "reject keeping only the last entry",
			source: `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.reject do |k, v|
    v < 5
  end.keys.join(",")
end`,
			want: "k5",
		},
		{
			name: "receiver is not mutated by the deferred build",
			source: `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.transform_values do |v|
    v * 10
  end
  h.values.join(",")
end`,
			want: "0,1,2,3,4,5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, tt.source)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q", got.String(), tt.want)
			}
		})
	}
}

// A block that raises must still abort the whole driver, even though the result
// hash is now built after the loop rather than incrementally during it.
func TestTypedHashBuildAfterLoopPropagatesBlockError(t *testing.T) {
	for _, name := range []string{"transform_values", "select", "reject"} {
		t.Run(name, func(t *testing.T) {
			source := `def run()
  h = {a: 1, b: 2, c: 3}
  h.` + name + ` do |k, v|
    raise "boom"
  end
end`
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("expected the block error to propagate")
			}
		})
	}
}
