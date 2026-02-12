package vibes

import (
	"context"
	"testing"
)

func benchmarkEngine() *Engine {
	return MustNewEngine(Config{
		StepQuota:        2_000_000,
		MemoryQuotaBytes: 2 << 20,
	})
}

func BenchmarkExecutionArithmeticLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(n)
  total = 0
  for i in 1..n
    total = total + i
  end
  total
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	args := []Value{NewInt(400)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionArrayPipeline(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(values)
  mapped = values.map do |v|
    v + 1
  end

  selected = mapped.select do |v|
    v % 2 == 0
  end

  selected.reduce(0) do |acc, v|
    acc + v
  end
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	values := make([]Value, 600)
	for i := range values {
		values[i] = NewInt(int64(i))
	}
	args := []Value{NewArray(values)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionMethodDispatchLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`class Counter
  def initialize(seed)
    @value = seed
  end

  def add(delta)
    @value = @value + delta
  end

  def value
    @value
  end
end

def run(n)
  counter = Counter.new(0)
  for i in 1..n
    counter.add(i)
  end
  counter.value
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	args := []Value{NewInt(300)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}
