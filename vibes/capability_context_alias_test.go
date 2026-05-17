package vibes

import (
	"context"
	"testing"
)

func TestContextCapabilityResolver(t *testing.T) {
	script := compileScriptDefault(t, `def run()
  ctx.user.id
end`)

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

	result := callScript(t, ctx, script, "run", nil, callOptionsWithCapabilities(
		MustNewContextCapability("ctx", resolver),
	))
	if result.Kind() != KindString || result.String() != "player-1" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestContextCapabilityRejectsCallableValue(t *testing.T) {
	script := compileScriptDefault(t, `def run()
  ctx.user.id
end`)

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

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewContextCapability("ctx", resolver),
	))
	requireErrorContains(t, err, "ctx capability value must be data-only")
}

func TestContextCapabilityRejectsNonObjectValue(t *testing.T) {
	script := compileScriptDefault(t, `def run()
  1
end`)

	resolver := func(ctx context.Context) (Value, error) {
		return NewString("invalid"), nil
	}

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewContextCapability("ctx", resolver),
	))
	requireErrorContains(t, err, "ctx capability resolver must return hash/object")
}

func TestContextCapabilityRejectsCyclicValue(t *testing.T) {
	script := compileScriptDefault(t, `def run()
  ctx
end`)

	resolver := func(context.Context) (Value, error) {
		cyclic := map[string]Value{}
		cyclic["self"] = NewHash(cyclic)
		return NewHash(cyclic), nil
	}

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewContextCapability("ctx", resolver),
	))
	requireErrorContains(t, err, "ctx capability value must not contain cyclic references")
}

func TestNewContextCapabilityRejectsInvalidArguments(t *testing.T) {
	resolver := func(context.Context) (Value, error) { return NewObject(map[string]Value{}), nil }

	_, err := NewContextCapability("", resolver)
	requireErrorContains(t, err, "name must be non-empty")

	_, err = NewContextCapability("ctx", nil)
	requireErrorContains(t, err, "requires a resolver")
}
