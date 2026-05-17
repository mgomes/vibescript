package contextcap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestNewCapabilityRejectsInvalidArguments(t *testing.T) {
	resolver := func(context.Context) (value.Value, error) {
		return value.NewObject(map[string]value.Value{}), nil
	}

	if _, err := NewCapability("", resolver); err == nil || !strings.Contains(err.Error(), "name must be non-empty") {
		t.Fatalf("expected name error, got %v", err)
	}
	if _, err := NewCapability("ctx", nil); err == nil || !strings.Contains(err.Error(), "requires a resolver") {
		t.Fatalf("expected resolver error, got %v", err)
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

func TestCapabilityBindReturnsClonedHash(t *testing.T) {
	source := map[string]value.Value{
		"user": value.NewObject(map[string]value.Value{
			"id": value.NewString("player-1"),
		}),
	}
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		return value.NewObject(source), nil
	})

	bound, err := cap.Bind(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := bound["ctx"]
	if !ok {
		t.Fatalf("missing ctx binding: %#v", bound)
	}
	if got.Kind() != value.KindObject {
		t.Fatalf("expected object, got kind %d", got.Kind())
	}

	source["user"] = value.NewString("mutated")
	user := got.Hash()["user"]
	if user.Kind() != value.KindObject {
		t.Fatalf("clone should be independent of resolver state, got kind %d", user.Kind())
	}
}

func TestCapabilityBindRejectsNonObjectValue(t *testing.T) {
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		return value.NewString("invalid"), nil
	})
	_, err := cap.Bind(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ctx capability resolver must return hash/object") {
		t.Fatalf("expected hash/object error, got %v", err)
	}
}

func TestCapabilityBindRejectsCyclicValue(t *testing.T) {
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		cyclic := map[string]value.Value{}
		cyclic["self"] = value.NewHash(cyclic)
		return value.NewHash(cyclic), nil
	})
	_, err := cap.Bind(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ctx capability value must not contain cyclic references") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCapabilityBindPropagatesResolverError(t *testing.T) {
	sentinel := errors.New("resolver failed")
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		return value.Value{}, sentinel
	})
	_, err := cap.Bind(context.Background())
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve ctx capability") {
		t.Fatalf("expected wrapper prefix, got %v", err)
	}
}

func TestCapabilityName(t *testing.T) {
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		return value.NewObject(map[string]value.Value{}), nil
	})
	if cap.Name() != "ctx" {
		t.Fatalf("expected name ctx, got %q", cap.Name())
	}
}
