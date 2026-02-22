package vibes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type benchmarkEventsCapability struct{}

func (benchmarkEventsCapability) Publish(ctx context.Context, req EventPublishRequest) (Value, error) {
	return NewHash(map[string]Value{
		"topic": NewString(req.Topic),
		"ok":    NewBool(true),
	}), nil
}

func benchmarkContextResolver(context.Context) (Value, error) {
	return NewHash(map[string]Value{
		"player_id": NewString("player-1"),
		"tenant_id": NewString("tenant-1"),
	}), nil
}

func benchmarkSourceFromFile(b *testing.B, rel string) string {
	b.Helper()
	path := filepath.Join("..", filepath.FromSlash(rel))
	source, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

func benchmarkEngineWithModules() *Engine {
	return MustNewEngine(Config{
		StepQuota:        2_000_000,
		MemoryQuotaBytes: 2 << 20,
		ModulePaths:      []string{filepath.FromSlash("testdata/modules")},
	})
}

func BenchmarkCompileControlFlowWorkload(b *testing.B) {
	source := benchmarkSourceFromFile(b, "tests/complex/loops.vibe")
	engine := benchmarkEngine()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.Compile(source); err != nil {
			b.Fatalf("compile failed: %v", err)
		}
	}
}

func BenchmarkCompileTypedWorkload(b *testing.B) {
	source := benchmarkSourceFromFile(b, "tests/complex/typed.vibe")
	engine := benchmarkEngine()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.Compile(source); err != nil {
			b.Fatalf("compile failed: %v", err)
		}
	}
}

func BenchmarkCompileMassiveWorkload(b *testing.B) {
	source := benchmarkSourceFromFile(b, "tests/complex/massive.vibe")
	engine := benchmarkEngine()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := engine.Compile(source); err != nil {
			b.Fatalf("compile failed: %v", err)
		}
	}
}

func BenchmarkCallShortScript(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run
  1
end`)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkCallControlFlowWorkload(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(limit)
  i = 0
  total = 0

  while i < limit
    i = i + 1
    if i % 2 == 0
      next
    end
    if i > 75
      break
    end
    total = total + i
  end

  total
end`)

	args := []Value{NewInt(200)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkCallTypedCompositeValidation(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(rows: array<{ id: string, values: array<int> }>) -> int
  total = 0
  rows.each do |row: { id: string, values: array<int> }|
    row[:values].each do |value: int|
      total = total + value
    end
  end
  total
end`)

	rows := make([]Value, 40)
	for i := range rows {
		values := make([]Value, 4)
		for j := range values {
			values[j] = NewInt(int64(i + j))
		}
		rows[i] = NewHash(map[string]Value{
			"id":     NewString("row"),
			"values": NewArray(values),
		})
	}

	args := []Value{NewArray(rows)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionCapabilityWorkflowLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(n)
  total = 0
  for i in 1..n
    player_id = ctx[:player_id]
    row = db.find("Player", player_id)
    events.publish("scores.seen", { player_id: row[:id], score: row[:score] })
    total = total + row[:score]
  end
  total
end`)

	args := []Value{NewInt(200)}
	opts := CallOptions{Capabilities: []CapabilityAdapter{
		MustNewDBCapability("db", benchmarkDBCapability{}),
		MustNewEventsCapability("events", benchmarkEventsCapability{}),
		MustNewContextCapability("ctx", benchmarkContextResolver),
	}}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, opts); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionTimeParseFormatLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(raw, n)
  out = ""
  for i in 1..n
    out = Time.parse(raw).format("2006-01-02T15:04:05Z07:00")
  end
  out
end`)

	args := []Value{NewString("2026-02-21T12:45:00Z"), NewInt(80)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionStringNormalizeLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(text, n)
  out = ""
  for i in 1..n
    out = text.squish.downcase.split(" ").join("-")
  end
  out
end`)

	args := []Value{NewString("  VibeScript   Runtime   Benchmarks  "), NewInt(80)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionHashTransformLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(payload, n)
  out = {}
  for i in 1..n
    merged = payload.merge({ seen: i })
    selected = merged.slice(:id, :name, :score, :seen)
    out = selected.transform_values do |value|
      value
    end
  end
  out
end`)

	payload := NewHash(map[string]Value{
		"id":       NewString("player-7"),
		"name":     NewString("alex"),
		"score":    NewInt(42),
		"active":   NewBool(true),
		"region":   NewString("us-east-1"),
		"attempts": NewInt(3),
	})
	args := []Value{payload, NewInt(80)}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkModuleRequireCacheHit(b *testing.B) {
	engine := benchmarkEngineWithModules()
	script := compileScriptWithEngine(b, engine, `def run(value)
  mod = require("helper")
  mod.double(value)
end`)

	args := []Value{NewInt(12)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkModuleRequireCacheMiss(b *testing.B) {
	moduleRoot := b.TempDir()
	modulePath := filepath.Join(moduleRoot, "dynamic.vibe")
	if err := os.WriteFile(modulePath, []byte("def value\n  7\nend\n"), 0o644); err != nil {
		b.Fatalf("write module: %v", err)
	}

	engine := MustNewEngine(Config{
		StepQuota:        2_000_000,
		MemoryQuotaBytes: 2 << 20,
		ModulePaths:      []string{moduleRoot},
	})
	script := compileScriptWithEngine(b, engine, `def run
  mod = require("dynamic")
  mod.value
end`)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		engine.ClearModuleCache()
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkModuleRequireCyclePath(b *testing.B) {
	moduleRoot := b.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "a.vibe"), []byte(`require("b")
def value
  1
end
`), 0o644); err != nil {
		b.Fatalf("write module a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "b.vibe"), []byte(`require("a")
def value
  2
end
`), 0o644); err != nil {
		b.Fatalf("write module b: %v", err)
	}

	engine := MustNewEngine(Config{
		StepQuota:        2_000_000,
		MemoryQuotaBytes: 2 << 20,
		ModulePaths:      []string{moduleRoot},
	})
	script := compileScriptWithEngine(b, engine, `def run
  require("a")
end`)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
			b.Fatalf("expected cycle error")
		}
	}
}

func BenchmarkComplexRunAnalytics(b *testing.B) {
	engine := benchmarkEngine()
	script := compileScriptFromFileWithEngine(b, engine, filepath.Join("..", "tests", "complex", "analytics.vibe"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkComplexRunTyped(b *testing.B) {
	engine := benchmarkEngine()
	script := compileScriptFromFileWithEngine(b, engine, filepath.Join("..", "tests", "complex", "typed.vibe"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkComplexRunMassive(b *testing.B) {
	engine := benchmarkEngine()
	script := compileScriptFromFileWithEngine(b, engine, filepath.Join("..", "tests", "complex", "massive.vibe"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}
