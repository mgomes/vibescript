package vibes

import (
	"context"
	"strings"
	"testing"
)

func TestCapabilityFoundationsMixedAdapters(t *testing.T) {
	db := &dbCapabilityStub{
		findResult: NewHash(map[string]Value{
			"id":     NewString("player-1"),
			"raised": NewInt(125),
		}),
	}
	events := &eventsCapabilityStub{publishResult: NewNil()}
	jobs := &jobQueueStub{}

	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run(player_id)
  player = db.find("Player", player_id)
  events.publish("player_seen", { id: player[:id], actor: ctx.user.id })
  jobs.enqueue("audit_player", { id: player[:id], raised: player[:raised] })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	ctxCap := MustNewContextCapability("ctx", func(context.Context) (Value, error) {
		return NewObject(map[string]Value{
			"user": NewObject(map[string]Value{
				"id": NewString("coach-9"),
			}),
		}), nil
	})

	result, err := script.Call(context.Background(), "run", []Value{NewString("player-1")}, CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", db),
			MustNewEventsCapability("events", events),
			ctxCap,
			MustNewJobQueueCapability("jobs", jobs),
		},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
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

func TestCapabilityFoundationsEachRespectsStepQuota(t *testing.T) {
	rows := make([]Value, 120)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{"amount": NewInt(1)})
	}
	db := &dbCapabilityStub{eachRows: rows}

	engine := MustNewEngine(Config{StepQuota: 50})
	script, err := engine.Compile(`def run()
  total = 0
  db.each("ScoreEntry") do |row|
    total = total + row[:amount]
  end
  total
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", db),
		},
	})
	if err == nil {
		t.Fatalf("expected step quota error")
	}
	if got := err.Error(); !strings.Contains(got, "step quota exceeded") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestCapabilityFoundationsEachNoopBlockRespectsStepQuota(t *testing.T) {
	rows := make([]Value, 120)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{"amount": NewInt(1)})
	}
	db := &dbCapabilityStub{eachRows: rows}

	engine := MustNewEngine(Config{StepQuota: 20})
	script, err := engine.Compile(`def run()
  db.each("ScoreEntry") do |row|
  end
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", db),
		},
	})
	if err == nil {
		t.Fatalf("expected step quota error")
	}
	if got := err.Error(); !strings.Contains(got, "step quota exceeded") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestCapabilityFoundationsEachRespectsRecursionLimit(t *testing.T) {
	db := &dbCapabilityStub{
		eachRows: []Value{
			NewHash(map[string]Value{"id": NewString("row-1")}),
		},
	}

	engine := MustNewEngine(Config{RecursionLimit: 5, StepQuota: 10_000})
	script, err := engine.Compile(`def recurse(n)
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
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			MustNewDBCapability("db", db),
		},
	})
	if err == nil {
		t.Fatalf("expected recursion limit error")
	}
	if got := err.Error(); !strings.Contains(got, "recursion depth exceeded") {
		t.Fatalf("unexpected error: %s", got)
	}
}
