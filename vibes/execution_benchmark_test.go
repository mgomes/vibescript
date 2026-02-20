package vibes

import (
	"context"
	"testing"
)

type benchmarkDBCapability struct{}

func (benchmarkDBCapability) Find(ctx context.Context, req DBFindRequest) (Value, error) {
	return NewHash(map[string]Value{"score": NewInt(1)}), nil
}

func (benchmarkDBCapability) Query(ctx context.Context, req DBQueryRequest) (Value, error) {
	return NewArray(nil), nil
}

func (benchmarkDBCapability) Update(ctx context.Context, req DBUpdateRequest) (Value, error) {
	return NewNil(), nil
}

func (benchmarkDBCapability) Sum(ctx context.Context, req DBSumRequest) (Value, error) {
	return NewInt(0), nil
}

func (benchmarkDBCapability) Each(ctx context.Context, req DBEachRequest) ([]Value, error) {
	return nil, nil
}

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

func BenchmarkExecutionCapabilityFindLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(n)
  total = 0
  for i in 1..n
    row = db.find("Player", "player-1")
    total = total + row[:score]
  end
  total
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	args := []Value{NewInt(300)}
	opts := CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", benchmarkDBCapability{}),
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, opts); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionJSONParseLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(raw, n)
  total = 0
  for i in 1..n
    payload = JSON.parse(raw)
    total = total + payload[:score]
  end
  total
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	args := []Value{
		NewString(`{"score":7,"tags":["a","b","c"],"active":true}`),
		NewInt(80),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionJSONStringifyLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(payload, n)
  out = ""
  for i in 1..n
    out = JSON.stringify(payload)
  end
  out
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	payload := NewHash(map[string]Value{
		"id":     NewString("player-7"),
		"score":  NewInt(42),
		"active": NewBool(true),
		"tags":   NewArray([]Value{NewString("a"), NewString("b"), NewString("c")}),
	})
	args := []Value{payload, NewInt(80)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionRegexReplaceAllLoop(b *testing.B) {
	engine := benchmarkEngine()
	script, err := engine.Compile(`def run(text, n)
  out = ""
  for i in 1..n
    out = Regex.replace_all(text, "ID-[0-9]+", "X")
  end
  out
end`)
	if err != nil {
		b.Fatalf("compile failed: %v", err)
	}

	args := []Value{
		NewString("ID-12 ID-34 ID-56 ID-78 ID-90"),
		NewInt(80),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}
