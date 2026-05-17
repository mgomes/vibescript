package events

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

type stubPublisher struct {
	calls   []PublishRequest
	ctxs    []context.Context
	result  value.Value
	failure error
}

var _ Publisher = (*stubPublisher)(nil)

func (s *stubPublisher) Publish(ctx context.Context, req PublishRequest) (value.Value, error) {
	s.calls = append(s.calls, req)
	s.ctxs = append(s.ctxs, ctx)
	if s.failure != nil {
		return value.NewNil(), s.failure
	}
	return s.result, nil
}

func TestNewCapabilityRejectsInvalidArguments(t *testing.T) {
	stub := &stubPublisher{}

	if _, err := NewCapability("", stub); err == nil || !strings.Contains(err.Error(), "name must be non-empty") {
		t.Fatalf("expected name error, got %v", err)
	}

	var publisher Publisher
	if _, err := NewCapability("events", publisher); err == nil || !strings.Contains(err.Error(), "requires a non-nil implementation") {
		t.Fatalf("expected nil interface error, got %v", err)
	}

	var typedNil *stubPublisher
	if _, err := NewCapability("events", typedNil); err == nil || !strings.Contains(err.Error(), "requires a non-nil implementation") {
		t.Fatalf("expected typed-nil error, got %v", err)
	}
}

func TestMustNewCapabilityPanicsOnInvalidArguments(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on invalid arguments")
		}
	}()
	_ = MustNewCapability("", nil)
}

func TestCapabilityPublishCallsHostAndClonesResult(t *testing.T) {
	stub := &stubPublisher{
		result: value.NewHash(map[string]value.Value{
			"meta": value.NewHash(map[string]value.Value{
				"trace": value.NewString("host"),
			}),
		}),
	}
	cap := MustNewCapability("events", stub)

	args := []value.Value{
		value.NewString("topic"),
		value.NewHash(map[string]value.Value{"id": value.NewString("p-1")}),
	}
	result, err := cap.Publish(context.Background(), args, map[string]value.Value{"trace": value.NewString("abc")}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Hash()["meta"].Hash()["trace"].String(); got != "host" {
		t.Fatalf("unexpected return: %s", got)
	}

	// Mutating the cloned return must not affect the host-side state.
	result.Hash()["meta"].Hash()["trace"] = value.NewString("mutated")
	if stub.result.Hash()["meta"].Hash()["trace"].String() != "host" {
		t.Fatalf("clone leaked host state")
	}

	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(stub.calls))
	}
	if stub.calls[0].Topic != "topic" {
		t.Fatalf("unexpected topic: %s", stub.calls[0].Topic)
	}
	if stub.calls[0].Payload["id"].String() != "p-1" {
		t.Fatalf("unexpected payload: %#v", stub.calls[0].Payload)
	}
	if stub.calls[0].Options["trace"].String() != "abc" {
		t.Fatalf("unexpected options: %#v", stub.calls[0].Options)
	}
}

func TestCapabilityPublishRejectsBadArgs(t *testing.T) {
	stub := &stubPublisher{result: value.NewNil()}
	cap := MustNewCapability("events", stub)

	if err := cap.ValidatePublishArgs(nil, nil, false); err == nil || !strings.Contains(err.Error(), "expects topic and payload") {
		t.Fatalf("expected arity error, got %v", err)
	}

	if err := cap.ValidatePublishArgs([]value.Value{value.NewString("topic"), value.NewInt(42)}, nil, false); err == nil || !strings.Contains(err.Error(), "expected hash, got int") {
		t.Fatalf("expected hash type error, got %v", err)
	}

	if err := cap.ValidatePublishArgs([]value.Value{value.NewString("topic"), value.NewHash(nil)}, nil, true); err == nil || !strings.Contains(err.Error(), "does not accept blocks") {
		t.Fatalf("expected block rejection, got %v", err)
	}

	if err := cap.ValidatePublishArgs([]value.Value{value.NewString(""), value.NewHash(nil)}, nil, false); err == nil || !strings.Contains(err.Error(), "non-empty string or symbol") {
		t.Fatalf("expected empty topic error, got %v", err)
	}
}

func TestCapabilityPublishPropagatesHostError(t *testing.T) {
	boom := errors.New("boom")
	stub := &stubPublisher{failure: boom}
	cap := MustNewCapability("events", stub)

	args := []value.Value{value.NewString("topic"), value.NewHash(nil)}
	_, err := cap.Publish(context.Background(), args, nil, false)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped host error, got %v", err)
	}
}

func TestCapabilityPublishRejectsCyclicPayload(t *testing.T) {
	stub := &stubPublisher{result: value.NewNil()}
	cap := MustNewCapability("events", stub)

	cyclic := map[string]value.Value{}
	cyclic["self"] = value.NewHash(cyclic)
	args := []value.Value{value.NewString("topic"), value.NewHash(cyclic)}

	_, err := cap.Publish(context.Background(), args, nil, false)
	if err == nil || !strings.Contains(err.Error(), "must not contain cyclic references") {
		t.Fatalf("expected cycle rejection, got %v", err)
	}
}
