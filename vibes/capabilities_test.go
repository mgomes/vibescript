package vibes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/db"
	"github.com/mgomes/vibescript/vibes/capability/events"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
	"github.com/mgomes/vibescript/vibes/value"
)

type stubDatabase struct{}

func (stubDatabase) Find(context.Context, db.DBFindRequest) (value.Value, error) {
	return value.NewNil(), nil
}

func (stubDatabase) Query(context.Context, db.DBQueryRequest) (value.Value, error) {
	return value.NewNil(), nil
}

func (stubDatabase) Sum(context.Context, db.DBSumRequest) (value.Value, error) {
	return value.NewNil(), nil
}

func (stubDatabase) Each(context.Context, db.DBEachRequest) ([]value.Value, error) {
	return nil, nil
}

func (stubDatabase) Update(context.Context, db.DBUpdateRequest) (value.Value, error) {
	return value.NewNil(), nil
}

type stubEventPublisher struct{}

func (stubEventPublisher) Publish(context.Context, events.PublishRequest) (value.Value, error) {
	return value.NewNil(), nil
}

type stubJobQueue struct{}

func (stubJobQueue) Enqueue(context.Context, jobqueue.JobQueueJob) (value.Value, error) {
	return value.NewNil(), nil
}

func stubContextResolver(context.Context) (value.Value, error) {
	return value.NewHash(map[string]value.Value{"tenant": value.NewString("acme")}), nil
}

func TestCapabilityConstructorValidation(t *testing.T) {
	t.Parallel()

	var typedNilDB *stubDatabase
	var typedNilPublisher *stubEventPublisher
	var typedNilQueue *stubJobQueue

	tests := []struct {
		name      string
		construct func() (vibes.CapabilityAdapter, error)
		wantErr   string
	}{
		{
			name:      "context_empty_name",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewContextCapability("", stubContextResolver) },
			wantErr:   "vibes: context capability name must be non-empty",
		},
		{
			name:      "context_nil_resolver",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewContextCapability("ctx", nil) },
			wantErr:   "vibes: context capability requires a resolver",
		},
		{
			name:      "db_empty_name",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewDBCapability("", stubDatabase{}) },
			wantErr:   "vibes: database capability name must be non-empty",
		},
		{
			name:      "db_nil_interface",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewDBCapability("db", nil) },
			wantErr:   "vibes: database capability requires a non-nil implementation",
		},
		{
			name:      "db_typed_nil",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewDBCapability("db", typedNilDB) },
			wantErr:   "vibes: database capability requires a non-nil implementation",
		},
		{
			name: "events_empty_name",
			construct: func() (vibes.CapabilityAdapter, error) {
				return vibes.NewEventsCapability("", stubEventPublisher{})
			},
			wantErr: "vibes: events capability name must be non-empty",
		},
		{
			name:      "events_nil_interface",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewEventsCapability("events", nil) },
			wantErr:   "vibes: events capability requires a non-nil implementation",
		},
		{
			name: "events_typed_nil",
			construct: func() (vibes.CapabilityAdapter, error) {
				return vibes.NewEventsCapability("events", typedNilPublisher)
			},
			wantErr: "vibes: events capability requires a non-nil implementation",
		},
		{
			name:      "jobqueue_empty_name",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewJobQueueCapability("", stubJobQueue{}) },
			wantErr:   "vibes: job queue capability name must be non-empty",
		},
		{
			name:      "jobqueue_nil_interface",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewJobQueueCapability("jobs", nil) },
			wantErr:   "vibes: job queue capability requires a non-nil implementation",
		},
		{
			name: "jobqueue_typed_nil",
			construct: func() (vibes.CapabilityAdapter, error) {
				return vibes.NewJobQueueCapability("jobs", typedNilQueue)
			},
			wantErr: "vibes: job queue capability requires a non-nil implementation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := tc.construct()
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("error = %q, want %q", err, tc.wantErr)
			}
			if adapter != nil {
				t.Fatalf("constructor returned non-nil adapter alongside error %q", err)
			}
		})
	}
}

