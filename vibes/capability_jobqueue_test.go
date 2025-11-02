package vibes

import (
	"context"
	"strings"
	"testing"
	"time"
)

type jobQueueStub struct {
	enqueueCalls []JobQueueJob
	enqueueCtx   []context.Context
	retryCalls   []JobQueueRetryRequest
	retryCtx     []context.Context
}

func (s *jobQueueStub) Enqueue(ctx context.Context, job JobQueueJob) (Value, error) {
	s.enqueueCalls = append(s.enqueueCalls, job)
	s.enqueueCtx = append(s.enqueueCtx, ctx)
	return NewString("queued"), nil
}

func (s *jobQueueStub) Retry(ctx context.Context, req JobQueueRetryRequest) (Value, error) {
	s.retryCalls = append(s.retryCalls, req)
	s.retryCtx = append(s.retryCtx, ctx)
	return NewBool(true), nil
}

func TestJobQueueCapabilityEnqueue(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, delay: 2.seconds, key: "abc", queue: "standard")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("trace"), "on")

	result, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{NewJobQueueCapability("jobs", stub)},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "queued" {
		t.Fatalf("unexpected enqueue result: %#v", result)
	}

	if len(stub.enqueueCalls) != 1 {
		t.Fatalf("expected 1 enqueue call, got %d", len(stub.enqueueCalls))
	}
	call := stub.enqueueCalls[0]
	if call.Name != "demo" {
		t.Fatalf("expected job name demo, got %s", call.Name)
	}

	payload := call.Payload
	if payload == nil {
		t.Fatalf("payload was nil")
	}
	if v, ok := payload["foo"]; !ok || v.Kind() != KindString || v.String() != "bar" {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	if call.Options.Delay == nil || *call.Options.Delay != 2*time.Second {
		t.Fatalf("expected delay 2s, got %+v", call.Options.Delay)
	}
	if call.Options.Key == nil || *call.Options.Key != "abc" {
		t.Fatalf("expected key abc, got %+v", call.Options.Key)
	}
	if call.Options.Kwargs == nil {
		t.Fatalf("expected kwargs map")
	}
	if v, ok := call.Options.Kwargs["queue"]; !ok || v.String() != "standard" {
		t.Fatalf("expected queue kwarg preserved, got %+v", call.Options.Kwargs)
	}

	if len(stub.enqueueCtx) != 1 {
		t.Fatalf("expected context capture")
	}
	if stub.enqueueCtx[0].Value(ctxKey("trace")) != "on" {
		t.Fatalf("context value not propagated")
	}
}

func TestJobQueueCapabilityRetry(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.retry("job-7", attempts: 3, priority: "high")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{NewJobQueueCapability("jobs", stub)},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindBool || !result.Bool() {
		t.Fatalf("unexpected retry result: %#v", result)
	}

	if len(stub.retryCalls) != 1 {
		t.Fatalf("expected 1 retry call, got %d", len(stub.retryCalls))
	}
	call := stub.retryCalls[0]
	if call.JobID != "job-7" {
		t.Fatalf("unexpected job id %s", call.JobID)
	}
	if len(call.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(call.Options))
	}
	if v, ok := call.Options["attempts"]; !ok || v.Int() != 3 {
		t.Fatalf("missing attempts option: %+v", call.Options)
	}
	if v, ok := call.Options["priority"]; !ok || v.String() != "high" {
		t.Fatalf("missing priority option: %+v", call.Options)
	}
}

func TestJobQueueCapabilityRejectsInvalidPayload(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", 42)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{NewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for invalid payload")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue expects payload hash") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNilCapabilityAdapterFiltering(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("test", { foo: "bar" })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{nil, NewJobQueueCapability("jobs", stub), nil},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "queued" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(stub.enqueueCalls) != 1 {
		t.Fatalf("expected 1 enqueue call, got %d", len(stub.enqueueCalls))
	}
}

func TestJobQueueRejectsNegativeDelay(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, delay: -5)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{NewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for negative delay")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue delay must be non-negative") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueRejectsEmptyKey(t *testing.T) {
	stub := &jobQueueStub{}
	engine := NewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, key: "")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{NewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for empty key")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue key must be non-empty") {
		t.Fatalf("unexpected error: %s", got)
	}
}
