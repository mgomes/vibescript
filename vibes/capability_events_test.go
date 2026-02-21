package vibes

import (
	"context"
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
	script := compileScriptDefault(t, `def run()
  events.publish("players_totals", { id: "p-1", total: "55.00 USD" }, trace: "abc")
end`)

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("request_id"), "req-1")
	result := callScript(t, ctx, script, "run", nil, callOptionsWithCapabilities(
		MustNewEventsCapability("events", stub),
	))
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
	script := compileScriptDefault(t, `def helper(value)
  value
end

def run()
  events.publish("topic", { callback: helper })
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewEventsCapability("events", stub),
	))
	requireErrorContains(t, err, "events.publish payload must be data-only")
}

func TestEventsCapabilityRejectsNonHashPayload(t *testing.T) {
	stub := &eventsCapabilityStub{}
	script := compileScriptDefault(t, `def run()
  events.publish("topic", 42)
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewEventsCapability("events", stub),
	))
	requireErrorContains(t, err, "events.publish payload expected hash, got int")
}

func TestEventsCapabilityRejectsCallableReturn(t *testing.T) {
	stub := &eventsCapabilityStub{
		publishResult: NewObject(map[string]Value{
			"fn": NewBuiltin("leak.fn", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewNil(), nil
			}),
		}),
	}
	script := compileScriptDefault(t, `def run()
  events.publish("topic", { id: "p-1" })
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewEventsCapability("events", stub),
	))
	requireErrorContains(t, err, "events.publish return value must be data-only")
}

func TestEventsCapabilityReturnsAreClonedFromHostState(t *testing.T) {
	stub := &eventsCapabilityStub{
		publishResult: NewHash(map[string]Value{
			"meta": NewHash(map[string]Value{
				"trace": NewString("host"),
			}),
		}),
	}
	script := compileScriptDefault(t, `def run()
  event = events.publish("topic", { id: "p-1" })
  event[:meta][:trace] = "script"
end`)

	callScript(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewEventsCapability("events", stub),
	))

	trace := stub.publishResult.Hash()["meta"].Hash()["trace"]
	if trace.Kind() != KindString || trace.String() != "host" {
		t.Fatalf("publish host result mutated by script: %#v", stub.publishResult)
	}
}

func TestNewEventsCapabilityRejectsInvalidArguments(t *testing.T) {
	stub := &eventsCapabilityStub{}
	_, err := NewEventsCapability("", stub)
	requireErrorContains(t, err, "name must be non-empty")

	var publisher EventPublisher
	_, err = NewEventsCapability("events", publisher)
	requireErrorContains(t, err, "requires a non-nil implementation")

	var typedNil *eventsCapabilityStub
	_, err = NewEventsCapability("events", typedNil)
	requireErrorContains(t, err, "requires a non-nil implementation")
}
