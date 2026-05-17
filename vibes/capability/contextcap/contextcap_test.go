package contextcap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestNewCapabilityRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	resolver := func(context.Context) (value.Value, error) {
		return value.NewObject(map[string]value.Value{}), nil
	}

	tests := []struct {
		name     string
		capName  string
		resolver Resolver
		wantErr  string
	}{
		{name: "empty_name", capName: "", resolver: resolver, wantErr: "name must be non-empty"},
		{name: "nil_resolver", capName: "ctx", resolver: nil, wantErr: "requires a resolver"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCapability(tc.capName, tc.resolver)
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

func TestCapabilityBindReturnsClonedHash(t *testing.T) {
	t.Parallel()
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

func TestCapabilityBindRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	cyclicResolver := func(context.Context) (value.Value, error) {
		cyclic := map[string]value.Value{}
		cyclic["self"] = value.NewHash(cyclic)
		return value.NewHash(cyclic), nil
	}

	tests := []struct {
		name     string
		resolver Resolver
		wantErr  string
	}{
		{
			name: "non_object_value",
			resolver: func(context.Context) (value.Value, error) {
				return value.NewString("invalid"), nil
			},
			wantErr: "ctx capability resolver must return hash/object",
		},
		{
			name:     "cyclic_value",
			resolver: cyclicResolver,
			wantErr:  "ctx capability value must not contain cyclic references",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cap := MustNewCapability("ctx", tc.resolver)
			_, err := cap.Bind(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCapabilityBindPropagatesResolverError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	cap := MustNewCapability("ctx", func(context.Context) (value.Value, error) {
		return value.NewObject(map[string]value.Value{}), nil
	})
	if cap.Name() != "ctx" {
		t.Fatalf("expected name ctx, got %q", cap.Name())
	}
}
