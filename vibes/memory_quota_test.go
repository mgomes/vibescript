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

const splitFixture = `
def run(input)
  input.split(",")
end
`

const classVarFixture = `
class Bucket
  @@items = {}

  def self.fill(count)
    for i in 1..count
      key = "k" + i
      @@items[key] = i
    end
    @@items["k1"]
  end
end

def run
  Bucket.fill(200)
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

func TestMemoryQuotaCountsClassVars(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 3072,
	})

	script, err := engine.Compile(classVarFixture)
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

func TestMemoryQuotaExceededOnCompletion(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	})

	script, err := engine.Compile(splitFixture)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	input := strings.Repeat("a,", 4000)
	_, err = script.Call(context.Background(), "run", []Value{NewString(input)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected memory quota error")
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryQuotaExceededForEmptyBodyDefaultArg(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	})

	largeCSV := strings.Repeat("abcdefghij,", 1500)
	source := `def run(payload = "` + largeCSV + `".split(","))
end`

	script, err := engine.Compile(source)
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

func TestMemoryQuotaExceededForBoundArguments(t *testing.T) {
	engine := NewEngine(Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	})

	script, err := engine.Compile(`def run(payload)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	parts := make([]Value, 2000)
	for i := range parts {
		parts[i] = NewString("abcdefghij")
	}
	largeArg := NewArray(parts)

	_, err = script.Call(context.Background(), "run", []Value{largeArg}, CallOptions{})
	if err == nil {
		t.Fatalf("expected memory quota error for positional arg")
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("unexpected positional arg error: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Keywords: map[string]Value{
			"payload": largeArg,
		},
	})
	if err == nil {
		t.Fatalf("expected memory quota error for keyword arg")
	}
	if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("unexpected keyword arg error: %v", err)
	}
}
