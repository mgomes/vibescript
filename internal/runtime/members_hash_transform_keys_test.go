package runtime

import (
	"context"
	"strings"
	"testing"
)

// hash.transform_keys populates its result after the block loop. The block
// produces the keys, so unlike the key-preserving drivers this cannot decide up
// front whether deferring is safe: it buffers until the block yields an array
// key (the only mutable key kind), then flushes and inserts inline for the rest.
// These pin the observable consequences of that -- insertion order, collision
// resolution, and key identity -- which are exactly what a deferred build can
// get wrong.
func TestTransformKeysBuildAfterLoopSemantics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "insertion order follows the receiver",
			source: `def run()
  h = {}
  i = 0
  while i < 8
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.transform_keys do |k|
    "x" + k
  end.keys.join(",")
end`,
			want: "xk0,xk1,xk2,xk3,xk4,xk5,xk6,xk7",
		},
		{
			name: "each remapped key keeps its own value",
			source: `def run()
  h = {}
  i = 0
  while i < 8
    h["k" + i.to_s] = i
    i = i + 1
  end
  h.transform_keys do |k|
    "x" + k
  end.values.join(",")
end`,
			want: "0,1,2,3,4,5,6,7",
		},
		{
			name: "colliding keys keep the first position and the last value",
			source: `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  out = h.transform_keys do |k|
    "same"
  end
  out.keys.join(",") + "|" + out.values.join(",") + "|" + out.size.to_s
end`,
			want: "same|5|1",
		},
		{
			name: "partial collision preserves surviving order",
			source: `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  out = h.transform_keys do |k|
    if k == "k0" || k == "k3"
      "dup"
    else
      k
    end
  end
  out.keys.join(",") + "|" + out.values.join(",")
end`,
			want: "dup,k1,k2,k4,k5|3,1,2,4,5",
		},
		{
			name: "an unsupported key still fails",
			source: `def run()
  h = {a: 1}
  h.transform_keys do |k|
    {nested: 1}
  end
end`,
			want: "!error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, tt.source)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if tt.want == "!error" {
				if err == nil {
					t.Fatalf("expected an unsupported-key error, got %q", got.String())
				}
				if !strings.Contains(err.Error(), "unsupported hash key") {
					t.Fatalf("expected an unsupported-key error, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q", got.String(), tt.want)
			}
		})
	}
}

// A block that yields an array key makes the deferred build unsafe, because that
// key can be mutated in place after its entry is processed. The loop flushes
// what it buffered and inserts inline from then on, so each key is canonicalized
// in the state it had when its entry was processed. Mutating an already-yielded
// array key must therefore not collapse it onto a later key's identity.
func TestTransformKeysArrayKeyFallbackKeepsIdentity(t *testing.T) {
	source := `def run()
  h = {}
  h["a"] = 1
  h["b"] = 2
  produced = []
  n = 0
  out = h.transform_keys do |k|
    n = n + 1
    key = [n]
    produced.push(key)
    if n == 2
      produced[0][0] = 2
    end
    key
  end
  out.size.to_s + "|" + out.values.join(",")
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got.String() != "2|1,2" {
		t.Fatalf("got %q, want %q (entries collapsed onto a re-canonicalized array key)", got.String(), "2|1,2")
	}
}

// The fallback triggers partway through, so entries buffered before the first
// array key are flushed and must keep both their order and their values relative
// to the entries inserted inline after it.
func TestTransformKeysMixedKeyKindsPreserveOrder(t *testing.T) {
	source := `def run()
  h = {}
  i = 0
  while i < 6
    h["k" + i.to_s] = i
    i = i + 1
  end
  out = h.transform_keys do |k|
    if k == "k3"
      [3]
    else
      k
    end
  end
  out.values.join(",") + "|" + out.size.to_s
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got.String() != "0,1,2,3,4,5|6" {
		t.Fatalf("got %q, want %q", got.String(), "0,1,2,3,4,5|6")
	}
}
