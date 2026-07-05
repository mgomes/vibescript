package runtime

import (
	"context"
	"fmt"
	"testing"
)

// typedHashBenchEntries builds a string-keyed hash payload whose values
// already satisfy hash<string, int>, so a typed boundary crossing is a
// pure validation pass with no coercion.
func typedHashBenchEntries(n int) map[string]Value {
	entries := make(map[string]Value, n)
	for i := range n {
		entries[fmt.Sprintf("key-%d", i)] = NewInt(int64(i))
	}
	return entries
}

// BenchmarkTypedHashNoChangeBoundary measures the cost of passing an
// already-conforming hash through a typed parameter boundary against the
// same call through an untyped parameter. The typed case should approach
// the untyped cost: normalization validates without copying when nothing
// changes.
func BenchmarkTypedHashNoChangeBoundary(b *testing.B) {
	script := compileScriptWithConfig(b, Config{MemoryQuotaBytes: 512 << 20}, `def typed(payload: hash<string, int>)
  payload
end

def untyped(payload)
  payload
end`)
	payload := NewHash(typedHashBenchEntries(10000))
	args := []Value{payload}

	for _, name := range []string{"typed", "untyped"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), name, args, CallOptions{}); err != nil {
					b.Fatalf("call failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkTypedHashNoChangeBoundarySmallSymbolKeys covers the typed-entry
// storage variant at realistic row size: a small symbol-keyed hash crossing a
// typed boundary should validate without materializing a temporary entry
// slice, so the typed call matches the untyped call allocation for allocation.
func BenchmarkTypedHashNoChangeBoundarySmallSymbolKeys(b *testing.B) {
	script := compileScriptWithConfig(b, Config{MemoryQuotaBytes: 512 << 20}, `def typed(payload: hash<symbol, int>)
  payload
end

def untyped(payload)
  payload
end`)
	payload := NewTypedHash(6)
	for i := range 6 {
		if err := payload.HashSet(NewSymbol(fmt.Sprintf("key_%d", i)), NewInt(int64(i))); err != nil {
			b.Fatalf("hash set: %v", err)
		}
	}
	args := []Value{payload}

	for _, name := range []string{"typed", "untyped"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), name, args, CallOptions{}); err != nil {
					b.Fatalf("call failed: %v", err)
				}
			}
		})
	}
}
