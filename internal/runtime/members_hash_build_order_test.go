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

// An array is the only hash key kind that can be mutated in place, so it is the
// only one whose lookup identity can differ between the moment its entry is
// processed and the moment a deferred build would insert it. A block that
// mutates an earlier key during a later iteration must not cause two entries to
// collapse onto one identity: the key-preserving drivers fall back to inserting
// during the loop whenever the receiver has an array key.
func TestTypedHashArrayKeyMutationKeepsSnapshotIdentity(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		want   string
	}{
		{name: "transform_values", driver: "transform_values do |v|\n    n = n + 1\n    if n == 2\n      k1[0] = 2\n    end\n    v\n  end", want: "2|a,b"},
		{name: "select", driver: "select do |k, v|\n    n = n + 1\n    if n == 2\n      k1[0] = 2\n    end\n    true\n  end", want: "2|a,b"},
		{name: "reject", driver: "reject do |k, v|\n    n = n + 1\n    if n == 2\n      k1[0] = 2\n    end\n    false\n  end", want: "2|a,b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := `def run()
  k1 = [1]
  k2 = [2]
  h = {}
  h[k1] = "a"
  h[k2] = "b"
  n = 0
  out = h.` + tt.driver + `
  out.size.to_s + "|" + out.values.join(",")
end`
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q (entries collapsed onto a re-canonicalized key)", got.String(), tt.want)
			}
		})
	}
}
