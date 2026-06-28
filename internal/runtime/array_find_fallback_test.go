package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestArrayFindOptionalFallbackCallable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def fallback(unused = nil)
  :none
end

def exploding_fallback(unused = nil)
  raise "fallback should not run"
end

def miss_with_fallback
  [1, 2].find(fallback) { |x| x > 3 }
end

def miss_without_fallback
  [1, 2].find { |x| x > 3 }
end

def miss_with_nil_fallback
  maybe = nil
  [1, 2].find(maybe) { |x| x > 3 }
end

def hit_ignores_fallback
  [1, 2].find(exploding_fallback) { |x| x == 2 }
end

def raw_fallback
  [1, 2].find("none") { |x| x > 3 }
end`)

	if got := callScript(t, context.Background(), script, "miss_with_fallback", nil, CallOptions{}); !got.Equal(NewSymbol("none")) {
		t.Fatalf("miss_with_fallback = %#v, want :none", got)
	}
	if got := callScript(t, context.Background(), script, "miss_without_fallback", nil, CallOptions{}); !got.Equal(NewNil()) {
		t.Fatalf("miss_without_fallback = %#v, want nil", got)
	}
	if got := callScript(t, context.Background(), script, "miss_with_nil_fallback", nil, CallOptions{}); !got.Equal(NewNil()) {
		t.Fatalf("miss_with_nil_fallback = %#v, want nil", got)
	}
	if got := callScript(t, context.Background(), script, "hit_ignores_fallback", nil, CallOptions{}); !got.Equal(NewInt(2)) {
		t.Fatalf("hit_ignores_fallback = %#v, want 2", got)
	}
	requireCallErrorContains(t, script, "raw_fallback", nil, CallOptions{}, "attempted to call non-callable value")
}

func TestArrayFindFallbackChargesLiveReceiver(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(30_000)
	fallbackResult := NewString(strings.Repeat("x", 256*1024))
	block := NewBlock([]Param{{Kind: ParamNormal, Name: "item"}}, nil, newEnv(nil))
	calls := 0
	fallback := NewBuiltin("test.find_fallback", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		calls++
		return fallbackResult, nil
	})

	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	receiverBytes := probe.estimateMemoryUsage(receiver)
	combinedBytes := probe.estimateMemoryUsage(receiver, fallbackResult)
	quota := receiverBytes + (combinedBytes-receiverBytes)/2
	if quota <= receiverBytes || quota >= combinedBytes {
		t.Fatalf("quota %d must fit receiver %d and reject receiver+result %d", quota, receiverBytes, combinedBytes)
	}

	tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, tight, receiver, "find", []Value{fallback}, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", calls)
	}

	roomy := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: combinedBytes + 64*1024}
	got, err := callArrayMember(t, roomy, receiver, "find", []Value{fallback}, block)
	if err != nil {
		t.Fatalf("array.find with room for receiver and fallback result returned error: %v", err)
	}
	if !got.Equal(fallbackResult) {
		t.Fatalf("array.find fallback result = %#v, want %#v", got, fallbackResult)
	}
}