func TestCapabilityConstructorsSucceed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		construct func() (vibes.CapabilityAdapter, error)
	}{
		{
			name:      "context",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewContextCapability("ctx", stubContextResolver) },
		},
		{
			name:      "db",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewDBCapability("db", stubDatabase{}) },
		},
		{
			name: "events",
			construct: func() (vibes.CapabilityAdapter, error) {
				return vibes.NewEventsCapability("events", stubEventPublisher{})
			},
		},
		{
			name:      "jobqueue",
			construct: func() (vibes.CapabilityAdapter, error) { return vibes.NewJobQueueCapability("jobs", stubJobQueue{}) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := tc.construct()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			if adapter == nil {
				t.Fatal("constructor returned nil adapter without error")
			}
		})
	}
}

func TestMustCapabilityConstructorsPanicOnInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		construct func()
		wantErr   string
	}{
		{
			name:      "context",
			construct: func() { vibes.MustNewContextCapability("", stubContextResolver) },
			wantErr:   "vibes: context capability name must be non-empty",
		},
		{
			name:      "db",
			construct: func() { vibes.MustNewDBCapability("db", nil) },
			wantErr:   "vibes: database capability requires a non-nil implementation",
		},
		{
			name:      "events",
			construct: func() { vibes.MustNewEventsCapability("", stubEventPublisher{}) },
			wantErr:   "vibes: events capability name must be non-empty",
		},
		{
			name:      "jobqueue",
			construct: func() { vibes.MustNewJobQueueCapability("jobs", nil) },
			wantErr:   "vibes: job queue capability requires a non-nil implementation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic, got none")
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("panic value = %T, want error", r)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("panic error = %q, want %q", err, tc.wantErr)
				}
			}()
			tc.construct()
		})
	}
}

func TestMustCapabilityConstructorsReturnAdapters(t *testing.T) {
	t.Parallel()

	adapters := map[string]vibes.CapabilityAdapter{
		"context":  vibes.MustNewContextCapability("ctx", stubContextResolver),
		"db":       vibes.MustNewDBCapability("db", stubDatabase{}),
		"events":   vibes.MustNewEventsCapability("events", stubEventPublisher{}),
		"jobqueue": vibes.MustNewJobQueueCapability("jobs", stubJobQueue{}),
	}
	for name, adapter := range adapters {
		if adapter == nil {
			t.Errorf("MustNew %s capability returned nil adapter", name)
		}
	}
}

func TestContextCapabilityResolvesThroughScriptCall(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile("def who()\n  ctx[:tenant]\nend")
	if err != nil {
		t.Fatal(err)
	}

	result, err := script.Call(context.Background(), "who", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewContextCapability("ctx", stubContextResolver),
		},
	})
	if err != nil {
		t.Fatalf("Call(who) error: %v", err)
	}
	if result.String() != "acme" {
		t.Fatalf("Call(who) = %q, want %q", result.String(), "acme")
	}
}

func TestContextCapabilityResolverErrorPropagates(t *testing.T) {
	t.Parallel()

	resolverErr := errors.New("tenant lookup failed")
	failing := func(context.Context) (value.Value, error) {
		return value.NewNil(), resolverErr
	}

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile("def who()\n  ctx[:tenant]\nend")
	if err != nil {
		t.Fatal(err)
	}

	_, err = script.Call(context.Background(), "who", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewContextCapability("ctx", failing),
		},
	})
	if err == nil {
		t.Fatal("Call with failing resolver: expected error, got nil")
	}
	if !errors.Is(err, resolverErr) {
		t.Errorf("Call error %v does not wrap resolver error", err)
	}
	if !strings.Contains(err.Error(), "resolve ctx capability: tenant lookup failed") {
		t.Errorf("Call error = %q, want resolver context prefix", err)
	}
}
