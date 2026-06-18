package runtime

import (
	"context"
	"testing"
)

func BenchmarkTasksMapNoGlobals(b *testing.B) {
	benchmarkTasksMapGlobals(b, nil)
}

func BenchmarkTasksMapUnusedLargeGlobal(b *testing.B) {
	benchmarkTasksMapGlobals(b, map[string]Value{
		"unused": benchmarkTaskRows(1000),
	})
}

func benchmarkTasksMapGlobals(b *testing.B, globals map[string]Value) {
	b.Helper()
	script := compileScriptWithConfig(b, Config{MemoryQuotaBytes: 512 << 20}, `def identity(item)
  item
end

def run(items)
  Tasks.map(items, max: 1, with: :identity)
end`)
	items := make([]Value, 40)
	for i := range items {
		items[i] = NewInt(int64(i))
	}
	args := []Value{NewArray(items)}
	opts := CallOptions{Globals: globals}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, opts); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func benchmarkTaskRows(n int) Value {
	rows := make([]Value, n)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{
			"id":     NewInt(int64(i)),
			"name":   NewString("row"),
			"active": NewBool(i%2 == 0),
			"score":  NewFloat(float64(i) / 10),
		})
	}
	return NewHash(map[string]Value{
		"rows": NewArray(rows),
	})
}
