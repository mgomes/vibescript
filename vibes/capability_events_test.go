package vibes

import (
	"context"
	"strings"
	"testing"
)

type eventsCapabilityStub struct {
	publishCalls  []EventPublishRequest
	publishCtx    []context.Context
	publishResult Value
	publishErr    error
}

func (s *eventsCapabilityStub) Publish(ctx context.Context, req EventPublishRequest) (Value, error) {
	s.publishCalls = append(s.publishCalls, req)
	s.publishCtx = append(s.publishCtx, ctx)
	if s.publishErr != nil {
		return NewNil(), s.publishErr
	}
	return s.publishResult, nil
}

func TestEventsCapabilityPublish(t *testing.T) {
	stub := &eventsCapabilityStub{publishResult: NewBool(true)}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  events.publish("players_totals", { id: "p-1", total: "55.00 USD" }, trace: "abc")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("request_id"), "req-1")
	result, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewEventsCapability("events", stub)},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindBool || !result.Bool() {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(stub.publishCalls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(stub.publishCalls))
	}
	call := stub.publishCalls[0]
	if call.Topic != "players_totals" {
		t.Fatalf("unexpected topic: %s", call.Topic)
	}
	if call.Payload["id"].String() != "p-1" || call.Payload["total"].String() != "55.00 USD" {
		t.Fatalf("unexpected payload: %#v", call.Payload)
	}
	if call.Options["trace"].String() != "abc" {
		t.Fatalf("unexpected options: %#v", call.Options)
	}
	if len(stub.publishCtx) != 1 || stub.publishCtx[0].Value(ctxKey("request_id")) != "req-1" {
		t.Fatalf("context value not propagated")
	}
}

func TestEventsCapabilityRejectsCallablePayload(t *testing.T) {
	stub := &eventsCapabilityStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def helper(value)
  value
end

def run()
  events.publish("topic", { callback: helper })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewEventsCapability("events", stub)},
	})
	if err == nil {
		t.Fatalf("expected callable payload error")
	}
	if got := err.Error(); !strings.Contains(got, "events.publish payload must be data-only") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestEventsCapabilityRejectsCallableReturn(t *testing.T) {
	stub := &eventsCapabilityStub{
		publishResult: NewObject(map[string]Value{
			"fn": NewBuiltin("leak.fn", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewNil(), nil
			}),
		}),
	}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  events.publish("topic", { id: "p-1" })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewEventsCapability("events", stub)},
	})
	if err == nil {
		t.Fatalf("expected return contract error")
	}
	if got := err.Error(); !strings.Contains(got, "events.publish return value must be data-only") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNewEventsCapabilityRejectsInvalidArguments(t *testing.T) {
	stub := &eventsCapabilityStub{}
	if _, err := NewEventsCapability("", stub); err == nil || !strings.Contains(err.Error(), "name must be non-empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}

	var publisher EventPublisher
	if _, err := NewEventsCapability("events", publisher); err == nil || !strings.Contains(err.Error(), "requires a non-nil implementation") {
		t.Fatalf("expected nil publisher error, got %v", err)
	}
}
