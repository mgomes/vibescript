package runtime

import (
	"context"
	"strings"
	"testing"
)

// hostBuiltStringHash returns a hash built through the host API, which keeps
// legacy untyped entries and is never promoted by reads. It is the receiver
// shape every hash an embedder passes in has, and the one that takes
// transform_keys' legacy branch.
func hostBuiltStringHash(n int) Value {
	m := make(map[string]Value, n)
	for i := range n {
		m[hostHashKey(i)] = NewInt(int64(i))
	}
	return NewHash(m)
}

// The legacy branch defers its result build like the typed one, so it inherits
// the same observable risks: insertion order, collision resolution, and key
// identity. These mirror the typed-branch tests against a host-built receiver.
func TestLegacyTransformKeysBuildAfterLoopSemantics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "insertion order follows the receiver's sorted keys",
			source: `def run(h)
  h.transform_keys do |k|
    "x" + k.to_s
  end.keys.join(",")
end`,
			want: "xkaaa,xkbaa,xkcaa,xkdaa,xkeaa,xkfaa",
		},
		{
			name: "each remapped key keeps its own value",
			source: `def run(h)
  h.transform_keys do |k|
    "x" + k.to_s
  end.values.join(",")
end`,
			want: "0,1,2,3,4,5",
		},
		{
			name: "colliding keys collapse to one entry with the last value",
			source: `def run(h)
  out = h.transform_keys do |k|
    "same"
  end
  out.keys.join(",") + "|" + out.values.join(",") + "|" + out.size.to_s
end`,
			want: "same|5|1",
		},
		{
			name: "partial collision preserves surviving order",
			source: `def run(h)
  out = h.transform_keys do |k|
    if k.to_s == "kaaa" || k.to_s == "kdaa"
      "dup"
    else
      k.to_s
    end
  end
  out.keys.join(",") + "|" + out.values.join(",")
end`,
			want: "dup,kbaa,kcaa,keaa,kfaa|3,1,2,4,5",
		},
		{
			name: "an unsupported key still fails",
			source: `def run(h)
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
			got, err := script.Call(context.Background(), "run", []Value{hostBuiltStringHash(6)}, CallOptions{})
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

// An array key can be mutated in place after its entry is processed, so a
// deferred insert would canonicalize it in its final state and collapse two
// entries onto one identity. The legacy branch flushes and inserts inline from
// the first array key onward, exactly as the typed branch does.
func TestLegacyTransformKeysArrayKeyFallbackKeepsIdentity(t *testing.T) {
	source := `def run(h)
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
	got, err := script.Call(context.Background(), "run", []Value{hostBuiltStringHash(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got.String() != "2|0,1" {
		t.Fatalf("got %q, want %q (entries collapsed onto a re-canonicalized array key)", got.String(), "2|0,1")
	}
}

// The fallback can trigger partway through, so entries buffered before the first
// array key are flushed and must keep their order and values relative to the
// entries inserted inline after it.
func TestLegacyTransformKeysMixedKeyKindsPreserveOrder(t *testing.T) {
	source := `def run(h)
  out = h.transform_keys do |k|
    if k.to_s == "kdaa"
      [3]
    else
      k.to_s
    end
  end
  out.values.join(",") + "|" + out.size.to_s
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
	got, err := script.Call(context.Background(), "run", []Value{hostBuiltStringHash(6)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got.String() != "0,1,2,3,4,5|6" {
		t.Fatalf("got %q, want %q", got.String(), "0,1,2,3,4,5|6")
	}
}

// A block can mutate or delete an entry of the receiver while iterating. Values
// are captured when the block runs, not when the deferred build flushes, so an
// already-processed entry keeps the value it contributed at the time -- matching
// what inserting inline gave, and what the typed branch gets from its
// snapshotted entries.
func TestLegacyTransformKeysCapturesValuesAtBlockTime(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
		want   string
	}{
		{
			name:   "rebinding an earlier entry",
			mutate: `h["kaaa"] = 99`,
			want:   "0,1,2",
		},
		{
			name:   "deleting an earlier entry",
			mutate: `h.delete("kaaa")`,
			want:   "0,1,2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := `def run(h)
  n = 0
  out = h.transform_keys do |k|
    n = n + 1
    if n == 2
      ` + tt.mutate + `
    end
    "x" + k
  end
  out.values.join(",")
end`
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: 8 << 20}, source)
			got, err := script.Call(context.Background(), "run", []Value{hostBuiltStringHash(3)}, CallOptions{})
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q (deferred flush read the receiver's later value)", got.String(), tt.want)
			}
		})
	}
}
