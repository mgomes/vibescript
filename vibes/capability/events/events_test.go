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
	t.Parallel()

	stub := &stubPublisher{}
	var nilPublisher Publisher
	var typedNil *stubPublisher

	tests := []struct {
		name      string
		capName   string
		publisher Publisher
		wantErr   string
	}{
		{name: "empty_name", capName: "", publisher: stub, wantErr: "name must be non-empty"},
		{name: "nil_interface", capName: "events", publisher: nilPublisher, wantErr: "requires a non-nil implementation"},
		{name: "typed_nil", capName: "events", publisher: typedNil, wantErr: "requires a non-nil implementation"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCapability(tc.capName, tc.publisher)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestMustNewCapabilityPanicsOnInvalidArguments(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on invalid arguments")
		}
	}()
	_ = MustNewCapability("", nil)
}

func TestCapabilityPublishCallsHostAndClonesResult(t *testing.T) {
	t.Parallel()
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

func TestValidatePublishArgsRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []value.Value
		kwargs  map[string]value.Value
		block   bool
		wantErr string
	}{
		{
			name:    "no_args",
			args:    nil,
			wantErr: "expects topic and payload",
		},
		{
			name:    "payload_wrong_type",
			args:    []value.Value{value.NewString("topic"), value.NewInt(42)},
			wantErr: "expected hash, got int",
		},
		{
			name:    "block_provided",
			args:    []value.Value{value.NewString("topic"), value.NewHash(nil)},
			block:   true,
			wantErr: "does not accept blocks",
		},
		{
			name:    "empty_topic",
			args:    []value.Value{value.NewString(""), value.NewHash(nil)},
			wantErr: "non-empty string or symbol",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cap := MustNewCapability("events", &stubPublisher{result: value.NewNil()})
			err := cap.ValidatePublishArgs(tc.args, tc.kwargs, tc.block)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCapabilityPublishPropagatesHostError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
