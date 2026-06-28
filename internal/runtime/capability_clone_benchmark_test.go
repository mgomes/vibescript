package runtime

import (
	"context"
	"fmt"
	"testing"
)

// payloadDBCapability is a Database stub whose read methods return a
// prebuilt payload. The capability boundary deep-clones every result
// before handing it to the script, so sharing one Value across calls is
// safe and mirrors a host that owns the underlying data.
type payloadDBCapability struct {
	result Value
}

type largeReturnJobQueue struct {
	result Value
}

func (c payloadDBCapability) Find(ctx context.Context, req DBFindRequest) (Value, error) {
	return c.result, nil
}

func (c payloadDBCapability) Query(ctx context.Context, req DBQueryRequest) (Value, error) {
	return c.result, nil
}

func (c payloadDBCapability) Update(ctx context.Context, req DBUpdateRequest) (Value, error) {
	return NewNil(), nil
}

func (c payloadDBCapability) Sum(ctx context.Context, req DBSumRequest) (Value, error) {
	return NewInt(0), nil
}

func (c payloadDBCapability) Each(ctx context.Context, req DBEachRequest) ([]Value, error) {
	return nil, nil
}

func (q largeReturnJobQueue) Enqueue(ctx context.Context, job JobQueueJob) (Value, error) {
	return q.result, nil
}

// capabilityBenchRows builds an array of n hashes with realistic mixed
// scalar fields, approximating a row set crossing the capability
// boundary in either direction.
func capabilityBenchRows(n int) Value {
	rows := make([]Value, n)
	for i := range n {
		rows[i] = NewHash(map[string]Value{
			"id":     NewString(fmt.Sprintf("row-%d", i)),
			"name":   NewString(fmt.Sprintf("Player %d", i)),
			"score":  NewInt(int64(i * 7)),
			"ratio":  NewFloat(float64(i) * 0.25),
			"active": NewBool(i%2 == 0),
			"region": NewString("us-east-1"),
		})
	}
	return NewArray(rows)
}

// capabilityBenchNested builds a hash chain depth levels deep so the
// recursive validate and clone walks are exercised on nesting rather
// than breadth.
func capabilityBenchNested(depth int) Value {
	val := NewString("leaf")
	for range depth {
		val = NewHash(map[string]Value{
			"label": NewString("node"),
			"child": val,
		})
	}
	return val
}

// capabilityPayloadEngine returns an engine with quotas sized so the
// large payloads below never trip the step or memory limits.
func capabilityPayloadEngine() *Engine {
	return MustNewEngine(Config{
		StepQuota:        2_000_000,
		MemoryQuotaBytes: 512 << 20,
	})
}

// BenchmarkCapabilityContractLargeReturn measures the host -> script
// direction: db.query returns an array of N row hashes, which the
// contracted boundary validates (data-only and cycle walks) and
// deep-clones before the script sees it.
func BenchmarkCapabilityContractLargeReturn(b *testing.B) {
	script := compileScriptWithEngine(b, capabilityPayloadEngine(), `def run()
  db.query("Players").size
end`)

	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows_%d", n), func(b *testing.B) {
			opts := callOptionsWithCapabilities(
				MustNewDBCapability("db", payloadDBCapability{result: capabilityBenchRows(n)}),
			)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", nil, opts); err != nil {
					b.Fatalf("call failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkJobQueueCapabilityLargeReturn(b *testing.B) {
	script := compileScriptWithEngine(b, capabilityPayloadEngine(), `def run()
  jobs.enqueue("demo", {}).size
end`)

	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows_%d", n), func(b *testing.B) {
			opts := callOptionsWithCapabilities(
				MustNewJobQueueCapability("jobs", largeReturnJobQueue{result: capabilityBenchRows(n)}),
			)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", nil, opts); err != nil {
					b.Fatalf("call failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkCapabilityContractLargeArgs measures the script -> host
// direction: the script passes a hash wrapping N row hashes to
// db.update, so the boundary validates the attributes argument and
// deep-clones it into the host request.
func BenchmarkCapabilityContractLargeArgs(b *testing.B) {
	script := compileScriptWithEngine(b, capabilityPayloadEngine(), `def run(payload)
  db.update("Players", "row-1", payload)
end`)

	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows_%d", n), func(b *testing.B) {
			args := []Value{NewHash(map[string]Value{
				"rows": capabilityBenchRows(n),
			})}
			opts := callOptionsWithCapabilities(
				MustNewDBCapability("db", payloadDBCapability{result: NewNil()}),
			)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", args, opts); err != nil {
					b.Fatalf("call failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkCapabilityContractDeepNestedReturn exercises the recursive
// validate and clone walks on depth instead of breadth: db.find returns
// a hash chain nested 10000 levels deep.
func BenchmarkCapabilityContractDeepNestedReturn(b *testing.B) {
	script := compileScriptWithEngine(b, capabilityPayloadEngine(), `def run()
  db.find("Tree", "root")[:label]
end`)

	opts := callOptionsWithCapabilities(
		MustNewDBCapability("db", payloadDBCapability{result: capabilityBenchNested(10000)}),
	)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := script.Call(context.Background(), "run", nil, opts); err != nil {
			b.Fatalf("call failed: %v", err)
		}
	}
}
