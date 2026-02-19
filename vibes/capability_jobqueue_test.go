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

type invalidReturnQueue struct{}
type mutatingInputQueue struct{}

type sharedReturnQueue struct {
	enqueueResult Value
	retryResult   Value
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

func (invalidReturnQueue) Enqueue(ctx context.Context, job JobQueueJob) (Value, error) {
	return NewObject(map[string]Value{
		"save": NewBuiltin("leak.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewString("ok"), nil
		}),
	}), nil
}

func (mutatingInputQueue) Enqueue(ctx context.Context, job JobQueueJob) (Value, error) {
	job.Payload["foo"] = NewString("host-payload")
	meta, ok := job.Options.Kwargs["meta"]
	if ok && (meta.Kind() == KindHash || meta.Kind() == KindObject) {
		meta.Hash()["trace"] = NewString("host-meta")
	}
	return NewString("queued"), nil
}

func (mutatingInputQueue) Retry(ctx context.Context, req JobQueueRetryRequest) (Value, error) {
	attempt, ok := req.Options["attempt"]
	if ok && (attempt.Kind() == KindHash || attempt.Kind() == KindObject) {
		attempt.Hash()["value"] = NewString("host-attempt")
	}
	kw, ok := req.Options["kw"]
	if ok && (kw.Kind() == KindHash || kw.Kind() == KindObject) {
		kw.Hash()["value"] = NewString("host-kw")
	}
	return NewBool(true), nil
}

func (s *sharedReturnQueue) Enqueue(ctx context.Context, job JobQueueJob) (Value, error) {
	return s.enqueueResult, nil
}

func (s *sharedReturnQueue) Retry(ctx context.Context, req JobQueueRetryRequest) (Value, error) {
	return s.retryResult, nil
}

func TestJobQueueCapabilityEnqueue(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, delay: 2.seconds, key: "abc", queue: "standard")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("trace"), "on")

	result, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
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
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.retry("job-7", attempts: 3, priority: "high")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
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

func TestJobQueueCapabilityEnqueueOptionsAreClonedFromScriptState(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  payload = { foo: "script-payload" }
  meta = { trace: "script-meta" }
  jobs.enqueue("demo", payload, meta: meta)
  { payload: payload[:foo], trace: meta[:trace] }
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", mutatingInputQueue{})},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	hash := result.Hash()
	if hash["payload"].Kind() != KindString || hash["payload"].String() != "script-payload" {
		t.Fatalf("script payload was mutated by host: %#v", result)
	}
	if hash["trace"].Kind() != KindString || hash["trace"].String() != "script-meta" {
		t.Fatalf("script kwargs value was mutated by host: %#v", result)
	}
}

func TestJobQueueCapabilityRetryOptionsAreClonedFromScriptState(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  arg = { attempt: { value: "script-attempt" } }
  kw = { value: "script-kw" }
  jobs.retry("job-1", arg, kw: kw)
  { attempt: arg[:attempt][:value], kw: kw[:value] }
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", mutatingInputQueue{})},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	hash := result.Hash()
	if hash["attempt"].Kind() != KindString || hash["attempt"].String() != "script-attempt" {
		t.Fatalf("script retry arg was mutated by host: %#v", result)
	}
	if hash["kw"].Kind() != KindString || hash["kw"].String() != "script-kw" {
		t.Fatalf("script retry kwargs were mutated by host: %#v", result)
	}
}

func TestJobQueueCapabilityReturnsAreClonedFromHostState(t *testing.T) {
	stub := &sharedReturnQueue{
		enqueueResult: NewHash(map[string]Value{
			"meta": NewHash(map[string]Value{
				"status": NewString("host-enqueue"),
			}),
		}),
		retryResult: NewHash(map[string]Value{
			"meta": NewHash(map[string]Value{
				"status": NewString("host-retry"),
			}),
		}),
	}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  queued = jobs.enqueue("demo", { foo: "bar" })
  queued[:meta][:status] = "script-enqueue"

  retried = jobs.retry("job-1")
  retried[:meta][:status] = "script-retry"
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	}); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	enqueueStatus := stub.enqueueResult.Hash()["meta"].Hash()["status"]
	if enqueueStatus.Kind() != KindString || enqueueStatus.String() != "host-enqueue" {
		t.Fatalf("enqueue host result mutated by script: %#v", stub.enqueueResult)
	}

	retryStatus := stub.retryResult.Hash()["meta"].Hash()["status"]
	if retryStatus.Kind() != KindString || retryStatus.String() != "host-retry" {
		t.Fatalf("retry host result mutated by script: %#v", stub.retryResult)
	}
}

func TestJobQueueCapabilityRejectsInvalidPayload(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", 42)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for invalid payload")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue expects payload hash") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueCapabilityRejectsCallablePayload(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def helper(value)
  value
end

def run()
  jobs.enqueue("demo", { callback: helper })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected callable payload contract error")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue payload must be data-only") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNilCapabilityAdapterFiltering(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("test", { foo: "bar" })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{nil, MustNewJobQueueCapability("jobs", stub), nil},
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
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, delay: -5)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for negative delay")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue delay must be non-negative") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueRejectsUnexpectedEnqueuePositionalArgs(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, { extra: true })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected positional arg error")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue expects job name and payload") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueRejectsEmptyKey(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" }, key: "")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected error for empty key")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue key must be non-empty") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueRejectsUnexpectedRetryPositionalArgs(t *testing.T) {
	stub := &jobQueueStub{}
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.retry("job-7", { attempts: 1 }, { force: true })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", stub)},
	})
	if err == nil {
		t.Fatalf("expected retry positional arg error")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.retry expects job id and optional options hash") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestJobQueueRejectsCallableReturnValue(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("demo", { foo: "bar" })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{MustNewJobQueueCapability("jobs", invalidReturnQueue{})},
	})
	if err == nil {
		t.Fatalf("expected return contract error")
	}
	if got := err.Error(); !strings.Contains(got, "jobs.enqueue return value must be data-only") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNewJobQueueCapabilityRejectsEmptyName(t *testing.T) {
	stub := &jobQueueStub{}
	_, err := NewJobQueueCapability("", stub)
	if err == nil {
		t.Fatalf("expected empty capability name to fail")
	}
	if got := err.Error(); !strings.Contains(got, "name must be non-empty") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNewJobQueueCapabilityRejectsNilQueue(t *testing.T) {
	var queue JobQueue
	_, err := NewJobQueueCapability("jobs", queue)
	if err == nil {
		t.Fatalf("expected nil queue to fail")
	}
	if got := err.Error(); !strings.Contains(got, "requires a non-nil implementation") {
		t.Fatalf("unexpected error: %s", got)
	}

	var typedNil *jobQueueStub
	_, err = NewJobQueueCapability("jobs", typedNil)
	if err == nil {
		t.Fatalf("expected typed nil queue to fail")
	}
	if got := err.Error(); !strings.Contains(got, "requires a non-nil implementation") {
		t.Fatalf("unexpected typed nil error: %s", got)
	}
}
