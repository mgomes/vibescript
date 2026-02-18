package vibes

import (
	"context"
	"strings"
	"testing"
)

func TestContextCapabilityResolver(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  ctx.user.id
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	type ctxKey string
	resolver := func(ctx context.Context) (Value, error) {
		userID, _ := ctx.Value(ctxKey("user_id")).(string)
		role, _ := ctx.Value(ctxKey("role")).(string)
		return NewObject(map[string]Value{
			"user": NewObject(map[string]Value{
				"id":   NewString(userID),
				"role": NewString(role),
			}),
		}), nil
	}

	ctx := context.WithValue(context.Background(), ctxKey("user_id"), "player-1")
	ctx = context.WithValue(ctx, ctxKey("role"), "coach")

	result, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewContextCapability("ctx", resolver)},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "player-1" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestContextCapabilityRejectsCallableValue(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  ctx.user.id
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	resolver := func(ctx context.Context) (Value, error) {
		return NewObject(map[string]Value{
			"user": NewObject(map[string]Value{
				"id": NewString("player-1"),
				"fn": NewBuiltin("ctx.user.fn", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					return NewNil(), nil
				}),
			}),
		}), nil
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewContextCapability("ctx", resolver)},
	})
	if err == nil {
		t.Fatalf("expected callable context data error")
	}
	if got := err.Error(); !strings.Contains(got, "ctx capability value must be data-only") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestContextCapabilityRejectsNonObjectValue(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  1
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	resolver := func(ctx context.Context) (Value, error) {
		return NewString("invalid"), nil
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewContextCapability("ctx", resolver)},
	})
	if err == nil {
		t.Fatalf("expected resolver shape error")
	}
	if got := err.Error(); !strings.Contains(got, "ctx capability resolver must return hash/object") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNewContextCapabilityRejectsInvalidArguments(t *testing.T) {
	resolver := func(context.Context) (Value, error) { return NewObject(map[string]Value{}), nil }

	if _, err := NewContextCapability("", resolver); err == nil || !strings.Contains(err.Error(), "name must be non-empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}
	if _, err := NewContextCapability("ctx", nil); err == nil || !strings.Contains(err.Error(), "requires a resolver") {
		t.Fatalf("expected nil resolver error, got %v", err)
	}
}
