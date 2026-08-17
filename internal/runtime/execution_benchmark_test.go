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

// BenchmarkExecutionEachRowsScalarAccumulator iterates hash rows while summing
// into an outer local. The accumulator resolves past the block-iteration region
// boundary to a prefix scope, so before scalar rebinds stopped bumping the
// mutation epoch every iteration invalidated the memoized prefix and re-walked
// the whole receiver, making the loop quadratic in the row count. It is the gate
// against that regression: the suite's other block benchmarks all use pure block
// bodies, which never exercised the invalidation path.
func BenchmarkExecutionEachRowsScalarAccumulator(b *testing.B) {
	script := compileScriptWithEngine(b, benchmarkEngine(), `def run(values)
  total = 0
  values.each do |value|
    total = total + 1
  end
  total
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
    total = total + payload["score"]
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

const recursiveFibSource = `def fib(n)
  if n < 2
    n
  else
    fib(n - 1) + fib(n - 2)
  end
end`

// BenchmarkExecutionRecursiveFib exercises the deep-recursion call path with a
// naive fib. Unlike the loop-shaped benchmarks, it grows the env stack with call
// depth, so it is the suite's gate against call-setup allocation churn (per-call
// environments, argument slices, non-local-return signals). It runs under an
// unlimited memory quota, matching the CLI's default xhigh profile, so the
// measurement reflects the pure call path rather than memory-estimator cost.
func BenchmarkExecutionRecursiveFib(b *testing.B) {
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script := compileScriptWithEngine(b, engine, recursiveFibSource)

	args := []Value{NewInt(20)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "fib", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

// BenchmarkExecutionRecursiveFibQuota runs the same recursion under a positive
// memory quota, so every step pays the reachable-graph estimator walk. It is the
// gate for memory-quota estimator cost on call-heavy workloads: the estimator
// re-walks the whole env stack per check, which scales with recursion depth.
func BenchmarkExecutionRecursiveFibQuota(b *testing.B) {
	engine := MustNewEngine(Config{StepQuota: 100_000_000, MemoryQuotaBytes: 16 << 20, RecursionLimit: 10_000})
	script := compileScriptWithEngine(b, engine, recursiveFibSource)

	args := []Value{NewInt(20)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "fib", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

// The block-driver matrix below covers the axes that select different estimator
// paths, which the suite previously conflated. Three matter independently:
//
//   - Body shape. A pure block body leaves the block-iteration region's memoized
//     prefix intact; a body that writes an outer local resolves past the region
//     boundary into the prefix. Only the second shape exercised the invalidation
//     that once made these loops quadratic, and until recently no benchmark used
//     it, which is why the regression went unnoticed.
//   - Hash receiver origin. A hash built through the host API keeps legacy
//     untyped entries and is never promoted by reads, so it takes a different
//     driver branch than one a script built, and the two failed for different
//     reasons.
//   - Driver. Drivers that only walk (each) and drivers that build a result
//     (select, transform_values, transform_keys) reach the estimator differently,
//     the latter through their result insertions.
//
// Each is a flat top-level benchmark rather than a sub-benchmark of a table: the
// CI smoke gate builds its -bench pattern by alternating threshold names, and Go
// splits -bench patterns on "/", so a sub-benchmark name cannot be gated.

// benchmarkTypedHash builds a hash with typed entries, the representation a
// script-built hash gets on its first write. benchmarkNumericHash's host-built
// map keeps legacy untyped entries instead, so the two exercise different
// branches of every hash driver.
func benchmarkTypedHash(b *testing.B, n int) Value {
	b.Helper()
	h := NewHashWithCapacity(n)
	for i := range n {
		if err := hashSet(h, NewString(fmt.Sprintf("k%03d", i)), NewInt(int64(i))); err != nil {
			b.Fatalf("build typed hash: %v", err)
		}
	}
	return h
}

// benchmarkHashOfRows builds a host-built hash whose values are row hashes: the
// "keyed records" shape an embedder passes in, and the one where re-walking the
// receiver per iteration is genuinely expensive. A hash of bare integers is too
// cheap to walk for a regression to separate from a healthy run by enough to
// gate on.
func benchmarkHashOfRows(n int) Value {
	entries := make(map[string]Value, n)
	statuses := []Value{NewString("open"), NewString("closed")}
	for i := range n {
		entries[fmt.Sprintf("k%03d", i)] = NewHash(map[string]Value{
			"id":     NewInt(int64(i)),
			"status": statuses[i%len(statuses)],
			"score":  NewInt(int64(i * 3)),
		})
	}
	return NewHash(entries)
}

func benchmarkBlockDriver(b *testing.B, source string, arg Value) {
	b.Helper()
	script := compileScriptWithEngine(b, benchmarkEngine(), source)
	args := []Value{arg}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", args, CallOptions{}); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}

// Array drivers, pure body versus outer-local accumulator. The pure variants are
// the controls: they were always fast, so a divergence between the pair isolates
// the region invalidation rather than general iteration cost.
func BenchmarkExecutionEachRowsPureBody(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.each do |value|
    value[:id]
  end
end`, benchmarkHashRows(600))
}

func BenchmarkExecutionMapRowsPureBody(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.map do |value|
    value[:id]
  end
end`, benchmarkHashRows(600))
}

func BenchmarkExecutionMapRowsScalarAccumulator(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  total = 0
  values.map do |value|
    total = total + 1
    value[:id]
  end
  total
end`, benchmarkHashRows(600))
}

// Hash drivers over a host-built (untyped) receiver: the path every hash an
// embedder passes in takes.
func BenchmarkExecutionHashEachHostBuilt(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.each do |key, value|
    value + 1
  end
end`, benchmarkNumericHash(1000))
}

func BenchmarkExecutionHashEachHostBuiltAccumulator(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  total = 0
  values.each do |key, value|
    total = total + value[:id]
  end
  total
end`, benchmarkHashOfRows(1000))
}

func BenchmarkExecutionHashSelectHostBuilt(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.select do |key, value|
    value > 0
  end
end`, benchmarkNumericHash(1000))
}

// The same drivers over a script-built (typed) receiver, which reaches the other
// branch and, for the result-building drivers, the deferred build.
func BenchmarkExecutionHashTransformValuesTyped(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.transform_values do |value|
    value + 1
  end
end`, benchmarkTypedHash(b, 1000))
}

func BenchmarkExecutionHashSelectTyped(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.select do |key, value|
    value > 0
  end
end`, benchmarkTypedHash(b, 1000))
}

// The block body deliberately avoids dispatching a builtin method. A builtin
// called from a block body re-raises the estimator's conservative bypass on
// every call, which is a separate, still-open limitation; including one here
// would leave only a ~4x gap between a healthy run and a regressed one, too
// narrow to gate on. Without it the gap is ~40x, which isolates this driver's
// own behavior.
func BenchmarkExecutionHashTransformKeysHostBuilt(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.transform_keys do |key|
    "x" + key
  end
end`, benchmarkNumericHash(1000))
}

func BenchmarkExecutionHashTransformKeysTyped(b *testing.B) {
	benchmarkBlockDriver(b, `def run(values)
  values.transform_keys do |key|
    "x" + key
  end
end`, benchmarkTypedHash(b, 1000))
}
