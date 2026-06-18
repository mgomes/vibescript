package runtime

import (
	"context"
	"fmt"
	"testing"
)

type benchmarkDBCapability struct{}

func (benchmarkDBCapability) Find(ctx context.Context, req DBFindRequest) (Value, error) {
	return NewHash(map[string]Value{
		"id":    req.ID,
		"score": NewInt(1),
	}), nil
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
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(n)
  total = 0
  for i in 1..n
    total = total + i
  end
  total
end`)

	args := []Value{NewInt(400)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionArrayPipeline(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
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

	values := make([]Value, 600)
	for i := range values {
		values[i] = NewInt(int64(i))
	}
	args := []Value{NewArray(values)}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionGroupByHashRowsLowCardinality(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
  values.group_by do |value|
    value[:status]
  end
end`)

	args := []Value{benchmarkHashRows(600)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionGroupByStableHashRowsLowCardinality(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
  values.group_by_stable do |value|
    value[:status]
  end
end`)

	args := []Value{benchmarkHashRows(600)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionPartitionHashRowsLowCardinality(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
  values.partition do |value|
    value[:active]
  end
end`)

	args := []Value{benchmarkHashRows(600)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionHashTransformValuesLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
  values.transform_values do |value|
    value + 1
  end
end`)

	args := []Value{benchmarkNumericHash(600)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func benchmarkHashRows(n int) Value {
	rows := make([]Value, n)
	statuses := []Value{NewString("open"), NewString("closed")}
	for i := range rows {
		rows[i] = NewHash(map[string]Value{
			"id":     NewInt(int64(i)),
			"status": statuses[i%len(statuses)],
			"active": NewBool(i%2 == 0),
			"amount": NewInt(int64(i * 10)),
		})
	}
	return NewArray(rows)
}

func benchmarkNumericHash(n int) Value {
	entries := make(map[string]Value, n)
	for i := range n {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	return NewHash(entries)
}

func BenchmarkExecutionArrayPushAccumulation(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(n)
  out = []
  for i in 1..n
    out = out.push(i)
  end
  out.size
end`)

	args := []Value{NewInt(400)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionArrayConcatAccumulation(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(n)
  out = []
  for i in 1..n
    out = out + [i]
  end
  out.size
end`)

	args := []Value{NewInt(400)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionMethodDispatchLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `class Counter
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

	args := []Value{NewInt(300)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionCapabilityFindLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(n)
  total = 0
  for i in 1..n
    row = db.find("Player", "player-1")
    total = total + row[:score]
  end
  total
end`)

	args := []Value{NewInt(300)}
	opts := CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", benchmarkDBCapability{}),
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, opts); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionJSONParseLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(raw, n)
  total = 0
  for i in 1..n
    payload = JSON.parse(raw)
    total = total + payload[:score]
  end
  total
end`)

	args := []Value{
		NewString(`{"score":7,"tags":["a","b","c"],"active":true}`),
		NewInt(80),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionJSONStringifyLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(payload, n)
  out = ""
  for i in 1..n
    out = JSON.stringify(payload)
  end
  out
end`)

	payload := NewHash(map[string]Value{
		"id":     NewString("player-7"),
		"score":  NewInt(42),
		"active": NewBool(true),
		"tags":   NewArray([]Value{NewString("a"), NewString("b"), NewString("c")}),
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

func BenchmarkExecutionRegexReplaceAllLoop(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(text, n)
  out = ""
  for i in 1..n
    out = Regex.replace_all(text, "ID-[0-9]+", "X")
  end
  out
end`)

	args := []Value{
		NewString("ID-12 ID-34 ID-56 ID-78 ID-90"),
		NewInt(80),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

func BenchmarkExecutionTallyLoop(b *testing.B) {
	values := make([]Value, 600)
	for i := range values {
		if i%2 == 0 {
			values[i] = NewString("active")
		} else {
			values[i] = NewString("complete")
		}
	}
	benchmarkExecutionTallyLoop(b, values)
}

func BenchmarkExecutionTallyUniqueLoop(b *testing.B) {
	values := make([]Value, 600)
	for i := range values {
		values[i] = NewString(fmt.Sprintf("status-%03d", i))
	}
	benchmarkExecutionTallyLoop(b, values)
}

func benchmarkExecutionTallyLoop(b *testing.B, values []Value) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values, n)
  out = {}
  for i in 1..n
    out = values.tally
  end
  out
end`)

	args := []Value{NewArray(values), NewInt(80)}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}
