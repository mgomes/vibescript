package runtime

import (
	"context"
	"errors"
	"testing"
)

type cancelingCapabilityAdapter struct {
	cancel context.CancelFunc
}

func (adapter cancelingCapabilityAdapter) Bind(CapabilityBinding) (map[string]Value, error) {
	adapter.cancel()
	return map[string]Value{
		"cap": NewString("bound after cancel"),
	}, nil
}

func TestCapabilityFoundationsMixedAdapters(t *testing.T) {
	t.Parallel()
	db := &dbCapabilityStub{
		findResult: NewHash(map[string]Value{
			"id":     NewString("player-1"),
			"raised": NewInt(125),
		}),
	}
	events := &eventsCapabilityStub{publishResult: NewNil()}
	jobs := &jobQueueStub{}

	script := compileScriptDefault(t, `def run(player_id)
  player = db.find("Player", player_id)
  events.publish("player_seen", { id: player[:id], actor: ctx.user.id })
  jobs.enqueue("audit_player", { id: player[:id], raised: player[:raised] })
end`)

	ctxCap := MustNewContextCapability("ctx", func(context.Context) (Value, error) {
		return NewObject(map[string]Value{
			"user": NewObject(map[string]Value{
				"id": NewString("coach-9"),
			}),
		}), nil
	})

	result := callScript(t, context.Background(), script, "run", []Value{NewString("player-1")}, callOptionsWithCapabilities(
		MustNewDBCapability("db", db),
		MustNewEventsCapability("events", events),
		ctxCap,
		MustNewJobQueueCapability("jobs", jobs),
	))
	if result.Kind() != KindString || result.String() != "queued" {
		t.Fatalf("unexpected result: %#v", result)
	}

	if len(db.findCalls) != 1 {
		t.Fatalf("expected db.find once, got %d", len(db.findCalls))
	}
	if len(events.publishCalls) != 1 {
		t.Fatalf("expected events.publish once, got %d", len(events.publishCalls))
	}
	eventPayload := events.publishCalls[0].Payload
	if eventPayload["actor"].String() != "coach-9" {
		t.Fatalf("unexpected actor in payload: %#v", eventPayload)
	}
	if len(jobs.enqueueCalls) != 1 {
		t.Fatalf("expected jobs.enqueue once, got %d", len(jobs.enqueueCalls))
	}
	if jobs.enqueueCalls[0].Payload["id"].String() != "player-1" {
		t.Fatalf("unexpected enqueue payload: %#v", jobs.enqueueCalls[0].Payload)
	}
}

func TestCapabilityBindHonorsCancellationBeforeRebindingGlobals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  cap
end
`)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := script.Call(ctx, "run", nil, CallOptions{Capabilities: []CapabilityAdapter{
		cancelingCapabilityAdapter{cancel: cancel},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call with capability canceled during Bind = %v, want context.Canceled", err)
	}
}

func TestCapabilityFoundationsEachRespectsStepQuota(t *testing.T) {
	t.Parallel()
	rows := make([]Value, 120)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{"amount": NewInt(1)})
	}
	db := &dbCapabilityStub{eachRows: rows}

	script := compileScriptWithConfig(t, Config{StepQuota: 50}, `def run()
  total = 0
  db.each("ScoreEntry") do |row|
    total = total + row[:amount]
  end
  total
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", db),
	))
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
}

func TestCapabilityFoundationsEachNoopBlockRespectsStepQuota(t *testing.T) {
	t.Parallel()
	rows := make([]Value, 120)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{"amount": NewInt(1)})
	}
	db := &dbCapabilityStub{eachRows: rows}

	script := compileScriptWithConfig(t, Config{StepQuota: 20}, `def run()
  db.each("ScoreEntry") do |row|
  end
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", db),
	))
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
}

func TestCapabilityFoundationsEachRespectsRecursionLimit(t *testing.T) {
	t.Parallel()
	db := &dbCapabilityStub{
		eachRows: []Value{
			NewHash(map[string]Value{"id": NewString("row-1")}),
		},
	}

	script := compileScriptWithConfig(t, Config{RecursionLimit: 5, StepQuota: 10_000}, `def recurse(n)
  if n <= 0
    0
  else
    recurse(n - 1)
  end
end

def run()
  db.each("ScoreEntry") do |row|
    recurse(20)
  end
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", db),
	))
	requireErrorContains(t, err, "recursion depth exceeded")
}
