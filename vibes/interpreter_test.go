package vibes

import (
	"context"
	"testing"
)

func TestCompileAndCallAdd(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def add(a, b)
  a + b
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "add", []Value{NewInt(2), NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 5 {
		t.Fatalf("expected 5, got %#v", result)
	}
}

func TestMoneyBuiltin(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def total
  money("10.00 USD") + money("5.00 USD")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "total", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindMoney {
		t.Fatalf("expected money value, got %v", result.Kind())
	}
	money := result.Money()
	if money.Cents() != 1500 || money.Currency() != "USD" {
		t.Fatalf("unexpected money result: %s", money.String())
	}
}

func TestGlobalsAccess(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def user_id
  ctx.user.id
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	ctxVal := NewObject(map[string]Value{
		"user": NewObject(map[string]Value{
			"id": NewString("coach-1"),
		}),
	})

	result, err := script.Call(context.Background(), "user_id", nil, CallOptions{Globals: map[string]Value{"ctx": ctxVal}})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "coach-1" {
		t.Fatalf("unexpected result: %v", result.String())
	}
}

func TestAssertFailure(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def check
  assert false, "boom"
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "check", nil, CallOptions{}); err == nil {
		t.Fatalf("expected assertion error")
	}
}

func TestSymbolIndex(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def amount(row)
  row[:amount]
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	row := NewHash(map[string]Value{"amount": NewInt(42)})
	result, err := script.Call(context.Background(), "amount", []Value{row}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 42 {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestDurationLiteral(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def seconds
  (2.minutes).seconds
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "seconds", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 120 {
		t.Fatalf("expected 120 seconds, got %v", result)
	}
}

func TestZeroArgCallWithoutParens(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def helper
  7
end

def run
  helper * 6
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 42 {
		t.Fatalf("expected 42, got %v", result)
	}
}
