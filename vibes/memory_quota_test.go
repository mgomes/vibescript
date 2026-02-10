package vibes

import (
	"context"
	"strings"
	"testing"
)

const quotaFixture = `
def run()
  items = []
  for i in 1..200
    items = items.push("abcdefghij")
  end
  items.size
end
`

func TestMemoryQuotaExceeded(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	})

	script, err := engine.Compile(quotaFixture)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected memory quota error")
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryQuotaAllowsExecution(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 1 << 20,
	})

	script, err := engine.Compile(quotaFixture)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 200 {
		t.Fatalf("unexpected result: %#v", result)
	}
}
